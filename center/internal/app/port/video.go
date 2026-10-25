package port

import (
	"context"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
)

// CameraRepository отдаёт список камер объекта. Реализация читает
// YAML-файл; при переходе на базу меняется только она.
type CameraRepository interface {
	Cameras(ctx context.Context) ([]video.Camera, error)
}

// StreamPublisher ведёт трансляцию одной камеры: забирает поток у
// источника и выкладывает его в формате, который играет браузер.
//
// Реализация (ffmpeg → HLS) заменяема: под WebRTC достаточно второй
// реализации этого интерфейса, прикладной слой не меняется.
type StreamPublisher interface {
	// Run ведёт трансляцию до остановки ctx или сбоя. Возвращает nil
	// только при штатной остановке по контексту; любой иной выход —
	// ошибка, по которой надзор перезапустит конвейер.
	//
	// onLive вызывается, когда зритель уже может подключиться; за одну
	// трансляцию может сработать только раз.
	Run(ctx context.Context, cam video.Camera, onLive func()) error

	// PlaylistURL — путь, по которому панель откроет эту камеру.
	PlaylistURL(id video.CameraID) string
}

// LayoutRepository отдаёт раскладку объекта: комнаты, их тренажёры и
// камеры. Реализация читает YAML; при переходе на базу меняется только она.
type LayoutRepository interface {
	Object(ctx context.Context) (layout.Object, error)
}

// SessionRepository хранит занятия: открытое в комнате и закрытые.
//
// Реализация в памяти; при переходе на базу меняется только она —
// занятия должны переживать перезапуск центра, иначе зачёт теряется.
type SessionRepository interface {
	// Save создаёт или обновляет занятие.
	Save(ctx context.Context, s training.Session) error
	// Active возвращает открытое занятие комнаты.
	Active(ctx context.Context, room string) (training.Session, bool, error)
	// Recent возвращает последние закрытые занятия комнаты, новые первыми.
	Recent(ctx context.Context, room string, limit int) ([]training.Session, error)
	// ByRescuer возвращает закрытые занятия карточки, новые первыми —
	// это и есть история тренировок в карточке спасателя.
	ByRescuer(ctx context.Context, iin string, limit int) ([]training.Session, error)
	// Get возвращает занятие по идентификатору: комментарий пишут
	// к прошедшему занятию, а не к открытому.
	Get(ctx context.Context, id string) (training.Session, bool, error)
}

// CardRepository хранит карточки спасателей.
//
// Уникальность ИИН обеспечивает реализация: проверка в коде не спасает
// от двух администраторов, заводящих одного человека одновременно.
type CardRepository interface {
	// Create заводит карточку. Повтор ИИН — rescuer.ErrDuplicate.
	Create(ctx context.Context, c rescuer.Card) error
	// Update меняет ФИО, подразделение и признак архивности.
	Update(ctx context.Context, c rescuer.Card) error
	// Get возвращает карточку по ИИН.
	Get(ctx context.Context, iin rescuer.IIN) (rescuer.Card, bool, error)
	// List возвращает карточки: поиск по ФИО и ИИН, без архивных,
	// если withArchived не задан.
	List(ctx context.Context, query string, withArchived bool) ([]rescuer.Card, error)
}

// RoomStateRepository хранит состояния комнат.
//
// Состояние переживает перезапуск центра: комната, оставшаяся в
// аварии, не должна «забыть» об этом от перезагрузки сервера.
type RoomStateRepository interface {
	Save(ctx context.Context, s layout.RoomState) error
	Get(ctx context.Context, room layout.RoomID) (layout.RoomState, bool, error)
	All(ctx context.Context) ([]layout.RoomState, error)
}

// Recorder ведёт видеозапись занятия.
//
// Запись отдельна от трансляции намеренно: Live идёт постоянно, а
// ролик охватывает только время занятия — от «Начать тренировку» до
// завершения (сценарий §6).
type Recorder interface {
	// Start начинает запись камер комнаты. Ошибка одной камеры не
	// отменяет занятие: тренировка важнее ролика.
	Start(ctx context.Context, session string, cameras []video.Camera)
	// Stop останавливает запись и возвращает получившиеся ролики.
	Stop(session string) []training.Record
}

// AlarmJournal хранит события аварийной остановки.
//
// Отдельно от состояний комнат: состояние говорит, как сейчас, а
// журнал — когда и почему это случилось (сценарий §7).
type AlarmJournal interface {
	Add(ctx context.Context, e layout.AlarmEvent) error
	// Recent возвращает последние события, новые первыми.
	Recent(ctx context.Context, limit int) ([]layout.AlarmEvent, error)
	// Resolve отмечает снятие аварии.
	Resolve(ctx context.Context, id string, at time.Time) error
}
