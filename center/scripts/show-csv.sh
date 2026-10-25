#!/usr/bin/env bash
# Печатает CSV из тома работающего сервиса.
#
# Контейнер собран на scratch — зайти внутрь нечем, поэтому том
# подключается к отдельному одноразовому контейнеру.
set -euo pipefail

# -a: том нужен и после остановки сервиса, чтобы проверить хвост CSV.
cid=$(docker compose ps -aq telemetryd)
if [[ -z $cid ]]; then
    echo "сервис не запущен: сначала make up" >&2
    exit 1
fi

volume=$(docker inspect -f \
    '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}' "$cid")

if [[ -z $volume ]]; then
    echo "том /data не найден у контейнера $cid" >&2
    exit 1
fi

docker run --rm -v "$volume":/d:ro busybox cat /d/telemetry.csv
