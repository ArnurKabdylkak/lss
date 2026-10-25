import { useCallback, useEffect, useState } from "react";
import Cameras from "./Cameras.jsx";
import Training, { LastResult } from "./Training.jsx";
import Card from "./ui/Card.jsx";
import Status from "./ui/Status.jsx";
import { Bar } from "./ui/Readout.jsx";
import { fmt, heaterReason } from "./../lib/format.js";
import { api } from "../lib/api.js";

// Части экрана помещения.
//
// Понятия «устройство» здесь нет намеренно. Для дежурного существуют
// тренажёры, задания маршрута и условия в зале; платы, реле и
// идентификаторы — забота наладки, а не того, кто ведёт тренировку.

// Conditions — условия в зале: температура и работа обогрева.
//
// Тепловая пушка не показывается как устройство: её включает
// подготовка, уставки приходят из раскладки, и дежурному важно
// только, какая температура и идёт ли нагрев.
export function Conditions({ room }) {
  const heater = room.heater;
  const last = heater?.last ?? {};
  const limit = room.heater_limits?.limit_c;
  const temp = room.has_temp ? room.temp_c : null;

  if (temp == null && !heater) return null;

  const state = !heater
    ? null
    : heater.stale || !heater.seen
      ? { tone: "warn", text: "нет связи с обогревом" }
      : last.relay_state === "on"
        ? { tone: "ok", text: "идёт нагрев" }
        : last.relay_state === "off"
          ? {
              tone: "idle",
              text: `нагрев снят · ${heaterReason[last.relay_reason] ?? "по уставке"}`,
            }
          : { tone: "idle", text: "нагрев не работает" };

  return (
    <Card
      title="Условия в зале"
      extra={
        state && (
          <Status tone={state.tone} size="sm">
            {state.text}
          </Status>
        )
      }
    >
      <div className="flex flex-wrap items-end gap-6">
        {temp != null && (
          <div>
            <div className="t-sm text-[var(--color-ink-soft)]">температура</div>
            <div className="readout">{fmt.temp(temp)} °C</div>
          </div>
        )}

        {limit != null && (
          <div className="min-w-[200px] flex-1">
            <Bar
              value={temp ?? 0}
              min={0}
              max={100}
              limit={limit}
              label={`температура в зале, защита снимет нагрев при ${limit} °C`}
            />
            <p className="mt-1 t-sm text-[var(--color-ink-soft)]">
              защита снимет нагрев при {limit} °C
              {room.heater_limits?.resume_c != null &&
                `, вернёт при ${room.heater_limits.resume_c} °C`}
            </p>
          </div>
        )}
      </div>
    </Card>
  );
}

export function CamerasCard({ room, floor = "" }) {
  const cameras = room.cameras ?? [];
  if (cameras.length === 0) return null;

  const live = cameras.filter((c) => c.live).length;
  return (
    <Card
      title={floor ? `Камеры — ${floor}` : "Камеры помещения"}
      extra={
        <span className="t-sm text-[var(--color-ink-soft)]">
          в эфире: {live} из {cameras.length}
        </span>
      }
    >
      <Cameras cameras={cameras} />
    </Card>
  );
}

// TrainingCard — занятие и итог предыдущего.
export function TrainingCard({ room, canOperate, onChanged }) {
  const [last, setLast] = useState(null);

  const refreshLast = useCallback(async () => {
    try {
      const list = await api.sessions(room.id);
      setLast(list[0] ?? null);
    } catch {
      // Журнал занятий не критичен: помещение остаётся рабочим.
    }
  }, [room.id]);

  useEffect(() => {
    refreshLast();
  }, [refreshLast, room.session?.id]);

  return (
    <Card title="Тренировка">
      <Training
        room={room}
        canOperate={canOperate}
        onChanged={() => {
          onChanged?.();
          refreshLast();
        }}
      />

      {!room.session && last && (
        <div
          className="mt-3 border-t pt-3"
          style={{ borderColor: "var(--color-line)" }}
        >
          <p className="mb-1 t-sm text-[var(--color-ink-soft)]">
            Прошлое занятие
          </p>
          <LastResult
            session={last}
            taskNames={Object.fromEntries(
              (room.tasks ?? []).map((t) => [t.id, t.name]),
            )}
          />
        </div>
      )}
    </Card>
  );
}
