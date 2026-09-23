# Multi-stage ultra-lightweight Dockerfile
# Stage 1: Build static Go binary with build cache
FROM golang:alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk --no-cache add git ca-certificates

ENV GOPROXY=https://goproxy.cn,direct

# Cache dependencies
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy source code and embedded static assets
COPY . .

# Build statically linked binary with build cache for lightning-fast rebuilds (< 2s)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/ow-oc-controller main.go

# Stage 2: Minimal runtime image based on Alpine (Total size ~ 15MB)
FROM alpine:latest

WORKDIR /app

# Add ca-certificates and tzdata
RUN apk --no-cache add ca-certificates tzdata

# Copy binary from builder
COPY --from=builder /app/ow-oc-controller /app/ow-oc-controller

# Environment defaults
ENV PORT=8080 \
    GOMEMLIMIT=32MiB

EXPOSE 8080

# Healthcheck probe
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/health || exit 1

CMD ["/app/ow-oc-controller"]
