import { useEffect, useMemo, useState } from 'react'
import Button from './ui/Button.jsx'
import Field from './ui/Field.jsx'
import Status from './ui/Status.jsx'
import { byTeam } from '../lib/flow.js'
import { fmt } from '../lib/format.js'
import { api } from '../lib/api.js'
import { openRoster } from '../shell/AppWindow.jsx'

// Занятие в комнате: какая команда сейчас в зале.
//
// Тренировку проходит команда, а карточки входят в её состав: ФИО и
// ИИН берутся из картотеки, результат и видеозапись связываются с
// карточкой каждого участника. ФИО экзаменуемого остаётся для занятий
// без картотеки — тогда оно вводится руками.

export default function Training({ room, canOperate, onChanged }) {
  const session = room.session
  return session
    ? <Running room={room} session={session} canOperate={canOperate} onChanged={onChanged} />
    : <StartForm room={room} canOperate={canOperate} />
}

// Перед тренировкой состав собирают в отдельном окне: сперва ФИО и
// ИИН, и только потом занятие. На экране комнаты этих полей нет
// намеренно — там смотрят за залом, а не заполняют бумаги.
function StartForm({ room, canOperate }) {
  const ready = room.state === 'ready'

  return (
    <div className="space-y-2">
      <p className="t-base">
        {!ready
          ? 'Занятие не идёт.'
          : byTeam(room)
            ? 'Комната готова. Соберите звено — состав вводится в отдельном окне.'
            : 'Комната готова. Выберите участников — они вводятся в отдельном окне.'}
      </p>

      {canOperate && room.roster && (
        <div className="flex flex-wrap items-center gap-3">
          <span className="t-sm text-[var(--color-ink-soft)]">
            Записано: {room.roster.size} чел.
            {room.roster.team && `, ${room.roster.team}`}
          </span>
          <Button size="sm" onClick={() => openRoster(room.id)}>
            Изменить состав
          </Button>
        </div>
      )}
    </div>
  )
}

function Running({ room, session, canOperate, onChanged }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [reason, setReason] = useState('')
  const [, tick] = useState(0)
  const [cards, setCards] = useState([])

  // Счётчик времени идёт сам: опрос комнат раз в пять секунд, а
  // секунды на экране должны меняться каждую секунду.
  useEffect(() => {
    const timer = setInterval(() => tick((t) => t + 1), 1000)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    let dropped = false
    api.rescuers()
      .then((list) => !dropped && setCards(list ?? []))
      .catch(() => {})
    return () => {
      dropped = true
    }
  }, [])

  const names = useMemo(() => {
    const by = new Map(cards.map((c) => [c.iin, c.name]))
    return (session.rescuers ?? []).map((iin) => by.get(iin) ?? iin)
  }, [cards, session.rescuers])

  const elapsed = Math.max(0, Math.floor((Date.now() - new Date(session.started)) / 1000))

  const finish = async () => {
    setBusy(true)
    setError('')
    try {
      await api.finishSession(room.id, { reason: reason.trim() })
      setReason('')
      onChanged?.()
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <Status tone="ok" size="sm">
              {byTeam(room) ? 'идёт тренировка' : 'идёт занятие'}
            </Status>
            <span className="t-lg font-semibold">
              {(byTeam(room) ? session.team : session.examinee) ||
                session.examinee ||
                session.team ||
                'без названия'}
            </span>
          </div>

          <p className="mt-1 t-sm text-[var(--color-ink-soft)]">
            начато в {fmt.time(session.started)} · идёт {clock(elapsed)}
            {session.operator && ` · открыл ${session.operator}`}
          </p>

          {names.length > 0 && (
            <p className="mt-1 t-base">
              <span className="text-[var(--color-ink-soft)]">
                {byTeam(room) ? 'состав звена: ' : 'участники: '}
              </span>
              {names.join(', ')}
            </p>
          )}
        </div>

        <div className="readout">{clock(elapsed)}</div>
      </div>

      {/* Код задания «Набор чисел»: администратор сообщает его
          участникам до старта (сценарий дымокамеры §4). */}
      {session.code && (
        <p className="t-base">
          <span className="text-[var(--color-ink-soft)]">код для стойки: </span>
          <span className="readout readout-sm">{session.code}</span>
          {session.attempts > 0 && (
            <span className="ml-2 t-sm text-[var(--color-ink-soft)]">
              попыток: {session.attempts}
            </span>
          )}
        </p>
      )}

      {canOperate && (
        <div className="flex flex-wrap items-end gap-3 border-t pt-3" style={{ borderColor: 'var(--color-line)' }}>
          <div className="min-w-[260px] flex-1">
            <Field
              label="Причина досрочного завершения"
              value={reason}
              onChange={setReason}
              hint="Пусто — занятие закрывается как обычное"
              autoComplete="off"
              placeholder="например: участнику стало плохо"
            />
          </div>
          <Button variant="danger" onClick={finish} disabled={busy}>
            {busy ? 'Завершаю…' : byTeam(room) ? 'Завершить тренировку' : 'Завершить занятие'}
          </Button>
        </div>
      )}

      {error && (
        <p role="alert" className="t-sm" style={{ color: 'var(--color-alarm)' }}>
          {error}
        </p>
      )}
    </div>
  )
}

// clock форматирует время тренировки: смотрят минуты и секунды, а не
// «15 мин».
function clock(seconds) {
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

// Итог прошлого занятия — показывается под комнатой сразу после
// завершения, чтобы результат не пришлось искать в журнале.
export function LastResult({ session, taskNames }) {
  if (!session) return null

  // Зачёт велоэргометра и лестницы — по времени, поэтому в итоге
  // стоит продолжительность, а не метры и этажи. У ударного молота
  // зачёт по подъёмам, и они остаются.
  const r = session.result
  const parts = [
    r?.sets > 0 && `подходов ${r.sets}`,
    r?.max_load_kg > 0 && `вес до ${fmt.speed(r.max_load_kg)} кг`,
  ].filter(Boolean)

  const tone = session.outcome === 'success'
    ? 'ok'
    : session.outcome === 'alarm'
      ? 'alarm'
      : 'warn'

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2 t-sm text-[var(--color-ink-soft)]">
        <Status tone={tone} size="sm">{session.status}</Status>
        <span className="text-[var(--color-ink)]">
          {session.team || session.examinee}
        </span>
        <span>· {clock(session.seconds)}</span>
        {parts.length > 0 && <span>· {parts.join(', ')}</span>}
        {session.reason && <span>· причина: {session.reason}</span>}
      </div>

      {/* Отметки заданий: когда и каким способом подтверждено
          (сценарий §6). */}
      {session.tasks?.length > 0 && (
        <ul className="space-y-1">
          {session.tasks.filter((t) => t.done).map((t) => (
            <li key={t.task} className="t-sm text-[var(--color-ink-soft)]">
              {taskNames?.[t.task] ?? t.task} — {clock(t.seconds)}{' '}
              {t.auto ? 'по сигналу тренажёра' : `отметил ${t.by || 'администратор'}`}
            </li>
          ))}
        </ul>
      )}

      {session.temp?.samples > 0 && (
        <p className="t-sm text-[var(--color-ink-soft)]">
          температура: {fmt.temp(session.temp.min_c)}…{fmt.temp(session.temp.max_c)} °C,
          средняя {fmt.temp(session.temp.avg_c)} °C
        </p>
      )}

      {/* Видеозапись охватывает только тренировку: подготовка в
          ролик не входит. */}
      {session.records?.length > 0 && (
        <ul className="flex flex-wrap gap-3">
          {session.records.map((rec) => (
            <li key={rec.camera} className="t-sm">
              {rec.ok ? (
                <a
                  href={rec.url} target="_blank" rel="noreferrer"
                  className="underline" style={{ color: 'var(--color-action)' }}
                >
                  запись {rec.camera} · {clock(rec.seconds)}
                </a>
              ) : (
                <span style={{ color: 'var(--color-alarm)' }}>
                  запись {rec.camera}: {rec.error || 'не получилась'}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
