// Порядок работы с комнатой — одним местом на всю панель.
//
// Дежурный не обязан помнить, что идёт за чем: экран сам называет
// текущий шаг и показывает **одно** главное действие. Раньше на
// странице висели четыре равноправные кнопки перехода состояний, и
// какую нажимать — приходилось выводить из инструкции.

export const STEPS = [
  { id: "waiting", title: "Ожидание", short: "Ожидание" },
  { id: "preparing", title: "Подготовка помещения", short: "Подготовка" },
  { id: "ready", title: "Комната готова", short: "Готова" },
  { id: "training", title: "Идёт тренировка", short: "Тренировка" },
  { id: "finishing", title: "Завершение", short: "Завершение" },
];

// byTeam сообщает, что помещение проходят командой. У теплокамеры
// занятие идёт по тренажёрам: там считают результат каждого, а не
// общий зачёт звена.
export function byTeam(room) {
  return room?.section !== "heat";
}

export function stepIndex(state) {
  const i = STEPS.findIndex((s) => s.id === state);
  return i < 0 ? 0 : i;
}

// nextAction — что делать прямо сейчас. Возвращает описание кнопки,
// а не саму кнопку: страница комнаты рисует её крупно, обзор — мелко.
// siblings — остальные помещения той же секции: у дымокамеры занятие
// одно на две комнаты, и шаг зависит от обеих.
export function nextAction(room, siblings) {
  switch (room?.state) {
    case "waiting":
      // Без состава подготовку центр не разрешит, и предлагать её
      // кнопкой было бы обманом.
      return room.roster
        ? {
            label: "Начать подготовку",
            hint: "Включится оборудование помещения, пойдёт отсчёт прогрева",
            kind: "prepare",
          }
        : {
            label: "Записать состав",
            hint: "ФИО и ИИН вносят до подготовки",
            kind: "roster",
          };

    case "preparing":
      return {
        label: "Подтвердить готовность",
        hint: room.preparation?.reached
          ? "Условия достигнуты, осмотрите зал и подтвердите"
          : "Подтверждать можно только после осмотра зала",
        kind: "ready",
      };

    case "ready": {
      // У секции занятие общее: пока соседнее помещение не готово,
      // начинать нечего, и сказать об этом надо до нажатия.
      const waiting = (siblings ?? []).filter((r) => r.state !== "ready");
      if (waiting.length > 0) {
        return {
          label: byTeam(room) ? "Начать тренировку" : "Начать занятие",
          hint: `Занятие общее на секцию. Не готово: ${waiting.map((r) => r.name).join(", ")}`,
          kind: "start",
          blocked: true,
        };
      }

      return {
        label: byTeam(room) ? "Начать тренировку" : "Начать занятие",
        hint: room.roster
          ? `Состав записан: ${room.roster.size} чел.${room.roster.team ? `, ${room.roster.team}` : ""}`
          : "Состав не записан",
        kind: "start",
      };
    }

    case "training":
      return {
        label: "Завершить тренировку",
        hint: "Таймеры и учёт остановятся, запись закроется",
        kind: "finish",
        danger: true,
      };

    case "finishing":
      return {
        label: "Вернуть в ожидание",
        hint: "Участники вышли, данные сохранены",
        kind: "waiting",
      };

    case "alarm":
      return {
        label: "Снять аварию",
        hint: "Оборудование само не включится: запуск после осмотра",
        kind: "waiting",
        danger: true,
      };

    default:
      return null;
  }
}

// summary — что показать о комнате одной строкой: то, чего ждёт
// дежурный, а не перечень полей.
export function summary(room) {
  if (!room) return "";

  switch (room.state) {
    case "preparing": {
      const p = room.preparation;
      if (!p) return "Идёт подготовка";
      if (p.reached) return "Условия достигнуты";
      if (p.expired) return "Время вышло, условия не достигнуты";
      return `Прогрев, осталось ${clock(p.seconds_left)}`;
    }

    case "ready":
      return byTeam(room) ? "Ждёт звено" : "Ждёт участников";

    case "training": {
      const s = room.session;
      if (!s) return "Идёт тренировка";
      const total = room.route?.total ?? (room.tasks ?? []).length;
      const done =
        room.route?.done ?? (room.tasks ?? []).filter((t) => t.done).length;
      if (total) return `${s.team || s.examinee}: маршрут ${done} из ${total}`;

      // В теплокамере имя занятия — перечень участников, названия
      // звена нет.
      const busy = (s.assignments ?? []).filter((a) => a.running).length;
      return busy
        ? `${s.examinee || s.team}: занято тренажёров ${busy}`
        : `${s.examinee || s.team} на занятии`;
    }

    case "finishing":
      return "Участники выходят";

    case "alarm":
      return room.state_reason || "Аварийная остановка";

    default:
      return room.roster
        ? `Состав записан: ${room.roster.size} чел. Можно готовить помещение`
        : "Свободна. Состав не записан";
  }
}

export function clock(seconds) {
  const s = Math.max(0, Math.round(seconds ?? 0));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, "0")}`;
}

// Этажи секции: сама комната и её соседи, по порядку этажей.
//
// Дымокамера — два этажа, но для занятия это одно помещение: звено
// проходит их подряд, а состав, таймер и зачёт у него общие. Поэтому
// панель показывает секцию одним окном, а этажи — блоками внутри.
export function floors(rooms, room) {
  if (!room) return [];
  const list = rooms.filter(
    (r) => r.id === room.id || (r.section && r.section === room.section),
  );
  // Открытая комната идёт первой при равных этажах: оператор открыл
  // именно её, и она должна остаться сверху.
  return list.sort(
    (a, b) => (a.floor ?? 0) - (b.floor ?? 0) || (a.id === room.id ? -1 : 1),
  );
}

// Название этажа для подписи блока. Пусто — секция из одной комнаты,
// и подписывать нечего.
export function floorName(room) {
  if (!room?.floor) return "";
  return `${room.floor} этаж`;
}

// Секция как одно помещение: её имя и число этажей.
export function sectionOf(room) {
  return {
    name: room?.section_name || room?.name || "",
    rooms: room?.section_rooms ?? 1,
    multi: (room?.section_rooms ?? 1) > 1,
  };
}
