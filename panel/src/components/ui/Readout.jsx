// Показание: крупное число, единица, подпись.
//
// Круговые шкалы заменены числом и линейной полосой намеренно. Дугу
// читают «на глаз», а инструктору нужно точное значение и видимая
// граница порога; на полосе порог — это отметка, к которой ползёт
// заливка, и до неё видно расстояние.

export default function Readout({ label, value, unit, tone = 'normal', hint, stale }) {
  const color = stale
    ? 'var(--color-ink-faint)'
    : tone === 'alarm'
      ? 'var(--color-alarm)'
      : tone === 'warn'
        ? 'var(--color-warn)'
        : 'var(--color-ink)'

  return (
    <div>
      <div className="t-sm text-[var(--color-ink-soft)]">{label}</div>
      <div className="mt-1 flex items-baseline gap-1.5">
        <span className="readout" style={{ color }}>
          {stale ? '—' : value}
        </span>
        <span className="t-base text-[var(--color-ink-soft)]">{unit}</span>
      </div>
      {hint && <div className="mt-1 t-sm text-[var(--color-ink-faint)]">{hint}</div>}
    </div>
  )
}

/*
 * Линейная шкала с отметкой порога.
 *
 * Заливка показывает текущее значение, вертикальная черта — порог, за
 * которым срабатывает защита. Видно не только «сколько сейчас», но и
 * «сколько осталось до срабатывания».
 */
export function Bar({ value, min = 0, max = 100, limit, stale, label }) {
  const span = max - min || 1
  const ratio = Math.max(0, Math.min((Number(value) - min) / span, 1))
  const overLimit = limit != null && Number(value) >= limit

  const fill = stale
    ? 'var(--color-line-strong)'
    : overLimit
      ? 'var(--color-alarm)'
      : 'var(--color-ok)'

  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuenow={stale ? undefined : Number(value)}
      aria-valuemin={min}
      aria-valuemax={max}
    >
      <div
        className="relative h-2.5 w-full overflow-hidden rounded-sm"
        style={{ background: 'var(--color-surface-muted)', border: '1px solid var(--color-line)' }}
      >
        <div
          className="h-full"
          style={{ width: `${ratio * 100}%`, background: fill }}
        />
        {limit != null && limit > min && limit < max && (
          <span
            aria-hidden="true"
            className="absolute top-0 h-full"
            style={{
              left: `${((limit - min) / span) * 100}%`,
              width: '2px',
              background: 'var(--color-alarm)',
            }}
          />
        )}
      </div>

      {/* Подпись порога стоит ровно под своей отметкой, иначе она
          относится неизвестно к чему. */}
      <div className="relative mt-1 h-4 t-xs text-[var(--color-ink-faint)]">
        <span className="absolute left-0">{min}</span>
        {limit != null && limit > min && limit < max && (
          <span
            className="absolute -translate-x-1/2 whitespace-nowrap"
            style={{ left: `${((limit - min) / span) * 100}%`, color: 'var(--color-alarm)' }}
          >
            порог {limit}
          </span>
        )}
        <span className="absolute right-0">{max}</span>
      </div>
    </div>
  )
}
