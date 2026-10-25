#!/usr/bin/env bash
# Сквозная проверка сервиса в контейнере.
#
# Поднимает отдельный стек на своих портах, поэтому запускается рядом
# с рабочим сервисом и не мешает ему. Стек и том удаляются при выходе,
# в том числе при ошибке.
#
#   scripts/smoke.sh
set -euo pipefail

cd "$(dirname "$0")/.."

export COMPOSE_PROJECT_NAME=mchs-smoke
export MCHS_UDP_PORT=${MCHS_SMOKE_UDP_PORT:-15005}
export MCHS_HTTP_PORT=${MCHS_SMOKE_HTTP_PORT:-18080}

api="http://localhost:$MCHS_HTTP_PORT"
failed=0

cleanup() {
    docker compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

ok()   { echo "  ok    $1"; }
fail() { echo "  ПРОВАЛ $1" >&2; failed=1; }

check() {
    local name=$1 expected=$2 actual=$3
    if [[ $actual == *"$expected"* ]]; then
        ok "$name"
    else
        fail "$name: ожидалось «$expected», получено «$actual»"
    fi
}

echo "== поднимаю стек ($COMPOSE_PROJECT_NAME, udp $MCHS_UDP_PORT, http $MCHS_HTTP_PORT)"
# MCHS_QUIET выключаем: проверяем в том числе вывод потребителя в журнал.
MCHS_QUIET=false docker compose up --build -d >/dev/null
scripts/wait-healthy.sh "$api" 30 >/dev/null
ok "сервис поднялся"

echo "== шлю телеметрию"
# Быстрый нагрев: иначе порог защиты в 40 °C не достигается за время проверки.
MCHS_SIM_HEAT=6.0 scripts/simulate.sh 127.0.0.1 "$MCHS_UDP_PORT" 6 >/dev/null
ok "пакеты отправлены"

echo "== битый пакет не должен ронять приём"
python3 -c "
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.sendto(b'not a json', ('127.0.0.1', $MCHS_UDP_PORT))
"
sleep 0.5
check "битый пакет отброшен" "пакет не разобран" "$(docker compose logs 2>&1)"

echo "== HTTP API"
check "healthz отвечает 200" "200" \
    "$(curl -s -o /dev/null -w '%{http_code}' "$api/healthz")"

devices=$(curl -fsS "$api/api/v1/devices")
check "устройство видно в списке" '"device"' "$devices"
check "показания дошли" '"rpm"' "$devices"

id=$(python3 -c "
import json, sys
d = json.loads(sys.argv[1])
print(d[0]['device'] if d else '')
" "$devices")

if [[ -n $id ]]; then
    check "устройство доступно поимённо" '"device"' \
        "$(curl -fsS "$api/api/v1/devices/$id")"
else
    fail "список устройств пуст"
fi

check "неизвестное устройство — 404" "404" \
    "$(curl -s -o /dev/null -w '%{http_code}' "$api/api/v1/devices/10.0.0.1")"

echo "== CSV на томе"
csv=$(scripts/show-csv.sh)
check "заголовок CSV на месте" "timestamp,source,event" "$csv"
check "показания записаны" ",sensor," "$csv"
check "события реле записаны" ",relay," "$csv"
check "нажатия клавиш записаны" ",key," "$csv"

echo "== мягкая остановка"
before=$(scripts/show-csv.sh | wc -l)
docker compose stop >/dev/null
after=$(scripts/show-csv.sh 2>/dev/null | wc -l || echo 0)
check "хвост CSV не потерян ($after строк)" "$before" "$after"
check "итог подведён" "итог" "$(docker compose logs --tail 5 2>&1)"

echo
if ((failed)); then
    echo "ПРОВЕРКА НЕ ПРОЙДЕНА" >&2
    exit 1
fi
echo "ПРОВЕРКА ПРОЙДЕНА"
