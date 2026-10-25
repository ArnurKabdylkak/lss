#!/usr/bin/env bash
# Выгрузка базы центра из контейнера.
#
# База живёт в томе Docker, а том — не резервная копия: он исчезает
# вместе с `docker compose down -v` и не переживает переустановку
# узла. В нём журнал занятий и история телеметрии, терять которые
# нельзя.
#
#   scripts/db-dump.sh [каталог]     по умолчанию ./backups
set -euo pipefail

cd "$(dirname "$0")/.."

dir=${1:-backups}
stamp=$(date +%Y%m%d-%H%M%S)
archive="$dir/mchs-$stamp.archive.gz"

mkdir -p "$dir"

if ! docker compose ps --status running --services 2>/dev/null | grep -qx mongo; then
  echo "ошибка: контейнер mongo не запущен" >&2
  echo "поднимите стек: docker compose up -d" >&2
  exit 1
fi

# --archive в поток и сжатие: один файл вместо дерева каталогов,
# его проще унести с объекта и положить обратно.
docker compose exec -T mongo \
  mongodump --quiet --db="${MCHS_MONGO_DB:-mchs}" --archive --gzip > "$archive"

echo "выгружено: $archive ($(du -h "$archive" | cut -f1))"
