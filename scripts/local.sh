#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

APP_ADDR="${APP_ADDR:-127.0.0.1:8080}"
FRONTEND_HOST="${FRONTEND_HOST:-127.0.0.1}"
FRONTEND_PORT="${FRONTEND_PORT:-5173}"
LOCAL_DATE="${LOCAL_DATE:-$(date +%F)}"
LOCAL_RETENTION_DAYS="${LOCAL_RETENTION_DAYS:-14}"
LOCAL_LOGS_BY_DAY="${LOCAL_LOGS_BY_DAY:-1}"
LOCAL_DATA_BY_DAY="${LOCAL_DATA_BY_DAY:-0}"
LOCAL_START_TIMEOUT="${LOCAL_START_TIMEOUT:-60}"

RUNTIME_DIR="$ROOT_DIR/.runtime"
PID_BACKEND="$RUNTIME_DIR/backend.pid"
PID_FRONTEND="$RUNTIME_DIR/frontend.pid"
BACKEND_BIN="$RUNTIME_DIR/sec-monitor-dev"
GO_BUILD_CACHE="$ROOT_DIR/.cache/go-build"
GO_MOD_CACHE="$ROOT_DIR/.cache/go-mod"

if [[ "$LOCAL_LOGS_BY_DAY" == "1" || "$LOCAL_LOGS_BY_DAY" == "true" ]]; then
  LOG_DIR="$ROOT_DIR/logs/$LOCAL_DATE"
else
  LOG_DIR="$ROOT_DIR/logs"
fi

if [[ -n "${DB_DSN:-}" ]]; then
  LOCAL_DB_DSN="$DB_DSN"
elif [[ "$LOCAL_DATA_BY_DAY" == "1" || "$LOCAL_DATA_BY_DAY" == "true" ]]; then
  LOCAL_DB_DSN="$ROOT_DIR/data/$LOCAL_DATE/sec_monitor.db"
else
  LOCAL_DB_DSN="$ROOT_DIR/data/sec_monitor.db"
fi

BACKEND_LOG="$LOG_DIR/backend.log"
FRONTEND_LOG="$LOG_DIR/frontend.log"

mkdir -p "$RUNTIME_DIR" "$LOG_DIR" "$(dirname "$LOCAL_DB_DSN")" "$GO_BUILD_CACHE" "$GO_MOD_CACHE"

usage() {
  cat <<'USAGE'
Usage: ./scripts/local.sh <command>

Commands:
  start      Start backend and frontend
  stop       Stop backend and frontend
  restart    Stop then start backend and frontend
  status     Show process and health status
  logs       Tail backend and frontend logs
  backend    Start backend only
  frontend   Start frontend only

Config:
  APP_ADDR=127.0.0.1:8080
  FRONTEND_HOST=127.0.0.1
  FRONTEND_PORT=5173
  LOCAL_RETENTION_DAYS=14
  LOCAL_START_TIMEOUT=60
  LOCAL_LOGS_BY_DAY=1
  LOCAL_DATA_BY_DAY=0
  LOCAL_DATE=YYYY-MM-DD
USAGE
}

pid_alive() {
  local pid="$1"
  [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1
}

read_pid() {
  local file="$1"
  [[ -f "$file" ]] && tr -d '[:space:]' < "$file" || true
}

service_running() {
  local pid_file="$1"
  local pid
  pid="$(read_pid "$pid_file")"
  pid_alive "$pid"
}

wait_http() {
  local url="$1"
  local attempts="${2:-30}"
  local i
  for ((i = 1; i <= attempts; i++)); do
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

cleanup_old_dirs() {
  local base="$1"
  [[ "$LOCAL_RETENTION_DAYS" =~ ^[0-9]+$ ]] || return 0
  [[ "$LOCAL_RETENTION_DAYS" -gt 0 ]] || return 0
  [[ -d "$base" ]] || return 0
  find "$base" -mindepth 1 -maxdepth 1 -type d -name '????-??-??' -mtime +"$LOCAL_RETENTION_DAYS" -print -exec rm -rf {} + 2>/dev/null || true
}

start_backend() {
  if service_running "$PID_BACKEND"; then
    echo "backend already running pid=$(read_pid "$PID_BACKEND")"
    return 0
  fi

  echo "building backend"
  GOCACHE="$GO_BUILD_CACHE" GOMODCACHE="$GO_MOD_CACHE" go build -o "$BACKEND_BIN" ./cmd/server >>"$BACKEND_LOG" 2>&1

  echo "starting backend on http://$APP_ADDR"
  nohup env \
    APP_ADDR="$APP_ADDR" \
    DB_DSN="$LOCAL_DB_DSN" \
    DATA_RETENTION_DAYS="${DATA_RETENTION_DAYS:-$LOCAL_RETENTION_DAYS}" \
    STORAGE_BY_DAY="${STORAGE_BY_DAY:-$LOCAL_DATA_BY_DAY}" \
    "$BACKEND_BIN" >>"$BACKEND_LOG" 2>&1 &
  echo "$!" > "$PID_BACKEND"

  if wait_http "http://$APP_ADDR/healthz" "$LOCAL_START_TIMEOUT"; then
    echo "backend ready"
  else
    echo "backend did not become healthy; see $BACKEND_LOG" >&2
    stop_one "backend" "$PID_BACKEND" >/dev/null 2>&1 || true
    return 1
  fi
}

start_frontend() {
  if service_running "$PID_FRONTEND"; then
    echo "frontend already running pid=$(read_pid "$PID_FRONTEND")"
    return 0
  fi

  if [[ ! -x "$ROOT_DIR/web/node_modules/.bin/vite" ]]; then
    echo "installing frontend dependencies"
    (cd "$ROOT_DIR/web" && npm install) >>"$FRONTEND_LOG" 2>&1
  fi

  echo "starting frontend on http://$FRONTEND_HOST:$FRONTEND_PORT"
  (
    cd "$ROOT_DIR/web"
    nohup ./node_modules/.bin/vite --host "$FRONTEND_HOST" --port "$FRONTEND_PORT" >>"$FRONTEND_LOG" 2>&1 &
    echo "$!" > "$PID_FRONTEND"
  )

  if wait_http "http://$FRONTEND_HOST:$FRONTEND_PORT/" "$LOCAL_START_TIMEOUT"; then
    echo "frontend ready"
  else
    echo "frontend did not become healthy; see $FRONTEND_LOG" >&2
    stop_one "frontend" "$PID_FRONTEND" >/dev/null 2>&1 || true
    return 1
  fi
}

stop_one() {
  local name="$1"
  local pid_file="$2"
  local pid
  pid="$(read_pid "$pid_file")"
  if ! pid_alive "$pid"; then
    echo "$name not running"
    rm -f "$pid_file"
    return 0
  fi

  echo "stopping $name pid=$pid"
  kill "$pid" >/dev/null 2>&1 || true
  for _ in {1..20}; do
    if ! pid_alive "$pid"; then
      rm -f "$pid_file"
      echo "$name stopped"
      return 0
    fi
    sleep 0.5
  done
  echo "$name still running; sending SIGKILL"
  kill -9 "$pid" >/dev/null 2>&1 || true
  rm -f "$pid_file"
}

status_one() {
  local name="$1"
  local pid_file="$2"
  local url="$3"
  local pid
  pid="$(read_pid "$pid_file")"
  if pid_alive "$pid"; then
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      echo "$name: running pid=$pid healthy url=$url"
    else
      echo "$name: running pid=$pid unhealthy url=$url"
    fi
  else
    echo "$name: stopped"
  fi
}

start_all() {
  cleanup_old_dirs "$ROOT_DIR/logs"
  if [[ "$LOCAL_DATA_BY_DAY" == "1" || "$LOCAL_DATA_BY_DAY" == "true" ]]; then
    cleanup_old_dirs "$ROOT_DIR/data"
  fi
  start_backend
  start_frontend
  echo "logs: $LOG_DIR"
  echo "database: $LOCAL_DB_DSN"
}

stop_all() {
  stop_one "frontend" "$PID_FRONTEND"
  stop_one "backend" "$PID_BACKEND"
}

case "${1:-}" in
  start)
    start_all
    ;;
  backend)
    cleanup_old_dirs "$ROOT_DIR/logs"
    start_backend
    ;;
  frontend)
    cleanup_old_dirs "$ROOT_DIR/logs"
    start_frontend
    ;;
  stop)
    stop_all
    ;;
  restart)
    stop_all
    start_all
    ;;
  status)
    status_one "backend" "$PID_BACKEND" "http://$APP_ADDR/healthz"
    status_one "frontend" "$PID_FRONTEND" "http://$FRONTEND_HOST:$FRONTEND_PORT/"
    echo "logs: $LOG_DIR"
    echo "database: $LOCAL_DB_DSN"
    ;;
  logs)
    touch "$BACKEND_LOG" "$FRONTEND_LOG"
    tail -n 80 -f "$BACKEND_LOG" "$FRONTEND_LOG"
    ;;
  *)
    usage
    exit 2
    ;;
esac
