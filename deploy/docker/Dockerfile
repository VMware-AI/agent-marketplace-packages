# Multi-stage Dockerfile for marketplace-api.
#
# Stage 1: build a static Go binary.
# Stage 2: ship it on Alpine with ca-certificates.
#
# Final image is ~30-40 MB.

# ---- builder ----
FROM golang:1.23-alpine AS builder
WORKDIR /src

# Pre-copy go.mod/go.sum for layer caching.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ internal/ ./
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out/marketplace-api \
    ./cmd/marketplace-api

# ---- runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /out/marketplace-api /usr/local/bin/marketplace-api

# /etc/agent-marketplace: mount config.yaml here
# /var/lib/agent-marketplace/dist: mount dist/ here
RUN mkdir -p /etc/agent-marketplace /var/lib/agent-marketplace

EXPOSE 8443
ENTRYPOINT ["/usr/local/bin/marketplace-api"]
CMD ["--config", "/etc/agent-marketplace/config.yaml"]