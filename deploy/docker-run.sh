#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

ACTION="${1:-start}"
IMAGE_NAME="${IMAGE_NAME:-practice-gk}"
IMAGE_TAG="${IMAGE_TAG:-latest}"

case "$ACTION" in
  start)
    docker compose up -d --wait
    echo "服务已启动：容器端口仅绑定 127.0.0.1，请通过宿主机反代访问（端口见 .env 的 GK_HOST_PORT）"
    ;;
  stop)
    docker compose down
    echo "服务已停止"
    ;;
  restart)
    docker compose down
    docker compose up -d --wait
    echo "服务已重启"
    ;;
  build)
    docker compose build --pull
    echo "镜像构建完成: ${IMAGE_NAME}:${IMAGE_TAG}"
    ;;
  rebuild)
    docker compose build --pull --no-cache
    docker compose down
    docker compose up -d --wait
    echo "全量重建完成"
    ;;
  logs)
    docker compose logs -f --tail=200
    ;;
  shell)
    docker compose exec gk sh
    ;;
  status)
    docker compose ps
    ;;
  *)
    echo "用法: ./docker-run.sh {start|stop|restart|build|rebuild|logs|shell|status}"
    exit 1
    ;;
esac
