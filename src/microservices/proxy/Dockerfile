# ---- build stage ----
FROM golang:1.22 AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o proxy main.go

# ---- run stage ----
FROM gcr.io/distroless/base-debian12
WORKDIR /app
ENV PORT=8000
ENV MONOLITH_BASE_URL=http://monolith:8080
ENV MOVIES_BASE_URL=http://movies:8081
ENV MOVIES_MIGRATION_PERCENT=0
EXPOSE 8000
COPY --from=builder /app/proxy /app/proxy
USER nonroot:nonroot
ENTRYPOINT ["/app/proxy"]
