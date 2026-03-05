# ── Stage 1: build
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy deps first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o arbiter ./cmd/main.go

# ── Stage 2: runtime
FROM alpine:3.21

RUN apk --no-cache add ca-certificates wget

COPY --from=builder /app/arbiter /arbiter

EXPOSE 3000

ENTRYPOINT ["/arbiter"]
