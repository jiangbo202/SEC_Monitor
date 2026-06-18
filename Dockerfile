FROM node:20-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.24 AS backend
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN go build -o /out/sec-monitor ./cmd/server

FROM debian:bookworm-slim
WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /app/data
COPY --from=backend /out/sec-monitor /app/sec-monitor
COPY --from=frontend /src/web/dist /app/web
ENV APP_ADDR=:8080
ENV DB_TYPE=sqlite
ENV DB_DSN=/app/data/sec_monitor.db
ENV WEB_DIST_DIR=/app/web
EXPOSE 8080
CMD ["/app/sec-monitor"]
