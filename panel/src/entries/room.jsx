import boot from './boot.jsx'
import AppWindow from '../shell/AppWindow.jsx'
import RoomWindow from '../pages/RoomWindow.jsx'

// Окно помещения: работа, состав, результаты и устройства — вкладками
// внутри окна, потому что всё это относится к одному помещению.
const roomId = new URLSearchParams(window.location.search).get('room') || ''

boot(
  <AppWindow
    title={({ rooms }) => rooms.find((r) => r.id === roomId)?.name || 'Помещение'}
    onClose={window.opener ? () => window.close() : undefined}
  >
    {({ rooms, roomsLoaded, canOperate, refreshRooms }) => (
      <RoomWindow
        roomId={roomId} rooms={rooms} roomsLoaded={roomsLoaded}
        canOperate={canOperate} onChanged={refreshRooms}
      />
    )}
  </AppWindow>,
)
