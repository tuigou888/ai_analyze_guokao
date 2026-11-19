#!/bin/sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$project_root"
ACTION="${1:-start}"

validate_compose() {
  # Do not print the resolved configuration: it may contain GK_SECRET_KEY.
  docker compose config --quiet
}

prepare_data() {
  /bin/sh "$project_root/deploy/prepare-data-permissions.sh"
}

check_data() {
  if [ ! -d var ] || [ -L var ]; then
    printf '%s\n' '启动前检查失败：var 数据目录缺失或为符号链接，请先确认数据路径。' >&2
    return 1
  fi
  /bin/sh "$project_root/deploy/prepare-data-permissions.sh" --check-paths
  # Run as the image's actual user, without publishing service ports or starting Go.
  # No database migration, secret generation or application write happens here.
  docker compose run --rm --no-deps --pull never --entrypoint /bin/sh gk -ec '
    fail() { printf "启动前检查失败：%s\n" "$1" >&2; exit 1; }
    [ "$(id -u)" = 10001 ] && [ "$(id -g)" = 10001 ] || fail "镜像运行用户必须为 10001:10001"
    [ -f /opt/gk/var/db/gk.sqlite ] && [ -s /opt/gk/var/db/gk.sqlite ] || fail "数据库缺失或为空；请恢复原数据或完成首次初始化，禁止创建空库替代"
    [ -r /opt/gk/var/db/gk.sqlite ] && [ -w /opt/gk/var/db/gk.sqlite ] || fail "数据库不可读写"
    [ -w /opt/gk/var/db ] && [ -x /opt/gk/var/db ] || fail "数据库目录不可写或不可进入"
    for path in /opt/gk/var/db/gk.sqlite-wal /opt/gk/var/db/gk.sqlite-shm; do
      if [ -e "$path" ]; then
        [ -f "$path" ] && [ -r "$path" ] && [ -w "$path" ] || fail "SQLite 辅助文件不可读写"
      fi
    done
    # Empty-key detection matches Go strings.TrimSpace, including Unicode spaces.
    gk_unicode_spaces=$(printf "\302\205|\302\240|\341\232\200|\342\200\200|\342\200\201|\342\200\202|\342\200\203|\342\200\204|\342\200\205|\342\200\206|\342\200\207|\342\200\210|\342\200\211|\342\200\212|\342\200\250|\342\200\251|\342\200\257|\342\201\237|\343\200\200")
    gk_key_nonspace=$(printf "%s" "${GK_SECRET_KEY:-}" | LC_ALL=C sed -E "s/[[:space:]]|$gk_unicode_spaces//g")
    if [ -z "$gk_key_nonspace" ]; then
      [ -f /opt/gk/var/secret.key ] && [ -s /opt/gk/var/secret.key ] && [ -r /opt/gk/var/secret.key ] || fail "密钥缺失或不可读；请恢复匹配密钥或提供有效 GK_SECRET_KEY"
    fi
    unset gk_key_nonspace
    printf "%s\n" "启动前权限检查通过（10001:10001）"
  '
}

prepare_and_check() {
  prepare_data
  check_data
}

start_service() {
  docker compose up -d --no-build --wait gk
}

case "$ACTION" in
  start)
    validate_compose
    prepare_and_check
    start_service
    echo '服务已启动：容器端口仅绑定 127.0.0.1，请通过宿主机反代访问（端口见 .env 的 GK_HOST_PORT）'
    ;;
  stop)
    docker compose stop gk
    echo 'gk 服务已停止，容器网络与其他服务保留'
    ;;
  restart)
    validate_compose
    prepare_and_check
    docker compose stop gk
    start_service
    echo '服务已重启'
    ;;
  build)
    validate_compose
    docker compose build --pull gk
    echo 'gk 镜像构建完成'
    ;;
  rebuild)
    validate_compose
    # Build and check before stopping the current service.
    docker compose build --pull --no-cache gk
    prepare_and_check
    docker compose stop gk
    start_service
    echo '全量重建完成'
    ;;
  prepare)
    prepare_data
    ;;
  check)
    validate_compose
    check_data
    ;;
  logs)
    docker compose logs -f --tail=200 gk
    ;;
  shell)
    docker compose exec gk sh
    ;;
  status)
    docker compose ps gk
    ;;
  *)
    echo '用法: sh deploy/docker-run.sh {start|stop|restart|build|rebuild|prepare|check|logs|shell|status}'
    exit 1
    ;;
esac
