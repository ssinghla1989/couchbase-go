## Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /app

# Install build tools
RUN apk add --no-cache git ca-certificates && update-ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /app/bin/api ./cmd/api

## Runtime stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && update-ca-certificates
WORKDIR /srv
COPY --from=builder /app/bin/api /usr/local/bin/api

ENV SERVER_PORT=8080
# Set memory limit hint for Go runtime (adjust at deploy time)
ENV GOMEMLIMIT=256MiB
EXPOSE 8080

# Run as non-root user
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/api"]

