import { STEPS, stepIndex } from '../lib/flow.js'

// Шаговая линия комнаты: где мы в порядке работы.
//
// Состояние комнаты — не ярлык, а место в последовательности. Строка
// шагов показывает пройденное, текущее и оставшееся, поэтому сменщик
// видит обстановку, не читая журнал.

export default function Steps({ state }) {
  const alarm = state === 'alarm'
  const current = stepIndex(state)

  if (alarm) {
    return (
      <div
        className="rounded-md border px-4 py-3 t-lg font-semibold"
        style={{
          background: 'var(--color-alarm-bg)',
          borderColor: 'var(--color-alarm)',
          color: 'var(--color-alarm)',
        }}
      >
        Аварийная остановка. Порядок работы прерван
      </div>
    )
  }

  return (
    <ol className="flex flex-wrap items-stretch gap-1">
      {STEPS.map((s, i) => {
        const done = i < current
        const now = i === current
        return (
          <li
            key={s.id}
            aria-current={now ? 'step' : undefined}
            className="flex min-w-[120px] flex-1 items-center gap-2 rounded border px-3 py-2"
            style={{
              background: now ? 'var(--color-action)' : 'var(--color-surface)',
              color: now ? 'var(--color-action-ink)' : done ? 'var(--color-ink)' : 'var(--color-ink-faint)',
              borderColor: now ? 'var(--color-action)' : 'var(--color-line)',
            }}
          >
            {/* Номер шага — не украшение: по нему сверяются с
                инструкцией, где шаги тоже пронумерованы. */}
            <span
              className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border t-sm font-semibold"
              style={{
                borderColor: now ? 'var(--color-action-ink)' : 'var(--color-line-strong)',
              }}
            >
              {done ? '✓' : i + 1}
            </span>
            <span className="t-base font-medium">{s.short}</span>
          </li>
        )
      })}
    </ol>
  )
}
