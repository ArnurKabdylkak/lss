import { useEffect, useRef } from 'react'
import {
  Chart as ChartJS, LineController, LineElement, PointElement,
  LinearScale, TimeScale, Tooltip, Legend, Filler,
} from 'chart.js'

ChartJS.register(LineController, LineElement, PointElement, LinearScale,
  TimeScale, Tooltip, Legend, Filler)

// График истории. Ось времени — числовая, без адаптера дат: лишняя
// зависимость в закрытом контуре дороже собственного форматирования.
export default function Chart({ series, height = 220 }) {
  const canvas = useRef(null)
  const chart = useRef(null)

  useEffect(() => {
    if (!canvas.current) return

    chart.current = new ChartJS(canvas.current, {
      type: 'line',
      data: { datasets: [] },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        interaction: { mode: 'index', intersect: false },
        scales: {
          x: {
            type: 'linear',
            ticks: {
              color: '#545b66',
              callback: (v) => new Date(v).toLocaleTimeString('ru-RU'),
              maxTicksLimit: 6,
            },
            grid: { color: '#e6e8ec' },
          },
          y: {
            ticks: { color: '#545b66' },
            grid: { color: '#e6e8ec' },
          },
        },
        plugins: {
          legend: { labels: { color: '#16191d', boxWidth: 12, usePointStyle: true } },
          tooltip: {
            callbacks: {
              title: (items) => new Date(items[0].parsed.x).toLocaleString('ru-RU'),
            },
          },
        },
      },
    })

    return () => chart.current?.destroy()
  }, [])

  useEffect(() => {
    if (!chart.current) return
    chart.current.data.datasets = series
    chart.current.update('none')
  }, [series])

  return (
    <div style={{ height }}>
      <canvas ref={canvas} />
    </div>
  )
}
