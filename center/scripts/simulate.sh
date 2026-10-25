#!/usr/bin/env bash
# Симулятор платы: шлёт телеметрию так же, как прошивка STM32F767.
#
# Нужен, пока платы нет под рукой, и позже — для разработки сборщика.
# Повторяет контракт из ARCHITECTURE.md §2.1: показания раз в 200 мс,
# события key и relay по ходу дела.
#
#   scripts/simulate.sh [хост] [порт] [секунд]
#
# MCHS_SIM_HEAT — скорость нагрева, °C/с (по умолчанию 0.9). Проверке
# нужен быстрый разогрев, чтобы увидеть срабатывание защиты за секунды.
#
# Без третьего аргумента работает до Ctrl-C.
set -euo pipefail

host=${1:-127.0.0.1}
port=${2:-${MCHS_UDP_PORT:-5005}}
seconds=${3:-0}

echo "шлю телеметрию на $host:$port (Ctrl-C для остановки)"

exec python3 - "$host" "$port" "$seconds" "${MCHS_SIM_HEAT:-0.9}" <<'PY'
import math
import socket
import sys
import time

host, port, seconds = sys.argv[1], int(sys.argv[2]), float(sys.argv[3])
heat = float(sys.argv[4])          # скорость нагрева, °C/с

sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
start = time.monotonic()
period = 0.2                      # REPORT_PERIOD_MS в прошивке
pulses = 0
sent = 0
relay_on = True
last_temp = 23.5


def uptime_ms():
    return int((time.monotonic() - start) * 1000)


def send(payload):
    global sent
    sock.sendto(payload.encode(), (host, port))
    sent += 1


try:
    while True:
        now = time.monotonic() - start
        if seconds and now >= seconds:
            break

        # Обороты гуляют вокруг 1500, как у настоящего вала под нагрузкой.
        rpm = 1500.0 + 120.0 * math.sin(now / 3.0)
        period_us = int(60_000_000 / rpm) if rpm > 0 else 0
        pulses += max(1, int(rpm / 60 * period))

        # Температура ползёт вверх, пока нагрузка включена, и падает,
        # когда защита её сняла, — чтобы увидеть срабатывание реле.
        if relay_on:
            temp = last_temp + heat * period
        else:
            temp = last_temp - heat * period * 1.5
        temp = max(20.0, min(temp, 45.0))
        last_temp = temp

        send(
            '{"rpm":%.1f,"period_us":%d,"pulses":%d,'
            '"temp_c":%.2f,"press_pa":%d,"uptime_ms":%d}'
            % (rpm, period_us, pulses, temp, 101325, uptime_ms())
        )

        # Порог и возврат — те же, что в include/relay.h.
        if relay_on and temp >= 40.0:
            relay_on = False
            send(
                '{"event":"relay","state":"off","reason":"overtemp",'
                '"temp_c":%.2f,"limit_c":40.00,"uptime_ms":%d}'
                % (temp, uptime_ms())
            )
        elif not relay_on and temp <= 37.0:
            relay_on = True
            send(
                '{"event":"relay","state":"on","reason":"ok",'
                '"temp_c":%.2f,"limit_c":40.00,"uptime_ms":%d}'
                % (temp, uptime_ms())
            )

        # Нажатие клавиши раз в пять секунд.
        if sent % 25 == 0:
            key = "0123456789ABCD*#"[(sent // 25) % 16]
            send('{"event":"key","key":"%s","uptime_ms":%d}' % (key, uptime_ms()))

        time.sleep(period)
except KeyboardInterrupt:
    pass
finally:
    print("\nотправлено пакетов: %d" % sent)
PY
