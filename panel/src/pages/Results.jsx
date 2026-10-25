import { useCallback, useEffect, useMemo, useState } from 'react'
import Button from '../components/ui/Button.jsx'
import Card from '../components/ui/Card.jsx'
import Field from '../components/ui/Field.jsx'
import Status from '../components/ui/Status.jsx'
import { fmt } from '../lib/format.js'
import { api } from '../lib/api.js'

// Результаты тренировок — отдельная страница.
//
// Журнал занятий объекта целиком: слева список, справа разбор
// выбранного. На каждое занятие центр складывает папку отчёта — PDF и
// видеозапись рядом, — поэтому здесь не только цифры, но и ссылки на
// то, что подшивают к делу (сценарий §6).

const TONE = {
  success: 'ok',
  partial: 'warn',
  aborted: 'warn',
  alarm: 'alarm',
}

export default function Results({ rooms, room: only }) {
  const [list, setList] = useState([])
  const [loaded, setLoaded] = useState(false)
  const [openId, setOpenId] = useState('')
  const [query, setQuery] = useState('')
  const [room, setRoom] = useState('')
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      // В окне помещения журнал его собственный: занятия соседних
      // комнат здесь только мешали бы.
      setList((only ? await api.sessions(only.id) : await api.allSessions(200)) ?? [])
      setError('')
    } catch (e) {
      setError(e.message)
    } finally {
      setLoaded(true)
    }
  }, [only?.id])

  useEffect(() => {
    refresh()
    // Журнал пополняется только по завершении занятия: раз в
    // полминуты достаточно, чаще — лишний шум в сети.
    const timer = setInterval(refresh, 30000)
    return () => clearInterval(timer)
  }, [refresh])

  const names = useMemo(() => {
    const out = { rooms: {}, tasks: {}, devices: {} }
    for (const r of rooms ?? []) {
      out.rooms[r.id] = r.name
      for (const t of r.tasks ?? []) out.tasks[t.id] = t.name
      for (const d of r.devices ?? []) out.devices[d.id] = d.name
    }
    return out
  }, [rooms])

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    return list.filter((s) => {
      if (room && s.room !== room && !(s.rooms ?? []).includes(room)) return false
      if (!q) return true
      return [s.team, s.examinee, s.status, s.reason]
        .filter(Boolean)
        .some((v) => v.toLowerCase().includes(q))
    })
  }, [list, query, room])

  const open = shown.find((s) => s.id === openId) ?? shown[0] ?? null

  return (
    <div className="grid h-full min-h-0 gap-4 [grid-template-columns:1fr] xl:[grid-template-columns:minmax(21rem,26rem)_1fr]">
      <Card
        title="Журнал занятий"
        extra={
          <Status tone="idle" size="sm">
            {shown.length} из {list.length}
          </Status>
        }
        className="flex min-h-0 flex-col"
        bodyClass="flex min-h-0 flex-1 flex-col"
      >
        <div className="mb-3 grid gap-3 sm:grid-cols-2">
          <Field
            label="Поиск" value={query} onChange={setQuery}
            autoComplete="off" placeholder="Звено 1"
          />
          {!only && (
            <div>
              <label className="block t-sm text-[var(--color-ink-soft)]" htmlFor="room">
                Помещение
              </label>
              <select
                id="room" value={room} onChange={(e) => setRoom(e.target.value)}
                className="mt-1 h-11 w-full rounded border px-3 t-base"
                style={{ background: 'var(--color-surface)', borderColor: 'var(--color-line-strong)' }}
              >
                <option value="">все</option>
                {(rooms ?? []).map((r) => (
                  <option key={r.id} value={r.id}>{r.name}</option>
                ))}
              </select>
            </div>
          )}
        </div>

        {error && (
          <p role="alert" className="mb-2 t-sm" style={{ color: 'var(--color-alarm)' }}>
            {error}
          </p>
        )}

        <ul className="min-h-0 flex-1 space-y-2 overflow-auto pr-1">
          {shown.length === 0 && (
            <li className="t-base text-[var(--color-ink-soft)]">
              {loaded ? 'Занятий пока нет.' : 'Читаю журнал…'}
            </li>
          )}
          {shown.map((s) => (
            <li key={s.id}>
              <button
                type="button" onClick={() => setOpenId(s.id)}
                aria-current={open?.id === s.id}
                className="w-full rounded border px-3 py-2 text-left"
                style={{
                  background: open?.id === s.id ? 'var(--color-surface-muted)' : 'var(--color-surface)',
                  borderColor: open?.id === s.id ? 'var(--color-line-strong)' : 'var(--color-line)',
                }}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="t-base font-medium">
                    {s.team || s.examinee || 'без названия'}
                  </span>
                  <Status tone={TONE[s.outcome] ?? 'idle'} size="sm">{s.status}</Status>
                </div>
                <div className="t-sm text-[var(--color-ink-soft)]">
                  {fmt.time(s.started)} · {names.rooms[s.room] ?? s.room} · {clock(s.seconds)}
                  {s.report && ' · отчёт готов'}
                </div>
              </button>
            </li>
          ))}
        </ul>
      </Card>

      {open ? (
        <Session session={open} names={names} onChanged={refresh} />
      ) : (
        <Card title="Разбор занятия">
          <p className="t-base text-[var(--color-ink-soft)]">
            Выберите занятие в журнале слева.
          </p>
        </Card>
      )}
    </div>
  )
}

function Session({ session: s, names, onChanged }) {
  const [comment, setComment] = useState(s.comment ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    setComment(s.comment ?? '')
  }, [s.id, s.comment])

  const save = async () => {
    setBusy(true)
    setError('')
    try {
      await api.comment(s.id, comment)
      onChanged?.()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const rooms = (s.rooms?.length ? s.rooms : [s.room]).map((r) => names.rooms[r] ?? r)

  return (
    <div className="fill-grid min-h-0 overflow-auto" style={{ '--fill-min': '24rem' }}>
      <Card
        title={s.team || s.examinee || 'Занятие'}
        extra={<Status tone={TONE[s.outcome] ?? 'idle'} size="sm">{s.status}</Status>}
      >
        <dl className="grid grid-cols-[150px_1fr] gap-y-1 t-base">
          <Row label="Начато">{fmt.time(s.started)}</Row>
          <Row label="Продолжительность">{clock(s.seconds)}</Row>
          <Row label="Помещения">{rooms.join(', ')}</Row>
          {s.size > 0 && <Row label="В звене">{s.size} чел.</Row>}
          {s.operator && <Row label="Открыл">{s.operator}</Row>}
          {s.code && <Row label="Код задания">{s.code}</Row>}
          {s.attempts > 0 && <Row label="Попыток кода">{s.attempts}</Row>}
          {s.reason && <Row label="Причина завершения">{s.reason}</Row>}
          {s.temp?.samples > 0 && (
            <Row label="Температура">
              {fmt.temp(s.temp.min_c)}…{fmt.temp(s.temp.max_c)} °C, средняя {fmt.temp(s.temp.avg_c)} °C
            </Row>
          )}
        </dl>

        {/* Папка отчёта — то, что отдают целиком: PDF и видео рядом. */}
        <div className="mt-4 border-t pt-3" style={{ borderColor: 'var(--color-line)' }}>
          {s.report_url ? (
            <div className="flex flex-wrap items-center gap-3">
              <a href={s.report_url} target="_blank" rel="noreferrer">
                <Button variant="primary">Открыть отчёт PDF</Button>
              </a>
            </div>
          ) : (
            <p className="t-sm text-[var(--color-ink-soft)]">
              Отчёта нет: занятие закрыто до того, как центр стал их
              складывать.
            </p>
          )}

          {s.report && (
            <p className="mt-2 t-sm text-[var(--color-ink-soft)]">
              Отчёт и видеозаписи сложены автоматически при завершении
              занятия: <code className="t-xs">{s.report}</code>
            </p>
          )}
        </div>
      </Card>

      <Card title="Задания маршрута">
        {s.tasks?.length > 0 ? (
          <ul className="space-y-1 t-base">
            {s.tasks.map((t) => (
              <li key={t.task} className="flex items-baseline justify-between gap-3">
                <span>{names.tasks[t.task] ?? t.task}</span>
                <span className="t-sm text-[var(--color-ink-soft)]">
                  {t.done
                    ? `${clock(t.seconds)} · ${t.auto ? 'сигнал' : t.by || 'администратор'}`
                    : 'не пройдено'}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="t-base text-[var(--color-ink-soft)]">
            В этом помещении маршрута нет: зачёт по упражнениям.
          </p>
        )}
      </Card>

      {s.assignments?.length > 0 && (
        <Card title="Упражнения">
          <ul className="space-y-1 t-base">
            {s.assignments.map((a, i) => (
              <li key={`${a.device}-${i}`}>
                {names.devices[a.device] ?? a.device} — {clock(a.seconds)}
                {a.lifts_target > 0 ? (
                  <>
                    , подъёмов задано {a.lifts_target}, выполнено {a.lifts ?? 0}
                    {' '}
                    <span
                      style={{
                        color: (a.lifts ?? 0) >= a.lifts_target
                          ? 'var(--color-ok)'
                          : 'var(--color-alarm)',
                      }}
                    >
                      {(a.lifts ?? 0) >= a.lifts_target
                        ? '— норма выполнена'
                        : `— не добрано ${a.lifts_target - (a.lifts ?? 0)}`}
                    </span>
                  </>
                ) : (
                  a.lifts > 0 && `, подъёмов ${a.lifts}`
                )}
                {a.target?.seconds > 0 && `, срок ${clock(a.target.seconds)}`}
              </li>
            ))}
          </ul>
        </Card>
      )}

      <Card title="Видеозаписи">
        {s.records?.length > 0 ? (
          <ul className="space-y-2 t-base">
            {s.records.map((rec) => (
              <li key={rec.camera}>
                {rec.ok ? (
                  <a
                    href={rec.url} target="_blank" rel="noreferrer"
                    className="underline" style={{ color: 'var(--color-action)' }}
                  >
                    {rec.camera} · {clock(rec.seconds)} · {(rec.bytes / 1048576).toFixed(0)} МБ
                  </a>
                ) : (
                  <span style={{ color: 'var(--color-alarm)' }}>
                    {rec.camera}: {rec.error || 'записи нет'}
                  </span>
                )}
              </li>
            ))}
          </ul>
        ) : (
          <p className="t-base text-[var(--color-ink-soft)]">
            Записей нет: камеры были отключены.
          </p>
        )}
      </Card>

      <Card title="Комментарий администратора">
        <textarea
          value={comment} onChange={(e) => setComment(e.target.value)}
          rows={4}
          className="w-full rounded border p-3 t-base"
          style={{ background: 'var(--color-surface)', borderColor: 'var(--color-line-strong)' }}
          placeholder="Разбор занятия"
        />
        <div className="mt-2 flex items-center gap-3">
          <Button onClick={save} disabled={busy}>
            {busy ? 'Сохраняю…' : 'Сохранить'}
          </Button>
          <span className="t-sm text-[var(--color-ink-soft)]">
            Правится потом: разбор бывает не сразу.
          </span>
        </div>
        {error && (
          <p role="alert" className="mt-2 t-sm" style={{ color: 'var(--color-alarm)' }}>
            {error}
          </p>
        )}
      </Card>
    </div>
  )
}

function Row({ label, children }) {
  return (
    <>
      <dt className="text-[var(--color-ink-soft)]">{label}</dt>
      <dd>{children}</dd>
    </>
  )
}

function clock(seconds) {
  const m = Math.floor((seconds ?? 0) / 60)
  return `${m}:${String((seconds ?? 0) % 60).padStart(2, '0')}`
}
