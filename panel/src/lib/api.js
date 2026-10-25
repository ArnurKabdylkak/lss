// Обращения к центру.
//
// Оператор входит по логину и паролю; токен сессии живёт в браузере
// и уходит заголовком. Имя для журнала действий берёт центр из сессии,
// а не из того, что напишет клиент.

const TOKEN_KEY = 'mchs.token'

export function token() {
  try {
    return localStorage.getItem(TOKEN_KEY) || ''
  } catch {
    return ''
  }
}

export function setToken(value) {
  try {
    if (value) localStorage.setItem(TOKEN_KEY, value)
    else localStorage.removeItem(TOKEN_KEY)
  } catch {
    // приватный режим браузера: сессия проживёт до перезагрузки
  }
}

// Unauthorized — центр не признал сессию; панель показывает вход.
export class Unauthorized extends Error {}

function headers() {
  const h = { 'Content-Type': 'application/json' }
  const t = token()
  if (t) h['Authorization'] = `Bearer ${t}`
  return h
}

async function request(path, options = {}) {
  const res = await fetch(path, { ...options, headers: headers() })

  if (res.status === 401) {
    setToken('')
    throw new Unauthorized('нужно войти')
  }

  if (!res.ok) {
    // Тело ошибки важнее кода: центр объясняет, почему команда негодна,
    // и это надо показать оператору дословно.
    let message = `ошибка ${res.status}`
    try {
      const body = await res.json()
      if (body.error) message = body.error
    } catch {
      // тело не JSON — оставляем код
    }
    throw new Error(message)
  }

  if (res.status === 204) return null
  return res.json()
}

export const api = {
  login: async (login, password) => {
    const res = await request('/api/v1/login', {
      method: 'POST',
      body: JSON.stringify({ login, password }),
    })
    setToken(res.token)
    return res
  },

  logout: async () => {
    try {
      await request('/api/v1/logout', { method: 'POST' })
    } finally {
      setToken('')
    }
  },

  me: () => request('/api/v1/me'),

  devices: () => request('/api/v1/devices'),
  device: (id) => request(`/api/v1/devices/${encodeURIComponent(id)}`),

  history: (id, { from, to, events } = {}) => {
    const q = new URLSearchParams()
    if (from) q.set('from', from.toISOString())
    if (to) q.set('to', to.toISOString())
    if (events) q.set('events', '1')
    return request(`/api/v1/devices/${encodeURIComponent(id)}/history?${q}`)
  },

  cameras: () => request('/api/v1/cameras'),

  rooms: () => request('/api/v1/rooms'),

  room: (id) => request(`/api/v1/rooms/${encodeURIComponent(id)}`),

  // Состав на предстоящую тренировку: ФИО и ИИН вносят до
  // подготовки, без него помещение не готовят.
  setRoster: (id, { team, rescuers } = {}) =>
    request(`/api/v1/rooms/${encodeURIComponent(id)}/roster`, {
      method: 'POST',
      body: JSON.stringify({ team, rescuers }),
    }),

  // Узел оборудования зала: дым, прожекторы, стробоскоп, звук,
  // свет, вытяжка, пушка. Помимо автоматики подготовки.
  setUnit: (id, unit, on) =>
    request(`/api/v1/rooms/${encodeURIComponent(id)}/equipment`, {
      method: 'POST',
      body: JSON.stringify({ unit, on }),
    }),

  // Подготовка помещения и перевод состояния (сценарий §3).
  prepare: (id) =>
    request(`/api/v1/rooms/${encodeURIComponent(id)}/prepare`, { method: 'POST' }),

  setState: (id, state, reason) =>
    request(`/api/v1/rooms/${encodeURIComponent(id)}/state`, {
      method: 'POST',
      body: JSON.stringify({ state, reason }),
    }),

  // Аварийная остановка всего комплекса.
  alarm: (reason) =>
    request('/api/v1/alarm', { method: 'POST', body: JSON.stringify({ reason }) }),

  // Назначение спасателя на тренажёр; пустой iin снимает его.
  assign: (room, device, iin) =>
    request(`/api/v1/rooms/${encodeURIComponent(room)}/session/assign`, {
      method: 'POST',
      body: JSON.stringify({ device, iin }),
    }),

  // Картотека спасателей (сценарий §1). ИИН — ключ карточки, он
  // заносится в произвольной форме и не проверяется.
  rescuers: (q) => request(`/api/v1/rescuers${q ? `?q=${encodeURIComponent(q)}` : ''}`),

  rescuer: (iin) => request(`/api/v1/rescuers/${encodeURIComponent(iin)}`),

  addRescuer: (card) =>
    request('/api/v1/rescuers', { method: 'POST', body: JSON.stringify(card) }),

  editRescuer: (iin, card) =>
    request(`/api/v1/rescuers/${encodeURIComponent(iin)}`, {
      method: 'PUT',
      body: JSON.stringify(card),
    }),

  // Карточка не удаляется, а уходит в архив: по ней уже есть зачёты.
  archiveRescuer: (iin, archived = true) =>
    request(`/api/v1/rescuers/${encodeURIComponent(iin)}/archive`, {
      method: 'POST',
      body: JSON.stringify({ archived }),
    }),

  // Занятие: открывается до начала на команду, закрывается с
  // результатом. Состав — ИИН карточек; ФИО остаётся для занятия
  // без картотеки.
  startSession: (room, { team, examinee, rescuers, assignments } = {}) =>
    request(`/api/v1/rooms/${encodeURIComponent(room)}/session`, {
      method: 'POST',
      body: JSON.stringify({ team, examinee, rescuers, assignments }),
    }),

  finishSession: (room, { reason, comment } = {}) =>
    request(`/api/v1/rooms/${encodeURIComponent(room)}/session/finish`, {
      method: 'POST',
      body: JSON.stringify({ reason, comment }),
    }),

  comment: (session, comment) =>
    request(`/api/v1/sessions/${encodeURIComponent(session)}/comment`, {
      method: 'POST',
      body: JSON.stringify({ comment }),
    }),

  // Задание тренажёру: срок для велоэргометра и лестницы, подъёмы
  // для ударного молота (сценарий теплокамеры §5).
  setTarget: (room, device, { seconds, reps } = {}) =>
    request(`/api/v1/rooms/${encodeURIComponent(room)}/session/target`, {
      method: 'POST',
      body: JSON.stringify({ device, seconds, reps }),
    }),

  // Отметка задания маршрута. Задания с устройством отмечаются сами,
  // остальные — администратором.
  markTask: (room, task, done = true) =>
    request(`/api/v1/rooms/${encodeURIComponent(room)}/session/task`, {
      method: 'POST',
      body: JSON.stringify({ task, done }),
    }),

  // Завалы включают по ходу маршрута и только во время тренировки.
  rubble: (room) =>
    request(`/api/v1/rooms/${encodeURIComponent(room)}/rubble`, { method: 'POST' }),

  // Журнал аварийных остановок.
  alarms: (limit = 50) => request(`/api/v1/alarms?limit=${limit}`),

  sessions: (room) => request(`/api/v1/rooms/${encodeURIComponent(room)}/sessions`),

  // Журнал результатов объекта целиком: отдельная страница смотрит
  // все занятия, а не по одной комнате.
  allSessions: (limit = 200) => request(`/api/v1/sessions?limit=${limit}`),

  commands: (site) => request(`/api/v1/commands${site ? `?site=${site}` : ''}`),

  send: (command) =>
    request('/api/v1/commands', { method: 'POST', body: JSON.stringify(command) }),
}

// Адрес трансляции камеры.
//
// Токен сюда не добавляется: имена сегментов в плейлисте
// относительные, строка запроса до них не доходит, и они получали бы
// 401. Запросы плеера авторизует cookie сессии, которую центр ставит
// при входе.
export function streamURL(playlist) {
  return playlist || ''
}
