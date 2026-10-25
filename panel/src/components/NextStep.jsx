import { useState } from 'react'
import Button from './ui/Button.jsx'
import { nextAction } from '../lib/flow.js'
import { api } from '../lib/api.js'
import { openRoom, openRoster } from '../shell/AppWindow.jsx'

// Главное действие комнаты: одно, крупное, с подписью последствия.
//
// Раньше рядом стояли четыре кнопки перехода состояний, и дежурный
// выбирал по памяти. Теперь экран сам называет следующий шаг, а
// остальные переходы живут в «прочих действиях» — ими пользуются
// редко и осознанно.

export default function NextStep({ room, siblings, canOperate, onChanged, size = 'lg' }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const action = nextAction(room, siblings)

  if (!action || !canOperate) return null

  const run = async () => {
    setBusy(true)
    setError('')
    try {
      switch (action.kind) {
        case 'prepare':
          await api.prepare(room.id)
          break
        case 'ready':
          await api.setState(room.id, 'ready', 'осмотрено администратором')
          break
        case 'roster':
          // Состав заносят в своём окне: его держат у входа в зал,
          // пока на экране помещения следят за обстановкой.
          openRoster(room.id)
          return
        case 'start':
          // Занятие открывается по записанному составу: людей уже
          // внесли до подготовки.
          await api.startSession(room.id, {})
          break
        case 'finish':
          await api.finishSession(room.id, {})
          break
        case 'waiting':
          await api.setState(room.id, 'waiting', '')
          break
        default:
          break
      }
      onChanged?.()
    } catch (e) {
      // Дословно: центр объясняет, почему шаг сейчас невозможен.
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-3">
        <Button
          variant={action.danger ? 'danger' : 'primary'}
          size={size === 'lg' ? 'md' : 'sm'}
          className={size === 'lg' ? 'h-14 px-6 t-lg' : ''}
          disabled={busy || action.blocked}
          onClick={run}
        >
          {busy ? 'Выполняю…' : action.label}
        </Button>
        {size === 'lg' && (
          <p
            className="max-w-[460px] t-sm"
            style={{ color: action.blocked ? 'var(--color-warn)' : 'var(--color-ink-soft)' }}
          >
            {action.hint}
          </p>
        )}
      </div>

      {/* Отсюда же открываем то помещение, которое задерживает: иначе
          дежурный ищет его окно сам. */}
      {action.blocked && size === 'lg' && (
        <div className="mt-2 flex flex-wrap gap-2">
          {(siblings ?? []).filter((r) => r.state !== 'ready').map((r) => (
            <Button key={r.id} size="sm" onClick={() => openRoom(r.id)}>
              Открыть «{r.name}»
            </Button>
          ))}
        </div>
      )}

      {error && (
        <p role="alert" className="mt-2 t-sm" style={{ color: 'var(--color-alarm)' }}>
          {error}
        </p>
      )}
    </div>
  )
}

// OtherActions — переходы, которые нужны редко: возврат назад,
// досрочное завершение. Спрятаны, но не убраны.
export function OtherActions({ room, canOperate, onChanged }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  if (!canOperate) return null

  const go = async (state, reason) => {
    setBusy(true)
    setError('')
    try {
      await api.setState(room.id, state, reason)
      onChanged?.()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mt-3 border-t pt-3" style={{ borderColor: 'var(--color-line)' }}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="t-sm underline"
        style={{ color: 'var(--color-ink-soft)' }}
      >
        {open ? 'Скрыть прочие действия' : 'Прочие действия'}
      </button>

      {open && (
        <div className="mt-2 flex flex-wrap gap-2">
          <Button
            size="sm" disabled={busy || room.state !== 'training'}
            onClick={() => go('finishing', '')}
          >
            Перевести в завершение
          </Button>
          <Button
            size="sm"
            disabled={busy || !['preparing', 'ready', 'finishing', 'alarm'].includes(room.state)}
            onClick={() => go('waiting', 'возврат администратором')}
          >
            Вернуть в ожидание
          </Button>
        </div>
      )}

      {error && (
        <p role="alert" className="mt-2 t-sm" style={{ color: 'var(--color-alarm)' }}>
          {error}
        </p>
      )}
    </div>
  )
}
