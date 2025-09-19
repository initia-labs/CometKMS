FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o cmkms ./cmd/cmkms

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app
WORKDIR /app
USER app
COPY --from=builder /app/cmkms ./cmkms
EXPOSE 8080
ENTRYPOINT ["/app/cmkms"]
