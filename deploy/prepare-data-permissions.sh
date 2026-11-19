#!/bin/sh
# Host-side preparation for the fixed non-root Docker user. No database/key creation.
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$project_root"
case "${1:-prepare}" in
  prepare|--check-paths) ;;
  *) printf '%s\n' '用法: prepare-data-permissions.sh [--check-paths]' >&2; exit 1 ;;
esac

as_root() {
  if [ "$(id -u)" = 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo -- "$@"
  else
    printf '%s\n' '需要 root 或 sudo 修正宿主机数据权限，请使用 sudo sh deploy/docker-run.sh prepare。' >&2
    exit 1
  fi
}

fail() {
  printf '权限准备失败：%s\n' "$1" >&2
  exit 1
}

# Reject links before any chmod/chown, so privileged preparation stays within var.
for directory in var var/db var/logs var/backups var/runs; do
  if as_root test -L "$directory"; then
    fail "$directory 是符号链接；请确认并手工处理实际数据路径。"
  fi
  if as_root test -e "$directory" && ! as_root test -d "$directory"; then
    fail "$directory 不是目录。"
  fi
done
if as_root test -L var/secret.key; then
  fail 'var/secret.key 是符号链接；不修改链接目标。'
fi
if as_root test -e var/secret.key && ! as_root test -f var/secret.key; then
  fail 'var/secret.key 不是普通文件。'
fi
if as_root test -d var/db; then
  gk_db_links=$(as_root find var/db -type l -print -quit) || fail '无法完成数据库符号链接检查；未调整权限。'
  if [ -n "$gk_db_links" ]; then
    fail 'var/db 中存在符号链接；不修改链接目标。'
  fi
  gk_db_hardlinks=$(as_root find var/db -type f -links +1 -print -quit) || fail '无法完成数据库硬链接检查；未调整权限。'
  if [ -n "$gk_db_hardlinks" ]; then
    fail 'var/db 中存在硬链接；请使用独立的数据文件。'
  fi
fi
if as_root test -f var/secret.key; then
  gk_key_hardlinks=$(as_root find var/secret.key -type f -links +1 -print -quit) || fail '无法完成密钥硬链接检查；未调整权限。'
  if [ -n "$gk_key_hardlinks" ]; then
    fail 'var/secret.key 存在硬链接；请使用独立的密钥文件。'
  fi
fi

# Reuse exactly the same path policy for the read-only check action.
if [ "${1:-prepare}" = --check-paths ]; then
  exit 0
fi

as_root install -d -o 10001 -g 10001 -m 750 -- var
as_root install -d -o 10001 -g 10001 -m 700 -- var/db var/logs var/backups var/runs
as_root chown -hR 10001:10001 -- var/db
as_root find var/db -type d -exec chmod 700 {} +
as_root find var/db -type f -exec chmod 600 {} +
if as_root test -f var/secret.key; then
  as_root chown -h 10001:10001 -- var/secret.key
  as_root chmod 600 -- var/secret.key
fi
printf '%s\n' '宿主机数据权限已准备：UID/GID 10001:10001；数据库和已有密钥保持私有，未创建或替换其内容。'
