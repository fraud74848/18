#!/bin/sh
# Run from the local-PostgreSQL server deployment. Never copies a live PGDATA directory.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
umask 077
mkdir -p backups
backup_file="backups/attendance-$(date -u +%Y%m%dT%H%M%SZ)-$$.dump"
temporary_file="${backup_file}.partial"
trap 'rm -f "$temporary_file"' EXIT HUP INT TERM
docker compose exec -T db pg_dump -U attendance -d attendance --format=custom --no-owner --no-privileges > "$temporary_file"
# Validate archive metadata before reporting success.
docker compose exec -T db pg_restore --list < "$temporary_file" > /dev/null
mv "$temporary_file" "$backup_file"
printf 'Backup saved: %s\n' "$backup_file"
