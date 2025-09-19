FROM golang:1.24.3-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o cmkms ./cmd/cmkms

FROM alpine:3.18
WORKDIR /app
COPY --from=builder /app/cmkms ./cmkms
EXPOSE 8080
ENTRYPOINT ["./cmkms"]
