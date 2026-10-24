#!/bin/sh
# Ежедневная копия базы админки. Ставится в cron на сервере:
#   /etc/cron.d/vendetta-backup → 30 4 * * * root /var/www/vendetta_admin/deploy/backup.sh
#
# Дамп в формате pg_dump -Fc (сжат, восстанавливается pg_restore) кладётся
# в /root/vendetta-backups/daily-ГГГГ-ММ-ДД.dump; копии старше KEEP_DAYS
# удаляются. Ручные дампы перед миграциями (before-*.dump) не трогаются.
#
# Восстановление (база admin_pg должна быть пустой или пересозданной):
#   docker exec -i admin_pg pg_restore -U admin -d admin --clean --if-exists < daily-….dump
set -eu

APP_DIR=${APP_DIR:-/var/www/vendetta_admin}
DIR=${BACKUP_DIR:-/root/vendetta-backups}
KEEP_DAYS=${KEEP_DAYS:-14}

cd "$APP_DIR"
U=$(grep '^POSTGRES_USER=' .env | cut -d= -f2)
D=$(grep '^POSTGRES_DB=' .env | cut -d= -f2)
mkdir -p "$DIR"

out="$DIR/daily-$(date +%F).dump"
tmp="$out.part"
# Сначала во временный файл: оборвавшийся дамп не должен выглядеть целым.
docker exec admin_pg pg_dump -U "${U:-admin}" -d "${D:-admin}" -Fc > "$tmp"
# Пустой или крошечный файл — признак сбоя, а не копии.
if [ "$(wc -c < "$tmp")" -lt 10000 ]; then
    echo "backup: дамп подозрительно мал, оставляю $tmp" >&2
    exit 1
fi
mv "$tmp" "$out"
find "$DIR" -name 'daily-*.dump' -mtime +"$KEEP_DAYS" -delete
echo "backup: $out ($(du -h "$out" | cut -f1))"
