import { useEffect, useRef, useState } from 'react'

// Кнопки. Высота 44 пикселя — минимум, в который уверенно попадают
// пальцем, в том числе по сенсорному экрану на стойке.

const VARIANTS = {
  primary: {
    background: 'var(--color-action)',
    color: 'var(--color-action-ink)',
    borderColor: 'var(--color-action)',
  },
  secondary: {
    background: 'var(--color-surface)',
    color: 'var(--color-ink)',
    borderColor: 'var(--color-line-strong)',
  },
  danger: {
    background: 'var(--color-alarm)',
    color: '#ffffff',
    borderColor: 'var(--color-alarm)',
  },
}

export default function Button({
  variant = 'secondary', size = 'md', children, className = '', ...rest
}) {
  const style = VARIANTS[variant] ?? VARIANTS.secondary
  const pad = size === 'sm' ? 'h-9 px-3 text-sm' : 'h-11 px-4 t-base'

  return (
    <button
      type="button"
      className={`inline-flex items-center justify-center gap-2 rounded border font-medium
        transition-colors disabled:cursor-not-allowed disabled:opacity-45 ${pad} ${className}`}
      style={style}
      {...rest}
    >
      {children}
    </button>
  )
}

/*
 * Кнопка с подтверждением для действий, которые меняют состояние
 * железа: сброс препятствия на трассу, включение нагрева.
 *
 * Подтверждение встроено в саму кнопку, а не в отдельное окно: окно
 * закрывает собой показания, а в диспетчерской нельзя терять из виду
 * то, чем управляешь. Нажатие мимо и клавиша Esc отменяют.
 */
export function ConfirmButton({
  variant = 'danger', size = 'md', confirmText = 'Подтвердить',
  children, onConfirm, disabled, className = '',
}) {
  const [asking, setAsking] = useState(false)
  const box = useRef(null)

  useEffect(() => {
    if (!asking) return

    const away = (e) => {
      if (box.current && !box.current.contains(e.target)) setAsking(false)
    }
    const esc = (e) => {
      if (e.key === 'Escape') setAsking(false)
    }

    document.addEventListener('mousedown', away)
    document.addEventListener('keydown', esc)
    return () => {
      document.removeEventListener('mousedown', away)
      document.removeEventListener('keydown', esc)
    }
  }, [asking])

  if (!asking) {
    return (
      <Button
        variant={variant} size={size} disabled={disabled}
        className={className} onClick={() => setAsking(true)}
      >
        {children}
      </Button>
    )
  }

  return (
    <span ref={box} className={`inline-flex gap-1 ${className}`}>
      <Button
        variant={variant} size={size} autoFocus
        onClick={() => { setAsking(false); onConfirm?.() }}
      >
        {confirmText}
      </Button>
      <Button variant="secondary" size={size} onClick={() => setAsking(false)}>
        Отмена
      </Button>
    </span>
  )
}
