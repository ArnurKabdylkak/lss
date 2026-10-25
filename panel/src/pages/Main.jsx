import { useEffect, useState } from "react";
import Button from "../components/ui/Button.jsx";
import Card from "../components/ui/Card.jsx";
import Status from "../components/ui/Status.jsx";
import { openRoom, openRoster } from "../shell/AppWindow.jsx";
import { clock, summary, sectionOf } from "../lib/flow.js";
import { fmt } from "../lib/format.js";
import { api } from "../lib/api.js";

// Главное окно комплекса.
//
// Отсюда начинается работа: три помещения, по окну на каждое. Ничего
// лишнего здесь нет намеренно — картотека, результаты и телеметрия
// принадлежат помещению и живут в его окне, рядом с тем, для чего
// они нужны.
//
// Дымокамера показывается одним помещением: два этажа — одно
// занятие, один состав и общий зачёт. Этажи видны строками внутри
// карточки, а не двумя отдельными окнами.

const TONE = {
  waiting: "idle",
  preparing: "warn",
  ready: "ok",
  training: "ok",
  finishing: "warn",
  alarm: "alarm",
};

export default function Main({ rooms, roomsLoaded, canOperate, onChanged }) {
  if (!roomsLoaded) {
    return (
      <p className="t-base text-[var(--color-ink-soft)]">
        Читаю раскладку объекта…
      </p>
    );
  }

  if (!rooms.length) {
    return (
      <Card title="Помещения не настроены">
        <p className="t-base text-[var(--color-ink-soft)]">
          Раскладка задаётся файлом <code>rooms.yaml</code> на стороне центра.
        </p>
      </Card>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-4">
      <div
        className="fill-grid fill-grid-stretch min-h-0 flex-1"
        style={{ "--fill-min": "20rem" }}
      >
        {sections(rooms).map((group) => (
          <RoomPanel key={group[0].id} room={group[0]} floors={group} />
        ))}
      </div>

      <Alarms />
    </div>
  );
}

// sections группирует комнаты по секциям: дымокамера — одна
// карточка с двумя этажами, теплокамера — одна комната.
function sections(rooms) {
  const out = [];
  const bySection = new Map();

  for (const room of rooms) {
    if (!room.section) {
      out.push([room]);
      continue;
    }
    const group = bySection.get(room.section);
    if (group) {
      group.push(room);
      continue;
    }
    const started = [room];
    bySection.set(room.section, started);
    out.push(started);
  }

  for (const group of out) {
    group.sort((a, b) => (a.floor ?? 0) - (b.floor ?? 0));
  }
  return out;
}

function RoomPanel({ room, floors }) {
  const tasks = room.tasks ?? [];
  const done = tasks.filter((t) => t.done).length;

  return (
    <section
      className="panel flex flex-col rounded-md border"
      style={{
        background: "var(--color-surface)",
        borderColor:
          room.state === "alarm"
            ? "var(--color-alarm)"
            : "var(--color-line-strong)",
        borderWidth: room.state === "alarm" ? 2 : 1,
      }}
    >
      <header
        className="flex items-center justify-between gap-2 border-b px-4 py-3"
        style={{ borderColor: "var(--color-line)" }}
      >
        <h2 className="t-lg font-semibold">{sectionOf(room).name}</h2>
        <Status tone={TONE[room.state] ?? "idle"} size="sm">
          {room.state_title}
        </Status>
      </header>

      <div className="flex-1 space-y-3 px-4 py-4">
        <p className="t-lg">{summary(room)}</p>

        {/* Этажи секции: состояние у них общее, а оборудование и
            камеры — свои, и это видно сразу. */}
        {floors.length > 1 && (
          <ul className="t-sm space-y-1 text-[var(--color-ink-soft)]">
            {floors.map((f) => (
              <li
                key={f.id}
                className="flex items-center justify-between gap-2"
              >
                <span>{f.floor ? `${f.floor} этаж` : f.name}</span>
                <span>
                  {(f.cameras ?? []).filter((c) => c.live).length} из{" "}
                  {(f.cameras ?? []).length} камер
                  {f.faults?.length > 0 &&
                    `, неисправностей: ${f.faults.length}`}
                </span>
              </li>
            ))}
          </ul>
        )}

        <dl className="fill-grid" style={{ "--fill-min": "8rem" }}>
          {room.has_temp && (
            <div>
              <dt className="t-sm text-[var(--color-ink-soft)]">температура</dt>
              <dd className="readout">{fmt.temp(room.temp_c)} °C</dd>
            </div>
          )}

          {room.preparation && !room.preparation.reached && (
            <div>
              <dt className="t-sm text-[var(--color-ink-soft)]">
                до конца прогрева
              </dt>
              <dd className="readout">
                {clock(room.preparation.seconds_left)}
              </dd>
            </div>
          )}

          {room.state === "training" &&
            (room.route?.total ?? tasks.length) > 0 && (
              <div>
                <dt className="t-sm text-[var(--color-ink-soft)]">маршрут</dt>
                <dd className="readout">
                  {room.route?.done ?? done} из{" "}
                  {room.route?.total ?? tasks.length}
                </dd>
              </div>
            )}

          {room.roster && room.state !== "training" && (
            <div>
              <dt className="t-sm text-[var(--color-ink-soft)]">
                состав записан
              </dt>
              <dd className="readout">{room.roster.size} чел.</dd>
            </div>
          )}
        </dl>

        {room.faults?.length > 0 && (
          <p className="t-sm" style={{ color: "var(--color-warn)" }}>
            неисправностей: {room.faults.length}
          </p>
        )}
      </div>

      <footer
        className="flex flex-wrap gap-2 border-t px-4 py-3"
        style={{ borderColor: "var(--color-line)" }}
      >
        <Button
          variant="primary"
          className="flex-1"
          onClick={() => openRoom(room.id)}
        >
          Открыть окно помещения
        </Button>

        {/* Состав заносят до подготовки, и начать можно прямо
            отсюда — своим окном. */}
        <Button onClick={() => openRoster(room.id)}>
          {room.roster ? "Состав" : "Записать состав"}
        </Button>
      </footer>
    </section>
  );
}

function Alarms() {
  const [list, setList] = useState([]);

  useEffect(() => {
    let dropped = false;
    api
      .alarms(10)
      .then((l) => !dropped && setList(l ?? []))
      .catch(() => {});
    return () => {
      dropped = true;
    };
  }, []);

  if (list.length === 0) return null;

  return (
    <Card title="Журнал аварийных остановок">
      <ul className="space-y-2">
        {list.map((e) => (
          <li key={e.id} className="t-base">
            <span className="text-[var(--color-ink-soft)]">
              {fmt.time(e.at)}
            </span>{" "}
            — {e.reason}
            <span className="t-sm text-[var(--color-ink-soft)]">
              {e.author && ` · ${e.author}`}
              {e.rooms?.length > 0 && ` · помещений: ${e.rooms.length}`}
              {e.active ? " · действует" : " · снята"}
            </span>
          </li>
        ))}
      </ul>
    </Card>
  );
}
