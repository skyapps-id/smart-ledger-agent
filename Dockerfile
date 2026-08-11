# ── Build stage ──
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cache dependency.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build static binary (CGO disabled agar portable; sqlite pure-Go via modernc).
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/server ./cmd/server

# ── Runtime stage ──
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 app

WORKDIR /app
COPY --from=builder /out/server /app/server

USER app
EXPOSE 8080

ENTRYPOINT ["/app/server"]
