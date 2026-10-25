#!/usr/bin/env python3
"""Заглушка центра для проверки сборщика на объекте.

Отвечает на те же два запроса, что настоящий центр: принимает батчи
телеметрии и подтверждения. Команды отдаются в ответе на выгрузку —
собственного канала к сборщику нет, он сам приходит за ними.

    python3 tools/fake-center.py                 # :8090, выход r1-strobe
    python3 tools/fake-center.py 8090 r2-heater  # другой выход

    curl localhost:8090/                         # кто на связи
    curl -XPOST localhost:8090/on               # выходу по умолчанию
    curl -XPOST localhost:8090/on/r1-strobe      # по имени
    curl -XPOST localhost:8090/off/r1-strobe
    curl -XPOST localhost:8090/all/off           # снять всё разом
    curl -XPOST localhost:8090/limits/r2-heater/60/50   # уставки защиты

Это инструмент разработки: ни токенов, ни дедупликации батчей тут нет.
"""

import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8090

# Выход, которому адресуются команды по умолчанию. Сборщик опознаёт
# адресата по полю device: видов команд под каждое реле в контракте нет,
# а имя должно дословно совпадать с id из конфигурации сборщика.
#
# Имена несут приставку комнаты (r1-, r2-, r3-): объект у сборщиков
# общий, и без неё реле разных комнат столкнулись бы в центре.
DEFAULT = sys.argv[2] if len(sys.argv) > 2 else "r1-strobe"

# Выходы, замеченные в телеметрии. Список не задаётся вручную: сборщик
# сам публикует каждый свой выход как отдельное устройство, и этого
# достаточно, чтобы знать, чем можно командовать.
outputs = {}

# Команды ждут здесь, пока сборщик не придёт за ними со своей выгрузкой.
queue = []
seq = 0


class Center(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0) or 0))

        if self.path == "/api/v1/telemetry":
            self.telemetry(body)
        elif self.path == "/api/v1/acks":
            self.acks(body)
        elif self.path in ("/all/off", "/all/on"):
            self.enqueue_all(self.path.rsplit("/", 1)[1])
        elif self.path in ("/on", "/off"):
            # Без имени — выходу по умолчанию: при отладке одного реле
            # набирать его имя каждый раз незачем.
            self.enqueue(self.path[1:], DEFAULT)
        elif self.path.startswith(("/on/", "/off/")):
            action, name = self.path[1:].split("/", 1)
            self.enqueue(action, name)
        elif self.path.startswith("/limits/"):
            self.set_limits(self.path[len("/limits/"):])
        else:
            self.reply(404, {"error": "неизвестный путь",
                             "hint": "/on, /off (выходу по умолчанию), "
                                     "/on/<выход>, /off/<выход>, /all/off, "
                                     "/limits/<выход>/<порог>/<возврат>"})

    def do_GET(self):
        self.reply(200, {
            "default": DEFAULT,
            "queued": [c["kind"] + " -> " + c["device"] for c in queue],
            # Что сборщик о себе рассказал: выходы с их состоянием.
            "outputs": outputs,
            "hint": "POST /on | /off | /on/<выход> | /off/<выход> | "
                    "/all/off | /limits/<выход>/<порог>/<возврат>",
        })

    # --- то, что вызывает сборщик ---

    def telemetry(self, body):
        batch = json.loads(body or b"{}")
        packets = batch.get("packets") or []

        for p in packets:
            d = p.get("data") or {}
            device = p.get("device_id")

            # Состояние выхода узнаётся из его же телеметрии: сборщик
            # публикует каждое реле как отдельное устройство.
            if d.get("state") in ("on", "off"):
                outputs[device] = {"state": d["state"],
                                   "reason": d.get("reason"),
                                   "source": p.get("source")}

            # Событие реле помечено отдельно: это смена состояния, а не
            # очередное измерение.
            mark = " [реле]" if d.get("event") == "relay" else ""
            print(f"  {device}: temp={d.get('temp_c')} "
                  f"state={d.get('state')} reason={d.get('reason')}{mark}")

        # Очередь отдаётся целиком и сразу опустошается: повторная
        # выдача той же команды привела бы к повторному щелчку реле.
        global queue
        commands, queue = queue, []
        if commands:
            print(f"  -> отдано команд: {[c['kind'] for c in commands]}")

        print(f"батч {batch.get('batch_id')} от {batch.get('site_id')}: "
              f"записей {len(packets)}")
        self.reply(200, {"accepted": len(packets), "duplicate": False,
                         "commands": commands})

    def acks(self, body):
        for a in json.loads(body or b"[]"):
            # accepted=false — это осознанный отказ, а не потеря
            # команды: причина в reason.
            verdict = "принята" if a.get("accepted") else "ОТКАЗ"
            print(f"подтверждение {a.get('id')}: {verdict} "
                  f"reason={a.get('reason')} state={a.get('state')}")
        self.reply(200, {"accepted": 0, "duplicate": False})

    # --- то, что вызывает оператор ---

    def enqueue_all(self, action):
        """Снять или поднять все известные выходы разом.

        Нужно прежде всего для «всё выключить»: на занятии это одно
        движение, а не восемь запросов по очереди.
        """
        if not outputs:
            self.reply(409, {"error": "выходы ещё не объявились в телеметрии"})
            return
        for device in sorted(outputs):
            self.enqueue(action, device, reply=False)
        self.reply(200, {"queued": len(queue)})

    def set_limits(self, tail):
        """Уставки тепловой защиты: /limits/<выход>/<порог>/<возврат>.

        Ключу без нагрева сборщик на это ответит отказом — и правильно:
        тепловой защиты у стробоскопа нет.
        """
        global seq
        parts = tail.split("/")
        if len(parts) != 3:
            self.reply(400, {"error": "ожидается /limits/<выход>/<порог>/<возврат>"})
            return
        name, limit, restore = parts
        seq += 1
        cmd = {
            "id": f"test-{seq:04d}",
            "device": name,
            "kind": "set_limits",
            "seq": seq,
            "limit_c": float(limit),
            "restore_c": float(restore),
            "operator": "fake-center",
        }
        queue.append(cmd)
        print(f"в очередь: set_limits -> {cmd['device']} "
              f"порог={limit} возврат={restore} id={cmd['id']}")
        self.reply(200, {"queued": cmd})

    def enqueue(self, action, device, reply=True):
        global seq
        seq += 1
        cmd = {
            "id": f"test-{seq:04d}",
            "device": device,
            "kind": "heater_on" if action == "on" else "heater_off",
            "seq": seq,
            "operator": "fake-center",
        }
        queue.append(cmd)
        print(f"в очередь: {cmd['kind']} -> {device} id={cmd['id']} "
              f"(уйдёт на следующей выгрузке)")
        if reply:
            self.reply(200, {"queued": cmd})

    def reply(self, code, payload):
        raw = json.dumps(payload, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, *_):
        pass  # свои сообщения понятнее стандартного лога


if __name__ == "__main__":
    # Построчный вывод: иначе при перенаправлении в файл сообщения
    # копятся в буфере и наблюдать за обменом вживую не получается.
    sys.stdout.reconfigure(line_buffering=True)

    print(f"заглушка центра слушает :{PORT}")
    ThreadingHTTPServer(("0.0.0.0", PORT), Center).serve_forever()
