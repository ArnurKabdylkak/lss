import { useCallback, useEffect, useState } from 'react'
import Button from '../components/ui/Button.jsx'
import Card from '../components/ui/Card.jsx'
import Field from '../components/ui/Field.jsx'
import Status from '../components/ui/Status.jsx'
import { LastResult } from '../components/Training.jsx'
import { fmt } from '../lib/format.js'
import { api } from '../lib/api.js'

// Картотека спасателей (сценарий §1).
//
// ИИН — ключ карточки: он заносится в произвольной форме и центром не
// проверяется. Карточка не удаляется, а уходит в архив: по ней уже
// есть зачёты, и терять их нельзя.

export default function Cards({ canOperate, rooms }) {
  const [list, setList] = useState([])
  const [loaded, setLoaded] = useState(false)
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState('')
  const [error, setError] = useState('')
  const [withArchived, setWithArchived] = useState(false)

  const refresh = useCallback(async (q) => {
    try {
      setList((await api.rescuers(q)) ?? [])
      setError('')
    } catch (e) {
      setError(e.message)
    } finally {
      setLoaded(true)
    }
  }, [])

  useEffect(() => {
    refresh(query)
  }, [refresh, query])

  const shown = withArchived ? list : list.filter((c) => !c.archived)

  // Названия заданий — из раскладки: в записи занятия лежат только
  // идентификаторы, а в карточке читают человеческие названия.
  const taskNames = {}
  for (const r of rooms ?? []) {
    for (const t of r.tasks ?? []) taskNames[t.id] = t.name
  }

  return (
    <div className="space-y-5">
      {canOperate && <NewCard onAdded={() => refresh(query)} />}

      <Card
        title="Картотека"
        extra={
          <label className="flex items-center gap-2 t-sm text-[var(--color-ink-soft)]">
            {/* Цель 24×24: в перчатках по сенсорному экрану
                в системный чекбокс не попадают. */}
            <input
              type="checkbox" checked={withArchived}
              onChange={(e) => setWithArchived(e.target.checked)}
              className="h-6 w-6"
              style={{ accentColor: 'var(--color-action)' }}
            />
            показывать архив
          </label>
        }
      >
        <div className="mb-3 max-w-[380px]">
          <Field
            label="Поиск по ФИО, ИИН или подразделению"
            value={query} onChange={setQuery}
            autoComplete="off" placeholder="Иванов"
          />
        </div>

        {error && (
          <p role="alert" className="mb-3 t-sm" style={{ color: 'var(--color-alarm)' }}>
            {error}
          </p>
        )}

        {shown.length === 0 ? (
          <p className="t-base text-[var(--color-ink-soft)]">
            {!loaded
              ? 'Читаю картотеку…'
              : query
                ? 'Ничего не найдено.'
                : 'Картотека пуста: заведите первую карточку.'}
          </p>
        ) : (
          <ul className="space-y-2">
            {shown.map((c) => (
              <li
                key={c.iin}
                className="rounded-md border px-3 py-2"
                style={{
                  borderColor: 'var(--color-line)',
                  background: c.archived ? 'var(--color-surface-muted)' : 'var(--color-surface)',
                }}
              >
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <div className="t-base font-medium">
                      {c.name}
                      {c.archived && (
                        <span className="ml-2">
                          <Status tone="idle" size="sm">в архиве</Status>
                        </span>
                      )}
                    </div>
                    <div className="t-sm text-[var(--color-ink-soft)]">
                      ИИН {c.iin}
                      {c.unit && ` · ${c.unit}`}
                      {c.created && ` · заведена ${fmt.time(c.created)}`}
                    </div>
                  </div>

                  <div className="flex gap-2">
                    <Button
                      size="sm"
                      onClick={() => setOpen((cur) => (cur === c.iin ? '' : c.iin))}
                    >
                      {open === c.iin ? 'Свернуть' : 'Тренировки'}
                    </Button>
                    {canOperate && (
                      <Button
                        size="sm"
                        onClick={() =>
                          api.archiveRescuer(c.iin, !c.archived)
                            .then(() => refresh(query))
                            .catch((e) => setError(e.message))
                        }
                      >
                        {c.archived ? 'Вернуть из архива' : 'В архив'}
                      </Button>
                    )}
                  </div>
                </div>

                {open === c.iin && <Trainings iin={c.iin} taskNames={taskNames} />}
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  )
}

// Trainings — тренировки участника: результат, отметки заданий и
// видеозаписи лежат в той же записи занятия (сценарий §6).
function Trainings({ iin, taskNames }) {
  const [card, setCard] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let dropped = false
    api.rescuer(iin)
      .then((c) => !dropped && setCard(c))
      .catch((e) => !dropped && setError(e.message))
    return () => {
      dropped = true
    }
  }, [iin])

  if (error) {
    return (
      <p role="alert" className="mt-3 t-sm" style={{ color: 'var(--color-alarm)' }}>
        {error}
      </p>
    )
  }
  if (!card) {
    return <p className="mt-3 t-sm text-[var(--color-ink-soft)]">Загружаю…</p>
  }

  const list = card.trainings ?? []
  if (list.length === 0) {
    return (
      <p className="mt-3 t-sm text-[var(--color-ink-soft)]">
        Тренировок в карточке пока нет.
      </p>
    )
  }

  return (
    <div className="mt-3 space-y-3 border-t pt-3" style={{ borderColor: 'var(--color-line)' }}>
      {list.map((s) => (
        <div key={s.id}>
          <div className="t-sm text-[var(--color-ink-soft)]">
            {fmt.time(s.started)} · {s.room}
          </div>
          <LastResult session={s} taskNames={taskNames} />
        </div>
      ))}
    </div>
  )
}

function NewCard({ onAdded }) {
  const [iin, setIin] = useState('')
  const [name, setName] = useState('')
  const [unit, setUnit] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const add = async (e) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.addRescuer({ iin: iin.trim(), name: name.trim(), unit: unit.trim() })
      setIin('')
      setName('')
      setUnit('')
      onAdded?.()
    } catch (err) {
      // Дословно: центр скажет, что карточка с таким ИИН уже есть или
      // что в ФИО недопустимый символ.
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title="Новая карточка">
      <form onSubmit={add} className="flex flex-wrap items-end gap-3">
        <div className="min-w-[200px] flex-1">
          <Field
            label="ИИН" value={iin} onChange={setIin}
            hint="Заносится в произвольной форме" autoComplete="off"
            placeholder="900315501234"
          />
        </div>
        <div className="min-w-[260px] flex-1">
          <Field
            label="ФИО" value={name} onChange={setName}
            autoComplete="off" placeholder="Иванов Пётр Сергеевич"
          />
        </div>
        <div className="min-w-[160px]">
          <Field
            label="Подразделение" value={unit} onChange={setUnit}
            autoComplete="off" placeholder="СЧ-2"
          />
        </div>
        <Button type="submit" variant="primary" disabled={busy || !iin.trim() || !name.trim()}>
          {busy ? 'Сохраняю…' : 'Завести карточку'}
        </Button>
      </form>

      {error && (
        <p role="alert" className="mt-3 t-sm" style={{ color: 'var(--color-alarm)' }}>
          {error}
        </p>
      )}
    </Card>
  )
}
