import { useEffect, useState } from "react";
import Button from "./ui/Button.jsx";
import Card from "./ui/Card.jsx";
import Field from "./ui/Field.jsx";
import Status from "./ui/Status.jsx";
import { fmt } from "../lib/format.js";
import { api } from "../lib/api.js";

// Упражнения теплокамеры.
//
// Велоэргометр и бесконечная лестница зачитываются **по времени**, и
// срок у них общий: звено работает отведённые минуты, а кто в какой
// момент на каком тренажёре — дело перестановок внутри зала. Поэтому
// таймер один на оба, а не два раздельных.
//
// У ударного молота зачёт по подъёмам — единственный тренажёр, где
// считают повторения.
//
// Смену тренажёров центр делает сам: выполнив задание, звено
// переходит по кругу. Оператор должен видеть, что это произошло, —
// иначе смена выглядит как сбой назначений.

const TIMED = ["bike", "stairs"];

export default function Exercises({ room, canOperate, onChanged }) {
  const list = room.exercises ?? [];
  const [cards, setCards] = useState([]);

  useEffect(() => {
    let dropped = false;
    api
      .rescuers()
      .then((l) => !dropped && setCards(l ?? []))
      .catch(() => {});
    return () => {
      dropped = true;
    };
  }, []);

  if (list.length === 0) return null;

  const name = (iin) => cards.find((c) => c.iin === iin)?.name ?? iin;
  const timed = list.filter((e) => TIMED.includes(e.kind));
  const reps = list.filter((e) => !TIMED.includes(e.kind));

  const rotations = room.rotations ?? 0;

  return (
    <Card
      title="Упражнения"
      extra={
        rotations > 0 && (
          <Status tone="ok" size="sm">
            смен тренажёров: {rotations}
          </Status>
        )
      }
    >
      <div className="fill-grid" style={{ "--fill-min": "20rem" }}>
        {timed.length > 0 && (
          <Timed
            room={room}
            list={timed}
            name={name}
            canOperate={canOperate}
            onChanged={onChanged}
          />
        )}

        {reps.map((ex) => (
          <Reps
            key={ex.device}
            room={room}
            ex={ex}
            who={ex.iin ? name(ex.iin) : ""}
            canOperate={canOperate}
            onChanged={onChanged}
          />
        ))}
      </div>
    </Card>
  );
}

// Timed — велоэргометр и лестница под общим сроком.
function Timed({ room, list, name, canOperate, onChanged }) {
  const [minutes, setMinutes] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [, tick] = useState(0);

  const running = list.filter((e) => e.busy);

  useEffect(() => {
    if (running.length === 0) return;
    const timer = setInterval(() => tick((t) => t + 1), 1000);
    return () => clearInterval(timer);
  }, [running.length]);

  // Общий срок — тот, что задан тренажёрам: он у них одинаковый.
  const target = list.find((e) => e.target?.seconds > 0)?.target?.seconds ?? 0;
  // Отсчёт идёт от того, кто начал раньше: срок общий, и дробить его
  // по тренажёрам значило бы считать два разных упражнения.
  const elapsed = running.length
    ? Math.max(...running.map((e) => e.seconds ?? 0))
    : 0;
  const left = target > 0 ? Math.max(0, target - elapsed) : 0;
  const done = target > 0 && elapsed >= target;

  const set = async () => {
    setBusy(true);
    setError("");
    try {
      // Срок уходит обоим тренажёрам: центр считает задание по
      // каждому, а зачёт у звена общий.
      const seconds = Math.round(Number(minutes) * 60);
      for (const ex of list) {
        await api.setTarget(room.id, ex.device, { seconds });
      }
      setMinutes("");
      onChanged?.();
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section
      className="panel fill-wide rounded-md border"
      style={{
        borderColor: done ? "var(--color-ok)" : "var(--color-line)",
        background: done ? "var(--color-ok-bg)" : "var(--color-surface-muted)",
      }}
    >
      <div>
        <header className="panel-head mb-2">
          <div>
            <h3 className="t-lg font-semibold">Велоэргометр и лестница</h3>
            <p className="t-sm text-[var(--color-ink-soft)]">
              Зачёт по времени, срок общий на оба тренажёра
            </p>
          </div>
          <Status
            tone={done ? "ok" : running.length ? "warn" : "idle"}
            size="sm"
          >
            {done
              ? "срок выработан"
              : running.length
                ? "идёт работа"
                : "ожидает"}
          </Status>
        </header>

        <div className="flex flex-wrap items-baseline gap-6">
          <div>
            <div className="t-sm text-[var(--color-ink-soft)]">прошло</div>
            <div className="readout">{clock(elapsed)}</div>
          </div>

          {target > 0 && (
            <div>
              <div className="t-sm text-[var(--color-ink-soft)]">осталось</div>
              <div className="readout">{done ? "—" : clock(left)}</div>
            </div>
          )}

          {target > 0 && (
            <div>
              <div className="t-sm text-[var(--color-ink-soft)]">срок</div>
              <div className="readout readout-sm">{clock(target)}</div>
            </div>
          )}
        </div>

        <ul className="mt-3 space-y-1">
          {list.map((ex) => (
            <li
              key={ex.device}
              className="t-base flex flex-wrap justify-between gap-2"
            >
              <span>{ex.name}</span>
              <span className="text-[var(--color-ink-soft)]">
                {ex.iin ? name(ex.iin) : "свободен"}
                {ex.busy && ` · ${clock(ex.seconds ?? 0)}`}
              </span>
            </li>
          ))}
        </ul>

        {canOperate && room.session && (
          <div
            className="mt-3 flex flex-wrap items-end gap-2 border-t pt-3"
            style={{ borderColor: "var(--color-line)" }}
          >
            <div className="w-[150px]">
              <Field
                label="общий срок, мин"
                value={minutes}
                onChange={setMinutes}
                inputMode="decimal"
                autoComplete="off"
                placeholder="5"
              />
            </div>
            <Button disabled={busy || !Number(minutes)} onClick={set}>
              {busy ? "…" : "Задать обоим"}
            </Button>
          </div>
        )}

        {error && (
          <p
            role="alert"
            className="t-sm mt-2"
            style={{ color: "var(--color-alarm)" }}
          >
            {error}
          </p>
        )}
      </div>
    </section>
  );
}

// Reps — ударный молот: единственный тренажёр с подъёмами.
function Reps({ room, ex, who, canOperate, onChanged }) {
  const [reps, setReps] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [, tick] = useState(0);

  useEffect(() => {
    if (!ex.busy) return;
    const timer = setInterval(() => tick((t) => t + 1), 1000);
    return () => clearInterval(timer);
  }, [ex.busy]);

  const set = async () => {
    setBusy(true);
    setError("");
    try {
      await api.setTarget(room.id, ex.device, { reps: Number(reps) });
      setReps("");
      onChanged?.();
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section
      className="panel rounded-md border"
      style={{
        borderColor: ex.done ? "var(--color-ok)" : "var(--color-line)",
        background: ex.done
          ? "var(--color-ok-bg)"
          : "var(--color-surface-muted)",
      }}
    >
      <div>
        <header className="panel-head mb-2">
          <div>
            <h3 className="t-lg font-semibold">{ex.name}</h3>
            <p className="t-sm text-[var(--color-ink-soft)]">
              {who || "свободен"}
            </p>
          </div>
          <Status tone={ex.done ? "ok" : ex.busy ? "warn" : "idle"} size="sm">
            {ex.done
              ? "задание выполнено"
              : ex.busy
                ? "идёт работа"
                : "ожидает"}
          </Status>
        </header>

        <div className="flex flex-wrap items-baseline gap-6">
          <div>
            <div className="t-sm text-[var(--color-ink-soft)]">
              подъёмы {ex.target?.reps > 0 ? "· счёт платы" : ""}
            </div>
            <div
              className="readout"
              style={{
                color:
                  ex.target?.reps > 0 && (ex.reps ?? 0) >= ex.target.reps
                    ? "var(--color-ok)"
                    : undefined,
              }}
            >
              {ex.reps ?? 0}
              {ex.target?.reps > 0 && (
                <span className="t-lg text-[var(--color-ink-soft)]">
                  {" "}
                  из {ex.target.reps}
                </span>
              )}
            </div>
          </div>

          {ex.busy && (
            <div>
              <div className="t-sm text-[var(--color-ink-soft)]">
                на тренажёре
              </div>
              <div className="readout readout-sm">{clock(ex.seconds ?? 0)}</div>
            </div>
          )}

          {ex.max_load_kg > 0 && (
            <div>
              <div className="t-sm text-[var(--color-ink-soft)]">вес</div>
              <div className="readout readout-sm">
                {fmt.speed(ex.max_load_kg)} кг
              </div>
            </div>
          )}
        </div>

        {canOperate && room.session && (
          <div
            className="mt-3 flex flex-wrap items-end gap-2 border-t pt-3"
            style={{ borderColor: "var(--color-line)" }}
          >
            <div className="w-[130px]">
              <Field
                label="подъёмов"
                value={reps}
                onChange={setReps}
                inputMode="numeric"
                autoComplete="off"
                placeholder="20"
              />
            </div>
            <Button disabled={busy || !Number(reps)} onClick={set}>
              {busy ? "…" : "Задать"}
            </Button>
          </div>
        )}

        {error && (
          <p
            role="alert"
            className="t-sm mt-2"
            style={{ color: "var(--color-alarm)" }}
          >
            {error}
          </p>
        )}
      </div>
    </section>
  );
}

function clock(seconds) {
  const s = Math.max(0, Math.round(seconds ?? 0));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, "0")}`;
}
