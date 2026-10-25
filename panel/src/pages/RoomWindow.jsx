import { useEffect, useState } from "react";
import Card from "../components/ui/Card.jsx";
import Results from "./Results.jsx";
import RoomPage from "./RoomPage.jsx";

// Окно помещения: всё о нём в одном месте.
//
// Вкладки принадлежат комнате, а не комплексу: состав набирают для
// этой комнаты, результаты смотрят по её занятиям, телеметрию — по её
// устройствам. Раньше это были отдельные разделы сайта, и дежурный
// ходил между ними, держа в голове, о какой комнате речь.

const TABS = [
  { id: "work", title: "Работа" },
  { id: "results", title: "Результаты" },
];

export default function RoomWindow({
  roomId,
  rooms,
  roomsLoaded,
  canOperate,
  onChanged,
}) {
  const room = rooms.find((r) => r.id === roomId);
  const [tab, setTab] = useState("work");

  useEffect(() => {
    if (room)
      document.title = `${room.section_name || room.name} — тренажёрный комплекс`;
  }, [room?.name]);

  // Окно состава сообщает о записи сразу: ждать очередного опроса
  // незачем, дежурный только что нажал кнопку.
  useEffect(() => {
    const onMessage = (e) => {
      if (e.data?.type === "mchs-roster-saved" && e.data.room === roomId)
        onChanged?.();
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [roomId, onChanged]);

  if (!room) {
    return (
      <main className="min-h-0 flex-1 overflow-auto p-4">
        <Card
          title={roomsLoaded ? "Помещение не найдено" : "Загружаю помещение"}
        >
          <p className="t-base text-[var(--color-ink-soft)]">
            {roomsLoaded
              ? `Помещения ${roomId} нет в раскладке объекта.`
              : "Читаю раскладку объекта."}
          </p>
        </Card>
      </main>
    );
  }

  return (
    <>
      {/* Вкладки окна: вид как у рабочей программы, а не у сайта. */}
      <nav
        className="flex shrink-0 gap-1 border-b px-3 pt-2"
        style={{
          background: "var(--color-surface-muted)",
          borderColor: "var(--color-line-strong)",
        }}
      >
        {TABS.map((t) => {
          const active = tab === t.id;
          return (
            <button
              key={t.id}
              type="button"
              onClick={() => setTab(t.id)}
              aria-current={active ? "page" : undefined}
              className="-mb-px rounded-t border border-b-0 px-4 py-2 t-base font-medium"
              style={{
                background: active ? "var(--color-surface)" : "transparent",
                color: active ? "var(--color-ink)" : "var(--color-ink-soft)",
                borderColor: active
                  ? "var(--color-line-strong)"
                  : "transparent",
              }}
            >
              {t.title}
            </button>
          );
        })}
      </nav>

      <main className="min-h-0 flex-1 overflow-auto p-4">
        {tab === "work" && (
          <RoomPage
            roomId={roomId}
            rooms={rooms}
            loaded={roomsLoaded}
            canOperate={canOperate}
            onChanged={onChanged}
          />
        )}

        {tab === "results" && <Results rooms={[room]} room={room} />}
      </main>
    </>
  );
}
