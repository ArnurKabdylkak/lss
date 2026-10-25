// Стрелочный индикатор: дуга, значение, подпись.
//
// Рисуется SVG, а не библиотекой: одна дуга не стоит зависимости,
// которую потом вносить в закрытый контур.
export default function Gauge({ label, value, unit, min = 0, max = 100, danger, stale }) {
  const clamped = Math.max(min, Math.min(value ?? 0, max))
  const ratio = (clamped - min) / (max - min || 1)

  // Дуга в 240° — привычный вид прибора: разрыв внизу отделяет начало
  // шкалы от конца, иначе на круге их не различить.
  const arc = 240
  const start = 150
  const radius = 70
  const center = 90

  const point = (angle) => {
    const rad = (angle * Math.PI) / 180
    return [center + radius * Math.cos(rad), center + radius * Math.sin(rad)]
  }

  const [x0, y0] = point(start)
  const [x1, y1] = point(start + arc * ratio)
  const [xe, ye] = point(start + arc)

  const path = (ax, ay, bx, by, sweep) =>
    `M ${ax} ${ay} A ${radius} ${radius} 0 ${sweep} 1 ${bx} ${by}`

  const overLimit = danger != null && value >= danger
  const color = stale ? '#475569' : overLimit ? '#ef4444' : '#22d3ee'

  return (
    <div className="flex flex-col items-center">
      <svg viewBox="0 0 180 150" className="w-full max-w-[200px]">
        <path
          d={path(x0, y0, xe, ye, arc > 180 ? 1 : 0)}
          fill="none" stroke="#1e293b" strokeWidth="14" strokeLinecap="round"
        />
        {ratio > 0 && (
          <path
            d={path(x0, y0, x1, y1, arc * ratio > 180 ? 1 : 0)}
            fill="none" stroke={color} strokeWidth="14" strokeLinecap="round"
          />
        )}
        <text x={center} y={center + 4} textAnchor="middle"
              className="fill-slate-100" fontSize="30" fontWeight="600">
          {stale ? '—' : value}
        </text>
        <text x={center} y={center + 26} textAnchor="middle"
              className="fill-slate-400" fontSize="13">
          {unit}
        </text>
      </svg>
      <div className="text-sm text-slate-400 -mt-2">{label}</div>
      {danger != null && (
        <div className="text-xs text-slate-500 mt-1">порог {danger} {unit}</div>
      )}
    </div>
  )
}
