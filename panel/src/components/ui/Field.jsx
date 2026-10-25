// Поле ввода с подписью, подсказкой и ошибкой.
//
// Ошибка и подсказка связаны с полем через aria-describedby, а
// неверное значение помечается aria-invalid: иначе программа чтения
// с экрана прочитает поле, но не скажет, что с ним не так.

export default function Field({
  label, value, onChange, error, hint, suffix, id, ...rest
}) {
  const fieldId = id || `f-${label.replace(/\s+/g, '-').toLowerCase()}`
  const errorId = `${fieldId}-error`
  const hintId = `${fieldId}-hint`

  return (
    <div>
      <label htmlFor={fieldId} className="block t-sm text-[var(--color-ink-soft)]">
        {label}
      </label>

      <div className="mt-1 flex items-center gap-2">
        <input
          id={fieldId}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={error ? 'true' : undefined}
          aria-describedby={error ? errorId : hint ? hintId : undefined}
          className="tabular h-11 w-full rounded border px-3 t-base text-[var(--color-ink)]"
          style={{
            background: 'var(--color-surface)',
            borderColor: error ? 'var(--color-alarm)' : 'var(--color-line-strong)',
          }}
          {...rest}
        />
        {suffix && <span className="t-base text-[var(--color-ink-soft)]">{suffix}</span>}
      </div>

      {error ? (
        <p id={errorId} className="mt-1 t-sm" style={{ color: 'var(--color-alarm)' }}>
          {error}
        </p>
      ) : hint ? (
        <p id={hintId} className="mt-1 t-sm text-[var(--color-ink-faint)]">{hint}</p>
      ) : null}
    </div>
  )
}
