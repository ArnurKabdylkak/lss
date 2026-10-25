#!/usr/bin/env bash
# Восстановление базы центра из выгрузки.
#
#   scripts/db-restore.sh backups/mchs-20261017-012345.archive.gz
#
# Существующие записи заменяются (--drop): восстановление из копии
# означает «вернуть как было», а не «подмешать к текущему».
set -euo pipefail

cd "$(dirname "$0")/.."

archive=${1:-}
if [ -z "$archive" ] || [ ! -f "$archive" ]; then
  echo "укажите файл выгрузки: scripts/db-restore.sh backups/mchs-*.archive.gz" >&2
  exit 1
fi

if ! docker compose ps --status running --services 2>/dev/null | grep -qx mongo; then
  echo "ошибка: контейнер mongo не запущен" >&2
  exit 1
fi

echo "ВНИМАНИЕ: текущее содержимое базы ${MCHS_MONGO_DB:-mchs} будет заменено"
echo "содержимым $archive"
if [ "${MCHS_DB_RESTORE_FORCE:-}" != "1" ]; then
  if [ -t 0 ]; then
    read -r -p "Продолжить? введите «да»: " answer
    case "$answer" in
      да | Да | ДА) ;;
      *) echo "отменено"; exit 1 ;;
    esac
  else
    echo "Запуск не интерактивный. Повторите с MCHS_DB_RESTORE_FORCE=1." >&2
    exit 1
  fi
fi

docker compose exec -T mongo \
  mongorestore --quiet --archive --gzip --drop < "$archive"

echo "восстановлено из $archive"
