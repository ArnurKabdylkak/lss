import { useCallback, useEffect, useState } from "react";
import Equipment from "../components/Equipment.jsx";
import Exercises from "../components/Exercises.jsx";
import NextStep, { OtherActions } from "../components/NextStep.jsx";
import Steps from "../components/Steps.jsx";
import { CamerasCard, Conditions, TrainingCard } from "../components/Room.jsx";
import Tasks from "../components/Tasks.jsx";
import Button from "../components/ui/Button.jsx";
import Card from "../components/ui/Card.jsx";
import Status from "../components/ui/Status.jsx";
import { api } from "../lib/api.js";
import {
  clock,
  summary,
  floors as floorsOf,
  floorName,
  sectionOf,
} from "../lib/flow.js";
import { fmt } from "../lib/format.js";

// Страница помещения: подготовка, состояние, занятие, тренажёры и
// камеры. Помещения разведены по окнам намеренно — дежурный ведёт
// одно и не должен отличать его поля от соседних.
//
// Дымокамера — одно помещение из двух этажей: занятие, состав и
// зачёт у них общие, поэтому управление здесь одно, а оборудование и
// камеры показаны по этажам. Разделение остаётся там, где оно
// настоящее: у каждого этажа свой сборщик, свои реле и свои камеры.

const TONE = {
  waiting: "idle",
  preparing: "warn",
  ready: "ok",
  training: "ok",
  finishing: "warn",
  alarm: "alarm",
};

export default function RoomPage({
  roomId,
  rooms,
  loaded = true,
  canOperate,
  onChanged,
}) {
  const room = rooms.find((r) => r.id === roomId);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const act = useCallback(
    async (fn) => {
      setBusy(true);
      setError("");
      try {
        await fn();
        onChanged?.();
      } catch (e) {
        // Причину показываем дословно: центр объясняет, почему
        // переход не разрешён.
        setError(e.message);
      } finally {
        setBusy(false);
      }
    },
    [onChanged],
  );

  if (!room && !loaded) {
    return (
      <Card title="Загружаю комнату">
        <p className="t-base text-[var(--color-ink-soft)]">
          Читаю раскладку объекта.
        </p>
      </Card>
    );
  }

  if (!room) {
    return (
      <Card title="Комната не найдена">
        <p className="t-base text-[var(--color-ink-soft)]">
          Комнаты {roomId} нет в раскладке объекта.
        </p>
        <div className="mt-3">
          <a href="index.html">
            <Button>К обзору</Button>
          </a>
        </div>
      </Card>
    );
  }

  const prep = room.preparation;

  // Соседи по секции: у дымокамеры занятие одно на два этажа.
  const siblings = rooms.filter(
    (r) => r.id !== room.id && r.section && r.section === room.section,
  );

  // Этажи помещения по порядку. Один этаж — обычная комната.
  const floors = floorsOf(rooms, room);
  const section = sectionOf(room);

  return (
    // Раскладка по ширине: на широком экране карточки идут в два-три
    // столбца, а не одной лентой вниз. Дежурный должен видеть
    // состояние, занятие и маршрут одновременно.
    <div className="fill-grid" style={{ "--fill-min": "26rem" }}>
      {/* Шапка комнаты: где мы в порядке работы, что происходит и
          что делать дальше. Это первое, на что смотрит дежурный. */}
      <Card className="fill-wide">
        <div className="panel-head mb-3">
          <div>
            <h2 className="t-xl font-semibold">{section.name}</h2>
            <p className="t-lg">{summary(room)}</p>
            {room.state_reason && (
              <p className="t-sm text-[var(--color-ink-soft)]">
                основание: {room.state_reason}, с {fmt.time(room.state_since)}
              </p>
            )}
          </div>

          <div className="flex items-center gap-4">
            {room.has_temp && (
              <div className="text-right">
                <div className="t-sm text-[var(--color-ink-soft)]">
                  температура
                </div>
                <div className="readout">{fmt.temp(room.temp_c)} °C</div>
              </div>
            )}
            {prep && !prep.reached && (
              <div className="text-right">
                <div className="t-sm text-[var(--color-ink-soft)]">
                  до конца прогрева
                </div>
                <div className="readout">{clock(prep.seconds_left)}</div>
              </div>
            )}
            <Status tone={TONE[room.state] ?? "idle"}>
              {room.state_title}
            </Status>
          </div>
        </div>

        <div className="mb-4">
          <Steps state={room.state} />
        </div>

        <NextStep
          room={room}
          siblings={siblings}
          canOperate={canOperate}
          onChanged={onChanged}
        />
        <OtherActions
          room={room}
          canOperate={canOperate}
          onChanged={onChanged}
        />

        {prep && (
          <p className="mt-3 t-sm text-[var(--color-ink-soft)]">
            {prep.message}
            {prep.target_c > 0 && ` · цель ${prep.target_c} °C`}
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

      <TrainingCard room={room} canOperate={canOperate} onChanged={onChanged} />

      {/* Код задания «Набор чисел» виден администратору с начала
          подготовки: он сообщает его участникам до старта. */}
      {room.code && (
        <Card title="Код для стойки набора">
          <div className="readout tracking-[0.2em]">{room.code}</div>
          <p className="mt-2 t-sm text-[var(--color-ink-soft)]">
            Формируется заново на каждую тренировку и сообщается участникам до
            старта. Неверный ввод задание не завершает, но учитывается в
            карточке.
          </p>
        </Card>
      )}

      {/* Назначения на тренажёры: показания закрепляются за карточкой
          того, кто назначен (сценарий теплокамеры §4). */}
      {room.session && (
        <Assignments
          room={room}
          canOperate={canOperate}
          onChanged={onChanged}
        />
      )}

      {/* Маршрут с отметками — там, где он задан раскладкой.
          У дымокамеры он сквозной, но задания стоят на своих этажах,
          и отмечать их проще там же, где они происходят. */}
      {floors.map((f) =>
        f.tasks?.length > 0 ? (
          <Tasks
            key={f.id}
            room={f}
            floor={section.multi ? floorName(f) : ""}
            canOperate={canOperate}
            onChanged={onChanged}
          />
        ) : null,
      )}

      {/* Упражнения с таймерами — там, где стоят тренажёры. */}
      {room.exercises?.length > 0 && (
        <div className="fill-wide">
          <Exercises
            room={room}
            canOperate={canOperate}
            onChanged={onChanged}
          />
        </div>
      )}

      <Conditions room={room} />

      {/* Оборудование зала: автоматика плюс ручное управление.
          У дымокамеры — по блоку на этаж: реле у этажей свои, и
          команда должна уходить именно на свой сборщик. */}
      {floors.map((f) => (
        <div key={f.id} className="fill-wide">
          <Equipment
            room={f}
            floor={section.multi ? floorName(f) : ""}
            canOperate={canOperate}
            onChanged={onChanged}
          />
        </div>
      ))}

      {floors.map((f) =>
        f.cameras?.length > 0 ? (
          <div key={f.id} className="fill-wide">
            <CamerasCard room={f} floor={section.multi ? floorName(f) : ""} />
          </div>
        ) : null,
      )}
    </div>
  );
}

function Assignments({ room, canOperate, onChanged }) {
  const trainers = (room.devices ?? []).filter((d) =>
    ["bike", "stairs", "load"].includes(d.kind),
  );
  const [cards, setCards] = useState([]);
  const [error, setError] = useState("");

  // Имена участников берём из картотеки: в занятии лежат только ИИН.
  useEffect(() => {
    let dropped = false;
    api
      .rescuers()
      .then((list) => !dropped && setCards(list))
      .catch(() => {});
    return () => {
      dropped = true;
    };
  }, []);

  // Где тренажёров нет, назначать нечего: маршрут проходит команда
  // целиком, и он показан отдельной карточкой.
  if (trainers.length === 0) return null;

  const assigned = new Map(
    (room.session.assignments ?? [])
      .filter((a) => a.running)
      .map((a) => [a.device, a.iin]),
  );
  const name = (iin) => cards.find((c) => c.iin === iin)?.name ?? iin;

  const change = async (device, iin) => {
    setError("");
    try {
      await api.assign(room.id, device, iin);
      onChanged?.();
    } catch (e) {
      setError(e.message);
    }
  };

  return (
    <Card title="Кто на каком тренажёре">
      <div className="space-y-3">
        {trainers.map((d) => {
          const current = assigned.get(d.id) ?? "";
          return (
            <div
              key={d.id}
              className="flex flex-wrap items-center justify-between gap-3"
            >
              <div>
                <div className="t-base font-medium">{d.name}</div>
                <div className="t-sm text-[var(--color-ink-soft)]">
                  {current ? name(current) : "свободен"}
                </div>
              </div>

              {canOperate && (
                <select
                  value={current}
                  onChange={(e) => change(d.id, e.target.value)}
                  className="h-11 rounded border px-3 t-base"
                  style={{
                    background: "var(--color-surface)",
                    borderColor: "var(--color-line-strong)",
                  }}
                >
                  <option value="">— свободен —</option>
                  {(room.session.rescuers ?? []).map((iin) => (
                    <option key={iin} value={iin}>
                      {name(iin)}
                    </option>
                  ))}
                </select>
              )}
            </div>
          );
        })}
      </div>

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
