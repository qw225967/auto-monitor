#!/bin/bash
# 一键启动：后端 + 前端 Web

set -e
cd "$(dirname "$0")/.."
ROOT=$(pwd)

# 端口
BACKEND_PORT=8088
FRONTEND_PORT=5173

# PID 文件
PID_BACKEND="$ROOT/.pid.backend"
PID_FRONTEND="$ROOT/.pid.frontend"

log() { echo "[$(date +%H:%M:%S)] $*"; }

stop_service() {
  local name=$1
  local pid_file=$2
  if [[ -f "$pid_file" ]]; then
    local pid=$(cat "$pid_file")
    if kill -0 "$pid" 2>/dev/null; then
      log "停止 $name (PID $pid)"
      kill "$pid" 2>/dev/null || true
      sleep 1
      kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$pid_file"
  fi
}

stop_by_port() {
  local port=$1
  local name=$2
  if command -v lsof &>/dev/null; then
    local pids=$(lsof -ti :$port 2>/dev/null || true)
    if [[ -n "$pids" ]]; then
      log "停止占用 $port 端口的 $name"
      echo "$pids" | xargs kill -9 2>/dev/null || true
      sleep 1
    fi
  elif command -v fuser &>/dev/null; then
    fuser -k $port/tcp 2>/dev/null || true
    sleep 1
  fi
}

stop_all() {
  log "停止现有服务..."
  stop_service "后端" "$PID_BACKEND"
  stop_service "前端" "$PID_FRONTEND"
  stop_by_port $BACKEND_PORT "后端"
  stop_by_port $FRONTEND_PORT "前端"
}

start_backend() {
  log "启动后端 (端口 $BACKEND_PORT)..."
  cd "$ROOT"
  nohup go run ./cmd/server >> logs/backend.log 2>&1 &
  echo $! > "$PID_BACKEND"
  log "后端已启动 PID $(cat $PID_BACKEND)"
}

start_frontend() {
  log "启动前端 (端口 $FRONTEND_PORT)..."
  cd "$ROOT/frontend"
  if [[ ! -d node_modules ]]; then
    log "安装前端依赖..."
    npm install
  fi
  nohup npm run dev -- --host >> ../logs/frontend.log 2>&1 &
  echo $! > "$PID_FRONTEND"
  log "前端已启动 PID $(cat $PID_FRONTEND)"
}

# 创建日志目录
mkdir -p "$ROOT/logs"

case "${1:-start}" in
  start)
    stop_all
    start_backend
    sleep 2
    start_frontend
    log "启动完成"
    log "  后端: http://localhost:$BACKEND_PORT"
    log "  前端: http://localhost:$FRONTEND_PORT"
    log "  日志: logs/backend.log, logs/frontend.log"
    ;;
  stop)
    stop_all
    log "已停止"
    ;;
  restart)
    stop_all
    sleep 2
    start_backend
    sleep 2
    start_frontend
    log "重启完成"
    log "  前端: http://localhost:$FRONTEND_PORT"
    ;;
  *)
    echo "用法: $0 {start|stop|restart}"
    echo "  start   - 启动后端+前端 (默认)"
    echo "  stop    - 停止所有服务"
    echo "  restart - 重启所有服务"
    exit 1
    ;;
esac
