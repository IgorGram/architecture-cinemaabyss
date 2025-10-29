package main

import (
	"crypto/sha1"
	"encoding/binary"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Targets struct {
	Monolith *httputil.ReverseProxy
	Movies   *httputil.ReverseProxy
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		log.Fatalf("invalid url %q: %v", raw, err)
	}
	return u
}

func newReverseProxy(target *url.URL) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(target)
	// Опционально: таймауты/директ и пр. (оставим по-простому)
	origDirector := p.Director
	p.Director = func(r *http.Request) {
		origDirector(r)
		// Правим Host, чтобы бэкенд видел корректный host
		r.Host = target.Host
	}
	return p
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func bucketByUserID(userID string) int {
	// Детерминированный бакет 0..99
	if userID == "" {
		// Fallback: псевдослучайный, но без липкости
		return rand.Intn(100)
	}
	h := sha1.Sum([]byte(userID))
	// Берём первые 2 байта для распределения
	num := binary.BigEndian.Uint16(h[:2])
	return int(num % 100)
}

func main() {
	rand.Seed(time.Now().UnixNano())

	port := getEnv("PORT", "8000")
	monolithURL := getEnv("MONOLITH_BASE_URL", "http://monolith:8080")
	moviesURL := getEnv("MOVIES_BASE_URL", "http://movies:8081")
	percentStr := getEnv("MOVIES_MIGRATION_PERCENT", "0")

	migrationPercent, err := strconv.Atoi(percentStr)
	if err != nil || migrationPercent < 0 || migrationPercent > 100 {
		log.Fatalf("MOVIES_MIGRATION_PERCENT must be 0..100, got: %q", percentStr)
	}

	targets := &Targets{
		Monolith: newReverseProxy(mustParseURL(monolithURL)),
		Movies:   newReverseProxy(mustParseURL(moviesURL)),
	}

	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Маршрутизация фильмов с процентом
	mux.Handle("/api/movies", proxyMovies(targets, migrationPercent))
	mux.Handle("/api/movies/", proxyMovies(targets, migrationPercent))

	// Остальное — в монолит (минимально достаточно для задания)
	mux.Handle("/", targets.Monolith)

	addr := ":" + port
	log.Printf("Proxy listening on %s", addr)
	log.Printf("MONOLITH_BASE_URL=%s, MOVIES_BASE_URL=%s, MOVIES_MIGRATION_PERCENT=%d",
		monolithURL, moviesURL, migrationPercent)

	srv := &http.Server{
		Addr:         addr,
		Handler:      logRequests(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s ua=%q dur=%s",
			r.RemoteAddr, r.Method, r.URL.Path, r.UserAgent(), time.Since(start))
	})
}

func proxyMovies(t *Targets, percent int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Липкий ключ — сначала заголовок, потом кука, иначе пусто
		userID := strings.TrimSpace(r.Header.Get("X-User-Id"))
		if userID == "" {
			if c, err := r.Cookie("uid"); err == nil {
				userID = c.Value
			}
		}
		bucket := bucketByUserID(userID)

		// Решение: если bucket < percent → новый movies, иначе → монолит
		if bucket < percent {
			w.Header().Set("X-Routed-To", "movies")
			t.Movies.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-Routed-To", "monolith")
		t.Monolith.ServeHTTP(w, r)
	})
}
