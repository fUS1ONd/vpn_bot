# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies (gcc for CGO/sqlite3)
RUN apk add --no-cache gcc musl-dev

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -a -ldflags '-linkmode external -extldflags "-static"' -o vpn-bot ./cmd/bot

# Runtime stage
FROM alpine:latest

WORKDIR /app

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Copy binary from builder
COPY --from=builder /build/vpn-bot /app/vpn-bot

# Create data directory for SQLite
RUN mkdir -p /app/data

# Run the bot
CMD ["/app/vpn-bot"]
