#!/bin/sh
set -eu
cd /opt/gk
backup_stamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_dir="/opt/gk/var/backups/$backup_stamp"
mkdir -p "$backup_dir"
./gk backup --db var/db/gk.sqlite --out "$backup_dir/gk.sqlite"
if [ -f var/secret.key ]; then
    cp var/secret.key "$backup_dir/secret.key"
    chmod 600 "$backup_dir/secret.key"
fi
# Keep every snapshot until the operator chooses a retention policy.
# Copy this directory to another machine; local snapshots alone do not survive disk loss.
printf 'Backup complete: %s\n' "$backup_dir"
