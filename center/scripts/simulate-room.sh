#!/usr/bin/env bash
# Обёртка над scripts/simulate_room.py для тех, кому удобнее из shell.
# На хосте без Python тот же имитатор поднимается в контейнере:
#
#   docker compose --profile sim up simulator
#
#   scripts/simulate-room.sh [адрес центра] [логин] [пароль] [секунд]
set -euo pipefail

center=${1:-http://127.0.0.1:8080}
login=${2:-operator}
password=${3:-${MCHS_PASSWORD:-}}
seconds=${4:-0}

echo "шлю телеметрию комнаты на $center (Ctrl-C для остановки)"
exec python3 "$(dirname "$0")/simulate_room.py" \
  "$center" "$login" "$password" "$seconds" "${MCHS_SITE:-obj-17}"
