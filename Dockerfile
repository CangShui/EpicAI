FROM golang:1.26-bookworm AS builder
WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/epicai ./cmd/epicai

FROM debian:bookworm
WORKDIR /app
COPY --from=builder /app/epicai /app/epicai
VOLUME ["/app/data", "/app/logs"]
EXPOSE 8000
ENV EPICAI_HOST=0.0.0.0 \
    EPICAI_PORT=8000 \
    EPICAI_DATABASE_URL=sqlite:data/epicai.db \
    EPICAI_STORAGE_PATH=data/storage
ENTRYPOINT ["/app/epicai"]
