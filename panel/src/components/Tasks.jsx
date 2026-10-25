import { useState } from "react";
import Button from "./ui/Button.jsx";
import Card from "./ui/Card.jsx";
import Status from "./ui/Status.jsx";
import { fmt } from "../lib/format.js";
import { api } from "../lib/api.js";

// Маршрут комнаты: что команда уже прошла.
//
// Задание проходит команда целиком, поэтому отметка одна. Часть
// заданий отмечается сама — по сигналу устройства, остальные ставит
// администратор, наблюдая за выполнением (сценарий дымокамеры §4–5).
// Полное прохождение маршрута завершает тренировку само.

export default function Tasks({ room, floor = "", canOperate, onChanged }) {
  const tasks = room.tasks ?? [];
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  if (tasks.length === 0) return null;

  const training = room.state === "training";
  const done = tasks.filter((t) => t.done).length;

  const act = async (key, fn) => {
    setBusy(key);
    setError("");
    try {
      await fn();
      onChanged?.();
    } catch (e) {
      // Дословно: центр объясняет, почему отметка или завалы не
      // приняты — например, тренировка ещё не началась.
      setError(e.message);
    } finally {
      setBusy("");
    }
  };

  return (
    <Card
      title={floor ? `Маршрут — ${floor}` : "Маршрут"}
      extra={
        <div className="flex items-center gap-3">
          <Status tone={done === tasks.length ? "ok" : "idle"} size="sm">
            здесь пройдено {done} из {tasks.length}
          </Status>

          {/* У дымокамеры маршрут идёт через две комнаты: сколько
              пройдено всего, знать полезно, а чужие задания списком
              здесь не нужны. */}
          {room.route && room.route.total > tasks.length && (
            <span className="t-sm text-[var(--color-ink-soft)]">
              по маршруту: {room.route.done} из {room.route.total}
            </span>
          )}

          {/* Завалы включают по ходу маршрута и только во время
              тренировки: вне её центр запрос отклонит. */}
          {canOperate && hasRubble(room) && (
            <Button
              size="sm"
              disabled={!training || busy === "rubble"}
              onClick={() => act("rubble", () => api.rubble(room.id))}
            >
              {busy === "rubble" ? "Включаю…" : "Включить завалы"}
            </Button>
          )}
        </div>
      }
    >
      <ul className="space-y-2">
        {tasks.map((t) => (
          <li
            key={t.id}
            className="flex flex-wrap items-center justify-between gap-3 rounded-md border px-3 py-2"
            style={{
              borderColor: t.done ? "var(--color-ok)" : "var(--color-line)",
              background: t.done
                ? "var(--color-ok-bg)"
                : "var(--color-surface-muted)",
            }}
          >
            <div>
              <div className="t-base font-medium">{t.name}</div>
              {t.note && (
                <div className="t-sm text-[var(--color-ink-soft)]">
                  {t.note}
                </div>
              )}
              <div className="t-sm text-[var(--color-ink-faint)]">
                {t.done
                  ? `отмечено в ${fmt.time(t.at)} · ${
                      t.auto
                        ? "по сигналу тренажёра"
                        : `отметил ${t.by || "администратор"}`
                    }`
                  : t.auto
                    ? "отметится по сигналу тренажёра"
                    : "отмечает администратор"}
              </div>
            </div>

            {canOperate && !t.auto && (
              <Button
                size="sm"
                variant={t.done ? "secondary" : "primary"}
                disabled={!training || busy === t.id}
                onClick={() =>
                  act(t.id, () => api.markTask(room.id, t.id, !t.done))
                }
              >
                {busy === t.id ? "…" : t.done ? "Снять отметку" : "Отметить"}
              </Button>
            )}
          </li>
        ))}
      </ul>

      {!training && (
        <p className="mt-3 t-sm text-[var(--color-ink-soft)]">
          Отметки принимаются во время тренировки: сигнал, пришедший раньше,
          зачёлся бы предыдущей команде.
        </p>
      )}

      {error && (
        <p
          role="alert"
          className="mt-3 t-sm"
          style={{ color: "var(--color-alarm)" }}
        >
          {error}
        </p>
      )}
    </Card>
  );
}

// hasRubble — есть ли в комнате механизм завалов. Кнопку показываем
// только там, где он стоит.
function hasRubble(room) {
  return (room.devices ?? []).some((d) => d.kind === "rubble");
}
