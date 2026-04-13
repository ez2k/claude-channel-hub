FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /claude-harness ./cmd/bot

FROM alpine:3.19
RUN apk add --no-cache bash curl ca-certificates
COPY --from=builder /claude-harness /claude-harness
COPY configs/ /app/configs/
COPY skills/  /app/skills/
WORKDIR /app
VOLUME /app/data
ENTRYPOINT ["/claude-harness", "-config", "/app/configs/channels.yaml", "-data", "/app/data"]
