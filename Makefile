.PHONY: start stop restart status logs backend frontend test lint build docker-build docker-up docker-down docker-logs docker-discovery-sync docker-discovery-market-sync

start:
	./scripts/local.sh start

stop:
	./scripts/local.sh stop

restart:
	./scripts/local.sh restart

status:
	./scripts/local.sh status

logs:
	./scripts/local.sh logs

backend:
	go run ./cmd/server

frontend:
	cd web && npm run dev

test:
	go test ./...

lint:
	golangci-lint run

build:
	go build -o dist/sec-monitor ./cmd/server
	cd web && npm run build

docker-build:
	docker build -t sec-monitor:local .

docker-up:
	./scripts/local.sh stop
	docker compose up -d --build

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f sec-monitor

docker-discovery-sync:
	docker compose exec sec-monitor /app/discovery-sync

docker-discovery-market-sync:
	docker compose exec -e DISCOVERY_SYNC_PHASE=market sec-monitor /app/discovery-sync
