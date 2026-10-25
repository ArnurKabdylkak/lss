import { defineConfig } from 'vite'

// Контур закрытый: ни одной внешней ссылки в бандле, всё собирается
// локально. Прокси на центр нужен только в разработке — в рабочей
// сборке панель отдаётся с того же адреса, что и API.
//
// JSX собирает встроенный в Vite esbuild: плагин на Babel потянул бы
// десятки пакетов, которые пришлось бы вносить в контур.
export default defineConfig({
  esbuild: { jsx: 'automatic' },
  server: {
    proxy: {
      '/api': { target: `http://localhost:${process.env.MCHS_CENTER_PORT || 8080}`, changeOrigin: true },
      '/healthz': { target: `http://localhost:${process.env.MCHS_CENTER_PORT || 8080}`, changeOrigin: true },
      // Плейлисты и сегменты трансляций: в разработке их отдаёт тот же
      // центр, что и API.
      '/video': { target: `http://localhost:${process.env.MCHS_CENTER_PORT || 8080}`, changeOrigin: true },
    },
  },
  // Два окна: главное и окно помещения. Разделов-страниц нет — всё,
  // что относится к помещению, живёт вкладками внутри его окна.
  build: {
    outDir: 'dist',
    assetsInlineLimit: 0,
    rollupOptions: {
      input: {
        index: 'index.html',
        room: 'room.html',
        roster: 'roster.html',
      },
    },
  },
})
