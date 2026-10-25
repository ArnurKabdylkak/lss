// Состояние показывается тремя способами разом: словом, цветом и
// формой значка. Одного цвета мало — около восьми процентов мужчин
// не различают красный и зелёный, а диспетчерскую панель читают
// быстро и боковым зрением.

const TONES = {
  ok: { bg: 'var(--color-ok-bg)', fg: 'var(--color-ok)', mark: '●' },
  warn: { bg: 'var(--color-warn-bg)', fg: 'var(--color-warn)', mark: '▲' },
  alarm: { bg: 'var(--color-alarm-bg)', fg: 'var(--color-alarm)', mark: '■' },
  idle: { bg: 'var(--color-idle-bg)', fg: 'var(--color-idle)', mark: '○' },
}

export default function Status({ tone = 'idle', children, size = 'md' }) {
  const t = TONES[tone] ?? TONES.idle
  const pad = size === 'sm' ? 'px-2 py-0.5 t-sm' : 'px-2.5 py-1 text-sm'

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded font-medium ${pad}`}
      style={{ background: t.bg, color: t.fg }}
    >
      <span aria-hidden="true" className="t-xs leading-none">{t.mark}</span>
      {children}
    </span>
  )
}
