import { useCallback, useEffect, useMemo, useState } from 'react'
import Button from '../components/ui/Button.jsx'
import Card from '../components/ui/Card.jsx'
import Field from '../components/ui/Field.jsx'
import Status from '../components/ui/Status.jsx'
import { api, Unauthorized } from '../lib/api.js'
import { byTeam } from '../lib/flow.js'

// Состав и картотека — вкладка окна помещения.
//
// Порядок по сценарию такой: сперва ФИО и ИИН, потом тренировка. Пока
// состав не подтверждён, занятие не открывается: показания без имён —
// поток чисел, который через час не привязать к человеку.
//
// В дымокамере маршрут проходят **звеном**, и у него есть название.
// В теплокамере звеньев нет: там каждый работает на своём тренажёре,
// результат считают по людям, поэтому поля названия нет и окно
// говорит об участниках занятия.

export default function Roster({ room, canOperate, onSaved }) {
  const roomId = room?.id
  const [cards, setCards] = useState([])
  const [picked, setPicked] = useState([])
  const [teamName, setTeamName] = useState('')
  const [query, setQuery] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)

  const refreshCards = useCallback(async (q) => {
    try {
      const list = await api.rescuers(q)
      setCards((list ?? []).filter((c) => !c.archived))
    } catch (e) {
      if (e instanceof Unauthorized) setError('Сессия не признана: войдите в панели заново.')
    }
  }, [])

  useEffect(() => {
    refreshCards('')
  }, [refreshCards])

  // Пока состав уже записан, он показан в списке выбранных: правка
  // состава — это замена списка, а не дозапись.
  useEffect(() => {
    if (room?.roster) {
      setPicked(room.roster.rescuers ?? [])
      setTeamName(room.roster.team ?? '')
    }
  }, [room?.roster?.since])

  const limit = room?.capacity ?? 0
  const team = byTeam(room)
  const full = limit > 0 && picked.length >= limit
  const names = useMemo(() => new Map(cards.map((c) => [c.iin, c.name])), [cards])

  const toggle = (iin) => {
    setError('')
    setPicked((cur) =>
      cur.includes(iin) ? cur.filter((x) => x !== iin) : full ? cur : [...cur, iin],
    )
  }

  const start = async () => {
    setBusy(true)
    setError('')
    try {
      await api.setRoster(roomId, { team: teamName.trim(), rescuers: picked })
      setDone(true)
      // Состав записан — возвращаем дежурного к работе с помещением.
      window.setTimeout(() => onSaved?.(), 700)
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    return (
      <Centered>
        <Status tone="ok">Состав записан</Status>
        <p className="mt-3 t-base text-[var(--color-ink-soft)]">
          Открываю комнату: помещение можно готовить.
        </p>
      </Centered>
    )
  }

  return (
    <div className="grid gap-4 lg:grid-cols-[1fr_1fr]">
      <div className="lg:col-span-2">
        <h1 className="t-xl font-semibold">
          {team ? 'Сбор звена' : 'Участники занятия'}
          {room ? ` · ${room.name}` : ''}
        </h1>
        <p className="t-sm text-[var(--color-ink-soft)]">
          {team
            ? 'Состав звена заносят до подготовки помещения: без него прогрев не начинают.'
            : 'Участников заносят до подготовки помещения; на тренажёры их ставят потом, в комнате.'}
        </p>
      </div>

      {/* Новая карточка — здесь же: человека заводят у входа, идти за
          этим в картотеку некогда. */}
      <NewCard onAdded={(iin) => {
        refreshCards(query)
        setPicked((cur) => (cur.includes(iin) || full ? cur : [...cur, iin]))
      }} />

      <Card
        title={team ? 'Состав звена' : 'Участники'}
        extra={
          <Status tone={picked.length > 0 ? 'ok' : 'idle'} size="sm">
            выбрано {picked.length}{limit > 0 && ` из ${limit}`}
          </Status>
        }
      >
        <div className="mb-3 grid gap-3 sm:grid-cols-2">
          {team && (
            <Field
              label="Название звена" value={teamName} onChange={setTeamName}
              hint="Необязательно: без него занятие назовут по участникам"
              autoComplete="off" placeholder="Звено 1"
            />
          )}
          <Field
            label="Поиск по картотеке" value={query}
            onChange={(v) => {
              setQuery(v)
              refreshCards(v)
            }}
            autoComplete="off" placeholder="Иванов"
          />
        </div>

        <div className="grid max-h-[320px] gap-2 overflow-auto sm:grid-cols-2">
          {cards.length === 0 && (
            <p className="t-sm text-[var(--color-ink-faint)]">
              Карточек нет: заведите слева.
            </p>
          )}
          {cards.map((c) => {
            const on = picked.includes(c.iin)
            return (
              <button
                key={c.iin} type="button" aria-pressed={on}
                onClick={() => toggle(c.iin)} disabled={!on && full}
                className="rounded border px-3 py-2 text-left t-base disabled:opacity-45"
                style={{
                  background: on ? 'var(--color-action)' : 'var(--color-surface)',
                  color: on ? 'var(--color-action-ink)' : 'var(--color-ink)',
                  borderColor: on ? 'var(--color-action)' : 'var(--color-line-strong)',
                }}
              >
                <div className="font-medium">{c.name}</div>
                <div className="t-xs opacity-80">
                  ИИН {c.iin}
                  {c.unit && ` · ${c.unit}`}
                </div>
              </button>
            )
          })}
        </div>

        {picked.length > 0 && (
          <p className="mt-3 t-sm text-[var(--color-ink-soft)]">
              в зал идут: {picked.map((iin) => names.get(iin) ?? iin).join(', ')}
          </p>
        )}

        {error && (
          <p role="alert" className="mt-3 t-sm" style={{ color: 'var(--color-alarm)' }}>
            {error}
          </p>
        )}

        {!canOperate && (
          <p className="mt-3 t-sm text-[var(--color-ink-soft)]">
            Состав записывает оператор.
          </p>
        )}

        <div className="mt-4 flex flex-wrap gap-2" hidden={!canOperate}>
          <Button
            variant="primary"
            disabled={busy || picked.length === 0}
            onClick={start}
          >
            {busy ? 'Записываю…' : 'Записать состав'}
          </Button>

          {/* Недоступная кнопка обязана объяснять себя: иначе её
              читают как поломку и ищут причину наугад. */}
          {picked.length === 0 && (
            <p className="self-center t-sm text-[var(--color-ink-soft)]">
              Отметьте хотя бы одного участника в картотеке.
            </p>
          )}

        </div>

        {room?.roster && (
          <p className="mt-3 t-sm text-[var(--color-ink-soft)]">
            Сейчас записано: {room.roster.size} чел.
            {room.roster.team && `, ${room.roster.team}`}. Новый список
            заменит прежний.
          </p>
        )}
      </Card>
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
      const added = iin.trim()
      setIin('')
      setName('')
      setUnit('')
      onAdded?.(added)
    } catch (err) {
      // Дословно: центр скажет, что карточка с таким ИИН уже есть.
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title="Новая карточка">
      <form onSubmit={add} className="grid gap-3">
        <Field
          label="ФИО" value={name} onChange={setName}
          autoComplete="off" placeholder="Иванов Пётр Сергеевич"
        />
        <div className="grid gap-3 sm:grid-cols-2">
          <Field
            label="ИИН" value={iin} onChange={setIin}
            hint="В произвольной форме" autoComplete="off" placeholder="900315501234"
          />
          <Field
            label="Подразделение" value={unit} onChange={setUnit}
            autoComplete="off" placeholder="СЧ-2"
          />
        </div>
        <div>
          <Button type="submit" disabled={busy || !iin.trim() || !name.trim()}>
            {busy ? 'Сохраняю…' : 'Завести и добавить в звено'}
          </Button>
        </div>
      </form>

      {error && (
        <p role="alert" className="mt-3 t-sm" style={{ color: 'var(--color-alarm)' }}>
          {error}
        </p>
      )}
    </Card>
  )
}

function Centered({ children }) {
  return (
    <div className="flex min-h-[200px] items-center justify-center p-6 text-center">
      <div>{children}</div>
    </div>
  )
}
