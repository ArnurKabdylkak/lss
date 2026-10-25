#!/usr/bin/env bash
# Ждёт готовности HTTP API сервиса.
#
#   scripts/wait-healthy.sh [http://localhost:8080] [таймаут_секунд]
set -euo pipefail

api=${1:-http://localhost:8080}
timeout=${2:-30}

for ((i = 0; i < timeout * 2; i++)); do
    if curl -fsS -o /dev/null "$api/healthz" 2>/dev/null; then
        echo "API готов: $api"
        exit 0
    fi
    sleep 0.5
done

echo "API не ответил за ${timeout}с: $api" >&2
echo "Журнал:" >&2
docker compose logs --tail 20 >&2 || true
exit 1
