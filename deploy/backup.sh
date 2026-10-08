#!/bin/sh
set -eu
cd /opt/gk
backup_stamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_dir="/opt/gk/var/backups/$backup_stamp"
./gk backup --db var/db/gk.sqlite --bundle "$backup_dir" --secret-key-file var/secret.key
# Keep every snapshot until the operator chooses a retention policy.
# Copy this directory to another machine; local snapshots alone do not survive disk loss.
printf 'Backup complete: %s\n' "$backup_dir"
