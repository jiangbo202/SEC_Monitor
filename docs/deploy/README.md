# Deployment

## Docker Image

The project builds a single image that serves:

- Vue frontend from `/`
- Gin API from `/api`
- health check from `/healthz`

Build:

```bash
docker build -t sec-monitor:local .
```

Run:

```bash
docker run -d \
  --name sec-monitor \
  -p 8080:8080 \
  -v sec-monitor-data:/app/data \
  -e APP_ADDR=:8080 \
  -e DB_DSN=/app/data/sec_monitor.db \
  -e WEB_DIST_DIR=/app/web \
  -e SEC_USER_AGENT="sec-monitor/0.1 your-email@example.com" \
  sec-monitor:local
```

Open `http://127.0.0.1:8080`.

## Docker Compose

```bash
docker compose up -d --build
docker compose logs -f sec-monitor
docker compose down
```

When switching from local development to Docker, stop local services first:

```bash
make stop
```

The `make docker-up` target already does this before starting Compose.

The default compose file stores SQLite data in the named volume `sec-monitor-data`.

## Publish

```bash
docker build -t ghcr.io/<user>/sec-monitor:latest .
docker push ghcr.io/<user>/sec-monitor:latest
```

When deploying a published image, keep `/app/data` mounted as a persistent volume.
