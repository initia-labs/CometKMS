# syntax=docker/dockerfile:1.6

FROM golang:1.24-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT

ENV CGO_ENABLED=0
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    mkdir -p /out; \
    GOOS="$TARGETOS"; \
    GOARCH="$TARGETARCH"; \
    GOARM=""; \
    if [ "$GOARCH" = "arm" ] && [ -n "$TARGETVARIANT" ]; then \
      GOARM="${TARGETVARIANT#v}"; \
    fi; \
    if [ "$GOARCH" = "arm" ] && [ -z "$GOARM" ]; then \
      GOARM=7; \
    fi; \
    if [ -n "$GOARM" ]; then \
      GOOS="$GOOS" GOARCH="$GOARCH" GOARM="$GOARM" \
        go build -trimpath -ldflags="-s -w" -o /out/cmkms ./cmd/cmkms; \
    else \
      GOOS="$GOOS" GOARCH="$GOARCH" \
        go build -trimpath -ldflags="-s -w" -o /out/cmkms ./cmd/cmkms; \
    fi

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S cmkms && adduser -S -G cmkms cmkms

WORKDIR /app
COPY --from=builder /out/cmkms /usr/local/bin/cmkms

USER cmkms
EXPOSE 8080
ENTRYPOINT ["cmkms"]
