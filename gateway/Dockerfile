FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /gateway ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /gateway /gateway
ENTRYPOINT ["/gateway"]
