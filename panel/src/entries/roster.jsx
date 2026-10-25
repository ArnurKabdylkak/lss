import boot from './boot.jsx'
import AppWindow from '../shell/AppWindow.jsx'
import Roster from '../pages/Roster.jsx'

// Окно состава: ФИО и ИИН заносят здесь, до подготовки помещения.
//
// Своё окно, а не вкладка в окне помещения: состав набирают у входа в
// зал, пока на экране помещения следят за обстановкой. Записали —
// окно закрылось, а окно помещения узнаёт об этом сразу.
const roomId = new URLSearchParams(window.location.search).get('room') || ''

boot(
  <AppWindow
    title={({ rooms }) => {
      const name = rooms.find((r) => r.id === roomId)?.name
      return name ? `Состав · ${name}` : 'Состав'
    }}
    onClose={() => window.close()}
  >
    {({ rooms, canOperate }) => {
      const room = rooms.find((r) => r.id === roomId)
      return (
        <main className="min-h-0 flex-1 overflow-auto p-4">
          {room ? (
            <Roster
              room={room} canOperate={canOperate}
              onSaved={() => {
                // Окно помещения не должно ждать очередного опроса.
                try {
                  window.opener?.postMessage({ type: 'mchs-roster-saved', room: roomId }, '*')
                } catch {
                  // чужое окно могло закрыться — не беда
                }
                window.close()
              }}
            />
          ) : (
            <p className="t-base text-[var(--color-ink-soft)]">Читаю раскладку объекта…</p>
          )}
        </main>
      )
    }}
  </AppWindow>,
)
