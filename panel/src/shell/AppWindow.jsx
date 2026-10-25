import { useCallback, useEffect, useState } from 'react'
import Login from '../components/Login.jsx'
import Button from '../components/ui/Button.jsx'
import Status from '../components/ui/Status.jsx'
import { api, token, Unauthorized } from '../lib/api.js'
import { subscribe } from '../lib/stream.js'

// Оконная оболочка.
//
// Это не сайт: у комплекса два вида окон — главное и окно комнаты.
// Поэтому нет ни адресной навигации по разделам, ни «страниц» в
// привычном вебе: сверху полоса окна с его названием и состоянием
// связи, остальное — рабочая площадь. Комнаты открываются своими
// окнами, картотека и результаты живут внутри окна комнаты, рядом с
// работой, а не отдельным сайтом.

export const SITE = 'obj-17'

export default function AppWindow({ title, subtitle, onClose, children }) {
  const [session, setSession] = useState(null)
  const [checking, setChecking] = useState(true)
  const [rooms, setRooms] = useState([])
  const [roomsLoaded, setRoomsLoaded] = useState(false)
  const [connection, setConnection] = useState('offline')
  const [error, setError] = useState('')

  useEffect(() => {
    if (!token()) {
      setChecking(false)
      return
    }
    api.me()
      .then(setSession)
      .catch(() => setSession(null))
      .finally(() => setChecking(false))
  }, [])

  const onUnauthorized = useCallback((e) => {
    if (e instanceof Unauthorized) {
      setSession(null)
      return true
    }
    return false
  }, [])

  const refreshRooms = useCallback(async () => {
    try {
      setRooms(await api.rooms())
    } catch (e) {
      onUnauthorized(e)
    } finally {
      setRoomsLoaded(true)
    }
  }, [onUnauthorized])

  useEffect(() => {
    if (!session) return
    refreshRooms()
    const timer = setInterval(refreshRooms, 5000)
    return () => clearInterval(timer)
  }, [session, refreshRooms])

  useEffect(() => {
    if (!session) return
    return subscribe(() => {}, setConnection)
  }, [session])

  if (checking) {
    return (
      <div className="flex min-h-screen items-center justify-center text-[var(--color-ink-soft)]">
        Проверяю сессию…
      </div>
    )
  }
  if (!session) return <Login onEnter={setSession} />

  const canOperate = session.role === 'operator'
  const alarms = rooms.filter((r) => r.state === 'alarm')
  const ctx = { session, canOperate, rooms, roomsLoaded, refreshRooms, setError }
  const caption = typeof title === 'function' ? title(ctx) : title

  return (
    <div className="flex h-screen flex-col overflow-hidden">
      {/* Полоса окна: название, состояние связи, аварийный стоп.
          Всегда на месте, как заголовок окна в программе. */}
      <header
        className="flex shrink-0 items-center justify-between gap-3 border-b px-4 py-2"
        style={{ background: 'var(--color-surface)', borderColor: 'var(--color-line-strong)' }}
      >
        <div className="flex items-center gap-3">
          <img src="/emblem.png" alt="" width="28" height="28" />
          <div>
            <h1 className="t-base font-semibold leading-tight">{caption}</h1>
            <p className="t-xs text-[var(--color-ink-soft)]">
              {subtitle || `объект ${SITE}`} · {session.name || session.login}
              {!canOperate && ' · только наблюдение'}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <Status tone={connection === 'online' ? 'ok' : 'warn'} size="sm">
            {connection === 'online' ? 'связь есть' : 'связь прервана'}
          </Status>

          {canOperate && (
            <Button
              size="sm" variant="danger"
              onClick={() => {
                // Подтверждение намеренно: кнопка останавливает весь
                // комплекс, и случайное нажатие дорого стоит.
                if (!window.confirm('Объявить аварийную остановку всего комплекса?')) return
                api.alarm('нажата кнопка в панели').then(refreshRooms).catch((e) => setError(e.message))
              }}
            >
              Аварийный стоп
            </Button>
          )}

          {onClose ? (
            <Button size="sm" onClick={onClose}>Закрыть окно</Button>
          ) : (
            <Button size="sm" onClick={() => api.logout().finally(() => setSession(null))}>
              Выйти
            </Button>
          )}
        </div>
      </header>

      {(alarms.length > 0 || error) && (
        <div
          aria-live="assertive"
          className="shrink-0 border-b px-4 py-2"
          style={{ background: 'var(--color-alarm-bg)', borderColor: 'var(--color-alarm)' }}
        >
          <ul className="flex flex-wrap gap-x-6 gap-y-1">
            {error && (
              <li className="t-base font-semibold" style={{ color: 'var(--color-alarm)' }}>
                {error}
              </li>
            )}
            {alarms.map((r) => (
              <li key={r.id} className="t-base font-semibold" style={{ color: 'var(--color-alarm)' }}>
                {r.name}: {r.state_reason || 'аварийная остановка'}
              </li>
            ))}
          </ul>
        </div>
      )}

      {children(ctx)}
    </div>
  )
}

// openRoster открывает окно состава помещения.
//
// Состав заносят до подготовки и у входа в зал: отдельное окно можно
// держать рядом, пока на экране помещения следят за обстановкой.
export function openRoster(id) {
  const win = window.open(
    `roster.html?room=${encodeURIComponent(id)}`,
    `mchs-roster-${id}`,
    'popup=yes,width=1080,height=760,menubar=no,toolbar=no,location=no,status=no',
  )
  win?.focus()
  return win
}

// openRoom открывает окно комнаты.
//
// Именно окно, а не вкладка: у каждой комнаты свой дежурный, окна
// раскладывают по мониторам, и закрытие одного не трогает остальные.
// Имя окна постоянное — повторное нажатие поднимает уже открытое.
export function openRoom(id) {
  const win = window.open(
    `room.html?room=${encodeURIComponent(id)}`,
    `mchs-room-${id}`,
    'popup=yes,width=1440,height=900,menubar=no,toolbar=no,location=no,status=no',
  )
  win?.focus()
  return win
}
