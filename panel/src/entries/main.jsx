import boot from './boot.jsx'
import AppWindow from '../shell/AppWindow.jsx'
import Main from '../pages/Main.jsx'

// Главное окно комплекса: из него открываются окна помещений.
boot(
  <AppWindow title="Тренажёрный комплекс">
    {({ rooms, roomsLoaded }) => (
      <main className="min-h-0 flex-1 overflow-auto p-4">
        <Main rooms={rooms} roomsLoaded={roomsLoaded} />
      </main>
    )}
  </AppWindow>,
)
