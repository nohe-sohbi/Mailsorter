# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies (alpine already has ca-certificates in golang image)
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o /mailsorter ./cmd/server

# Final stage
FROM alpine:latest

WORKDIR /root/

# Copy ca-certificates from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the binary from builder
COPY --from=builder /mailsorter .

EXPOSE 8080

CMD ["./mailsorter"]
