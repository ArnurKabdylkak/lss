#!/usr/bin/env python3
"""Имитатор объекта: телеметрия теплокамеры (велосипед, лестница,
силовая установка, пушка) и дымокамеры (датчики комнат, стойка набора
кода, имитаторы очагов, завалы, дверь) — батчами на вход сборщика.

Нужен, пока лестница и силовая установка существуют только на бумаге:
их контракт согласовывается, прошивки нет, а панель и тракт центра
проверять надо уже сейчас. Батчи, а не UDP: идентификаторы устройств
проставляются только там, по адресу источника их не различить.

    python3 scripts/simulate_room.py <адрес центра> <логин> <пароль> <секунд> <объект>

Запускается и из контейнера — см. профиль sim в compose.yaml, так он
работает на хосте без Python, в том числе на Windows.
"""

import datetime
import json
import math
import random
import sys
import time
import urllib.error
import urllib.request

center, login, password, seconds, site = sys.argv[1:6]
seconds = float(seconds)

BIKE = site + "-bike-1"
STAIRS = site + "-stairs-1"
LOAD = site + "-load-1"
HEATER = site + "-heater-1"

# Дымокамера: две комнаты со своей обвязкой. Устройства те же, что в
# rooms.yaml, — иначе комната не узнает собственные сигналы.
CODE = site + "-code-1"
FIRE1 = site + "-fire-1"
FIRE2 = site + "-fire-2"
MAZE = site + "-maze-1"
RUBBLE = site + "-rubble-1"
DOOR = site + "-door-1"
SMOKE_SENSORS = (site + "-smoke-temp-2", site + "-smoke-temp-3")

PERIOD = 0.5            # как часто уходит батч
LIMIT_C = 60.0          # уставки той же комнаты, что в rooms.yaml
RESUME_C = 50.0


def post(path, payload, token=None):
    body = json.dumps(payload).encode()
    req = urllib.request.Request(center + path, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=5) as res:
        raw = res.read()
        return json.loads(raw) if raw else None


def now():
    # RFC 3339: смещение со двоеточием, иначе центр не разберёт время.
    return datetime.datetime.now().astimezone().isoformat()


token = None
if password:
    try:
        token = post("/api/v1/login", {"login": login, "password": password})["token"]
    except urllib.error.HTTPError as e:
        # Вход может быть не настроен — тогда API открыт, и это нормально.
        if e.code != 501:
            raise

start = time.monotonic()
batch = 0
sent = 0

# Состояние тренажёров: оно накапливается, как на настоящих платах.
distance = 0.0
floor, floors, steps, going_up = 1, 0, 0, True
reps, sets, load_kg = 0, 0, 40.0
temp, heating = 22.0, True

# Дымокамера. Температура держится около 25 °C: там греть нечего, но
# панель должна видеть живой датчик, а не «нет данных».
smoke_temp = 24.0

# Задания маршрута имитируются по кругу: набор кода с ошибками, потом
# верный ввод, затем очаги, завалы и дверь. Так панель показывает и
# попытки, и автоматические отметки, не дожидаясь настоящих плат.
ROUTE_PERIOD = 90.0     # круг маршрута, секунд
route_marks = set()


def record(device, data):
    return {
        "device_id": device,
        "received": now(),
        "source": "127.0.0.1:0",
        "data": data,
    }


def uptime():
    return int((time.monotonic() - start) * 1000)


try:
    while True:
        elapsed = time.monotonic() - start
        if seconds and elapsed >= seconds:
            break

        packets = []

        # Велосипед: скорость гуляет вокруг 24 км/ч.
        speed = 24.0 + 4.0 * math.sin(elapsed / 5.0)
        distance += speed / 3.6 * PERIOD
        packets.append(record(BIKE, {
            "speed_kmh": round(speed, 1),
            "rpm": round(speed * 8.1, 1),
            "distance_m": round(distance, 1),
            "temp_c": round(temp, 2),
            "press_pa": 101325,
            "uptime_ms": uptime(),
        }))

        # Лестница: ступень примерно раз в полсекунды, этаж — каждые 16.
        steps += random.choice([1, 2])
        packets.append(record(STAIRS, {
            "floor": floor,
            "floors": floors,
            "step_count": steps,
            "uptime_ms": uptime(),
        }))
        if steps % 16 < 2:
            floor = floor + 1 if going_up else max(1, floor - 1)
            floors += 1
            if floor >= 9:
                going_up = False
            elif floor <= 1:
                going_up = True
            packets.append(record(STAIRS, {
                "event": "floor",
                "floor": floor,
                "floors": floors,
                "floor_dir": "up" if going_up else "down",
                "step_count": steps,
                "uptime_ms": uptime(),
            }))

        # Силовая установка: подход из 12 повторов, потом пауза.
        reps += 1
        if reps > 12:
            sets += 1
            reps = 0
            load_kg = min(60.0, load_kg + 2.5)   # между подходами добавляют вес
            packets.append(record(LOAD, {
                "event": "set",
                "load_kg": load_kg,
                "reps": 12,
                "sets": sets,
                "uptime_ms": uptime(),
            }))
        packets.append(record(LOAD, {
            "load_kg": load_kg,
            "reps": reps,
            "sets": sets,
            "uptime_ms": uptime(),
        }))

        # Дымокамера: датчики воздуха обеих комнат.
        smoke_temp += random.uniform(-0.2, 0.2)
        smoke_temp = min(32.0, max(20.0, smoke_temp))
        for sensor in SMOKE_SENSORS:
            packets.append(record(sensor, {
                "temp_c": round(smoke_temp, 2),
                "press_pa": 101325,
                "uptime_ms": uptime(),
            }))

        # Маршрут дымокамеры: события идут по кругу, чтобы на панели
        # было видно и попытки кода, и отметки по сигналу.
        phase = elapsed % ROUTE_PERIOD
        if phase < PERIOD:
            route_marks.clear()

        def once(key):
            if key in route_marks:
                return False
            route_marks.add(key)
            return True

        if 10.0 <= phase < 10.0 + PERIOD and once("code-bad"):
            # Неверный ввод задание не завершает, но учитывается.
            packets.append(record(CODE, {
                "event": "code", "code_result": "invalid",
                "code": "%04d" % random.randrange(10000),
                "uptime_ms": uptime(),
            }))
        if 20.0 <= phase < 20.0 + PERIOD and once("code-ok"):
            packets.append(record(CODE, {
                "event": "code", "code_result": "valid",
                "uptime_ms": uptime(),
            }))
        if 30.0 <= phase < 30.0 + PERIOD and once("maze"):
            packets.append(record(MAZE, {
                "event": "obstacle", "obstacle_state": "dropped",
                "uptime_ms": uptime(),
            }))
        for at, device, key in ((40.0, FIRE1, "fire1"), (55.0, FIRE2, "fire2")):
            if at <= phase < at + PERIOD and once(key):
                # Очаг отключён кнопкой на месте: по этому сигналу
                # центр и засчитывает задание.
                packets.append(record(device, {
                    "event": "relay", "state": "off", "reason": "button",
                    "uptime_ms": uptime(),
                }))
        if 65.0 <= phase < 65.0 + PERIOD and once("rubble"):
            packets.append(record(RUBBLE, {
                "event": "obstacle", "obstacle_state": "dropped",
                "uptime_ms": uptime(),
            }))
        if 75.0 <= phase < 75.0 + PERIOD and once("door"):
            packets.append(record(DOOR, {
                "event": "obstacle", "obstacle_state": "dropped",
                "uptime_ms": uptime(),
            }))

        # Тепловая пушка: греет до порога, защита снимает нагрев.
        temp += (1.2 if heating else -0.8) * PERIOD
        packets.append(record(HEATER, {
            "temp_c": round(temp, 2),
            "press_pa": 101325,
            "uptime_ms": uptime(),
        }))
        if heating and temp >= LIMIT_C:
            heating = False
            packets.append(record(HEATER, {
                "event": "relay", "state": "off", "reason": "overtemp",
                "temp_c": round(temp, 2), "limit_c": LIMIT_C, "uptime_ms": uptime(),
            }))
        elif not heating and temp <= RESUME_C:
            heating = True
            packets.append(record(HEATER, {
                "event": "relay", "state": "on", "reason": "ok",
                "temp_c": round(temp, 2), "limit_c": LIMIT_C, "uptime_ms": uptime(),
            }))

        batch += 1
        try:
            post("/api/v1/telemetry", {
                "site_id": site,
                "batch_id": "sim-%d-%d" % (int(start), batch),
                "packets": packets,
            }, token)
            sent += len(packets)
        except urllib.error.URLError as e:
            # Центр перезапускают на ходу: имитатор должен это
            # пережить, а не падать вместе с ним.
            print("центр недоступен (%s), повтор через 3 с" % e, flush=True)
            time.sleep(3)
            continue

        time.sleep(PERIOD)
except KeyboardInterrupt:
    pass
finally:
    print("\nотправлено записей: %d" % sent)
