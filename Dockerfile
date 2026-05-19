FROM golang:1.26.2-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /file-service ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates libreoffice-writer ttf-dejavu ttf-liberation
WORKDIR /app
COPY --from=builder /file-service .
COPY templates/ /app/templates/
EXPOSE 8021
ENV FILES_DIR=/app/files
CMD ["./file-service"]
