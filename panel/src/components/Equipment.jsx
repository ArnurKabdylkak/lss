import { useState } from "react";
import Button from "./ui/Button.jsx";
import Card from "./ui/Card.jsx";
import Status from "./ui/Status.jsx";
import { api } from "../lib/api.js";

// Оборудование зала.
//
// Подготовка включает всё положенное помещению разом — это основной
// путь, и трогать здесь ничего не нужно. Но по ходу занятия дым
// приходится добавлять, прожекторы гасить, а вытяжку пускать раньше
// срока, поэтому к автоматике добавлено ручное управление.
//
// Узлы названы по смыслу: какое реле за что отвечает, знает сборщик.

export default function Equipment({ room, floor = "", canOperate, onChanged }) {
  const units = room.equipment ?? [];
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  if (units.length === 0) return null;

  const alarm = room.state === "alarm";
  const on = units.filter((u) => u.on).length;
  const waiting = units.filter((u) => u.wait).length;

  const toggle = async (unit, next) => {
    setBusy(unit);
    setError("");
    try {
      await api.setUnit(room.id, unit, next);
      onChanged?.();
    } catch (e) {
      // Дословно: центр объясняет, почему узел сейчас не переключить.
      setError(e.message);
    } finally {
      setBusy("");
    }
  };

  return (
    <Card
      title={floor ? `Оборудование зала — ${floor}` : "Оборудование зала"}
      extra={
        <Status tone={waiting > 0 ? "warn" : on > 0 ? "ok" : "idle"} size="sm">
          включено {on} из {units.length}
          {waiting > 0 && `, ждут ответа: ${waiting}`}
        </Status>
      }
    >
      <div className="fill-grid" style={{ "--fill-min": "13rem" }}>
        {units.map((u) => (
          <div
            key={u.unit}
            className="flex items-center justify-between gap-3 rounded border px-3 py-2"
            style={{
              borderColor: u.wait
                ? "var(--color-warn)"
                : u.on
                  ? "var(--color-ok)"
                  : "var(--color-line)",
              background: u.on
                ? "var(--color-ok-bg)"
                : "var(--color-surface-muted)",
            }}
          >
            <div>
              <div className="t-base font-medium">{u.name}</div>
              {/*
                Состояние показывается по ответу сборщика, а не по
                нажатию: пока реле не щёлкнуло, узел не работает, что
                бы ни нажал оператор.
              */}
              <div className="t-sm text-[var(--color-ink-soft)]">
                {u.wait
                  ? u.want
                    ? "включается…"
                    : "выключается…"
                  : u.on
                    ? "работает"
                    : "выключено"}
              </div>
            </div>

            {canOperate && (
              <Button
                size="sm"
                variant={u.on ? "secondary" : "primary"}
                disabled={alarm || busy === u.unit || u.wait}
                onClick={() => toggle(u.unit, !u.on)}
              >
                {busy === u.unit || u.wait
                  ? "…"
                  : u.on
                    ? "Выключить"
                    : "Включить"}
              </Button>
            )}
          </div>
        ))}
      </div>

      <p className="t-sm mt-3 text-[var(--color-ink-soft)]">
        {alarm
          ? "В аварии оборудованием управляет защита: из панели его не переключить."
          : "Старт подготовки включает всё разом; здесь — поштучно, по ходу занятия."}
      </p>

      {error && (
        <p
          role="alert"
          className="t-sm mt-2"
          style={{ color: "var(--color-alarm)" }}
        >
          {error}
        </p>
      )}
    </Card>
  );
}
