// Карточка: белая плоскость, тонкая граница, без теней и свечения.
// Тень не несёт смысла и на светлом фоне только мылит края.
export default function Card({ title, extra, children, className = '', bodyClass = '', ...rest }) {
  return (
    <section
      className={`panel rounded-md border bg-[var(--color-surface)] ${className}`}
      style={{ borderColor: 'var(--color-line)' }}
      {...rest}
    >
      {(title || extra) && (
        <header
          className="flex items-center justify-between gap-3 border-b"
          style={{ borderColor: 'var(--color-line)' }}
        >
          <h2 className="font-semibold text-[var(--color-ink)]">{title}</h2>
          {extra}
        </header>
      )}
      <div className={bodyClass}>{children}</div>
    </section>
  )
}
