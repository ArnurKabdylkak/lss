// Единое место для правил показа величин: одни и те же обороты не должны
// выглядеть по-разному в индикаторе и в таблице.

export const fmt = {
  speed: (v) => (v ?? 0).toFixed(1),
  distance: (m) => (m >= 1000 ? `${(m / 1000).toFixed(2)} км` : `${Math.round(m ?? 0)} м`),
  rpm: (v) => (v ?? 0).toFixed(0),
  temp: (v) => (v ?? 0).toFixed(2),
  press: (v) => ((v ?? 0) / 100).toFixed(1), // в гектопаскалях: так читают барометр
  uptime: (ms) => {
    const s = Math.floor((ms ?? 0) / 1000)
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    return h > 0 ? `${h} ч ${m} мин` : `${m} мин ${s % 60} с`
  },
  time: (iso) => (iso ? new Date(iso).toLocaleTimeString('ru-RU') : '—'),
  dateTime: (iso) => (iso ? new Date(iso).toLocaleString('ru-RU') : '—'),
}

// Причины состояния пушки на языке инструктора, а не протокола.
export const heaterReason = {
  ok: 'норма',
  overtemp: 'перегрев',
  cooldown: 'выдержка после отключения',
  sensor_fail: 'нет достоверной температуры',
  operator_off: 'выключена оператором',
  unknown: 'неизвестно',
}

// Отказы на команду — оператор должен понимать, почему не вышло.
export const ackReason = {
  overtemp: 'отклонено: перегрев',
  sensor_fail: 'отклонено: нет достоверной температуры',
  cooldown: 'отклонено: идёт выдержка после отключения',
  operator_off: 'отклонено: нагрев запрещён оператором',
  bad_limits: 'отклонено: негодные уставки',
  out_of_range: 'отклонено: значение вне допустимых границ',
  too_soon: 'отклонено: препятствие ещё падает, взведите позже',
  no_such_obstacle: 'отклонено: такого препятствия нет',
  stale_seq: 'отклонено: устаревшая команда',
  duplicate: 'повтор уже исполненной',
  heater_disabled: 'отклонено: пушка не настроена на сборщике',
  unknown: 'отклонено: неизвестная команда',
  ok: 'исполнено',
}

// Что делает команда — человеческим языком для журнала.
export const codeResult = {
  valid: 'код верный',
  invalid: 'код неверный',
  locked: 'ввод придержан после неудачных попыток',
  cleared: 'набор стёрт',
}

export const commandKind = {
  heater_on: 'включить нагрев',
  heater_off: 'выключить нагрев',
  set_limits: 'уставки защиты',
  set_period: 'период отчётов',
  drop: 'сбросить препятствие',
  arm: 'взвести препятствие',
  set_code: 'сменить код на стойке',
}

// Пропала ли связь с устройством. Порог берём с запасом относительно
// периода отчётов: одиночная потеря датаграммы — не авария.
export function isStale(received, seconds = 10) {
  if (!received) return true
  return (Date.now() - new Date(received).getTime()) / 1000 > seconds
}
