import React from 'react'
import ReactDOM from 'react-dom/client'
import '../index.css'

// Общая точка запуска страницы. Каждая страница панели — свой файл и
// свой бандл: раздел открывается в отдельной вкладке и живёт сам по
// себе, не завися от того, что делают в соседнем окне.
export default function boot(element) {
  ReactDOM.createRoot(document.getElementById('root')).render(
    <React.StrictMode>{element}</React.StrictMode>,
  )
}
