package rooms

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/repo"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

type stubLayout struct{ obj layout.Object }

func (s stubLayout) Object(context.Context) (layout.Object, error) { return s.obj, nil }

type stubStreams struct{ states []video.State }

func (s stubStreams) States() []video.State { return s.states }
func (s stubStreams) PlaylistURL(id video.CameraID) string {
	return "/video/" + string(id) + "/index.m3u8"
}

func object() layout.Object {
	return layout.Object{
		Site: "obj-17",
		Rooms: []layout.Room{{
			ID:   "room-1",
			Name: "Тренажёрный зал",
			Devices: []layout.Device{
				{ID: "bike-1", Name: "Велосипед", Kind: layout.KindBike},
				{ID: "stairs-1", Kind: layout.KindStairs},
			},
			Heater:  layout.Heater{Device: "heater-1", LimitC: 60, ResumeC: 50},
			Prepare: layout.Prepare{TargetC: 40, Timeout: 30 * time.Minute},
			Cameras: []video.Camera{
				{ID: "cam-1", Enabled: true},
				{ID: "cam-2", Enabled: true},
			},
		}},
	}
}

func TestRoomsComposesState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := repo.NewMemory()

	// Велосипед прислал показания только что, лестница — давно,
	// пушка не выходила на связь вовсе.
	if err := state.Save(ctx, telemetry.Packet{
		Device:   "bike-1",
		Received: time.Now(),
		Data:     telemetry.Telemetry{SpeedKMH: 24},
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(ctx, telemetry.Packet{
		Device:   "stairs-1",
		Received: time.Now().Add(-time.Minute),
		Data:     telemetry.Telemetry{Floor: 7, Floors: 12},
	}); err != nil {
		t.Fatal(err)
	}

	streams := stubStreams{states: []video.State{
		{Camera: video.Camera{ID: "cam-1"}, Status: video.StatusLive},
		{Camera: video.Camera{ID: "cam-2"}, Status: video.StatusFailed},
	}}

	svc := New(stubLayout{obj: object()}, state, repo.NewStates(), repo.NewSessions(),
		streams, nil, nil, nil, nil, nil, repo.NewAlarms(), nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	views, err := svc.Rooms(ctx)
	if err != nil {
		t.Fatalf("Rooms() вернул ошибку: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("комнат %d, ожидалась одна", len(views))
	}
	room := views[0]

	bike := room.Devices[0]
	if !bike.Seen || bike.Stale {
		t.Errorf("велосипед должен быть на связи: %+v", bike)
	}
	if bike.Last.Data.SpeedKMH != 24 {
		t.Errorf("показания велосипеда не подхвачены: %+v", bike.Last.Data)
	}

	stairs := room.Devices[1]
	if !stairs.Seen || !stairs.Stale {
		t.Errorf("лестница молчит минуту и должна быть помечена потерянной: %+v", stairs)
	}
	if stairs.Device.Title() != "бесконечная лестница" {
		t.Errorf("устройство без имени называется по виду, получено %q", stairs.Device.Title())
	}

	if room.Heater == nil {
		t.Fatal("пушка комнаты не собрана")
	}
	if room.Heater.Seen {
		t.Error("пушка не выходила на связь, но помечена видимой")
	}

	// Ссылка на трансляцию — только у камеры в эфире: иначе плеер
	// панели будет биться о несуществующий плейлист.
	if room.Cameras[0].Playlist != "/video/cam-1/index.m3u8" {
		t.Errorf("камере в эфире не выдана ссылка: %+v", room.Cameras[0])
	}
	if room.Cameras[1].Playlist != "" {
		t.Errorf("упавшей камере выдана ссылка: %+v", room.Cameras[1])
	}
}

func TestRoomNotFound(t *testing.T) {
	t.Parallel()

	svc := New(stubLayout{obj: object()}, repo.NewMemory(), repo.NewStates(), repo.NewSessions(),
		stubStreams{}, nil, nil, nil, nil, nil, repo.NewAlarms(), nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, ok, err := svc.Room(context.Background(), "room-9"); ok || err != nil {
		t.Fatalf("Room(неизвестная) = ok %v, err %v", ok, err)
	}
	if _, ok, err := svc.Room(context.Background(), "room-1"); !ok || err != nil {
		t.Fatalf("Room(room-1) = ok %v, err %v", ok, err)
	}
}

// save кладёт последнее показание устройства в состояние.
func save(t *testing.T, state *repo.Memory, id string, data telemetry.Telemetry) {
	t.Helper()

	err := state.Save(context.Background(), telemetry.Packet{
		Device: telemetry.DeviceID(id), Received: time.Now(), Data: data,
	})
	if err != nil {
		t.Fatalf("подготовка состояния: %v", err)
	}
}

func service(t *testing.T) (*Service, *repo.Memory) {
	t.Helper()

	state := repo.NewMemory()
	svc := New(stubLayout{obj: object()}, state, repo.NewStates(), repo.NewSessions(),
		stubStreams{}, nil, nil, nil, nil, nil, repo.NewAlarms(), nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return svc, state
}

func TestRoomStateFlow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := service(t)

	// Пока комнату не трогали, она в «Ожидании» — это умолчание,
	// а не запись в базе.
	st, err := svc.State(ctx, "room-1")
	if err != nil || st.State != layout.StateWaiting {
		t.Fatalf("начальное состояние = %+v, err %v", st, err)
	}

	if _, err := svc.SetState(ctx, "room-1", layout.StatePreparing, "прогрев", "Дежурный"); err != nil {
		t.Fatalf("переход в подготовку отклонён: %v", err)
	}
	if _, err := svc.SetState(ctx, "room-1", layout.StateReady, "", "Дежурный"); err != nil {
		t.Fatalf("переход в готовность отклонён: %v", err)
	}

	// «Тренировка» наступает открытием занятия, а не переключателем.
	if _, err := svc.SetState(ctx, "room-1", layout.StateTraining, "", "Дежурный"); err == nil {
		t.Error("перевод в тренировку без открытого занятия должен отклоняться")
	}

	// Неизвестная комната.
	if _, err := svc.SetState(ctx, "room-9", layout.StatePreparing, "", "Дежурный"); err == nil {
		t.Error("перевод неизвестной комнаты должен отклоняться")
	}
}

func TestAlarmStopsEverything(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sessions := repo.NewSessions()
	// Команда оборудованию и журнал аварий.
	var alarmed, released int
	alarmCmd := func(_ context.Context, _ layout.Room, _ string, on bool) error {
		if on {
			alarmed++
		} else {
			released++
		}
		return nil
	}
	journal := repo.NewAlarms()

	// Закрытие занятия идёт тем же путём, что и кнопка «Завершить»:
	// там же останавливается видеозапись.
	finish := func(ctx context.Context, room layout.RoomID, reason string, alarm bool) error {
		sess, ok, err := sessions.Active(ctx, string(room))
		if err != nil || !ok {
			return err
		}
		sess.Finished = time.Now()
		sess.Reason = reason
		// Авария отмечается в карточке особо: по ней видно, что
		// тренировку остановила не администратор (сценарий §7).
		if alarm {
			sess.Outcome = training.OutcomeAlarm
		}
		return sessions.Save(ctx, sess)
	}

	svc := New(stubLayout{obj: object()}, repo.NewMemory(), repo.NewStates(), sessions,
		stubStreams{}, nil, nil, nil, nil, alarmCmd, journal, finish, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Открытое занятие при аварии закрывается с отметкой: результаты
	// и запись должны сохраниться, а не пропасть вместе с аварией.
	open := training.Session{
		ID: "s1", Room: "room-1", Examinee: "Иванов Пётр", Started: time.Now(),
	}
	if err := sessions.Save(ctx, open); err != nil {
		t.Fatal(err)
	}

	states, err := svc.Alarm(ctx, "сработала кнопка", "Дежурный")
	if err != nil {
		t.Fatalf("Alarm() вернул ошибку: %v", err)
	}
	if len(states) != 1 || states[0].State != layout.StateAlarm {
		t.Fatalf("после аварии состояния = %+v", states)
	}

	if _, ok, _ := sessions.Active(ctx, "room-1"); ok {
		t.Error("занятие должно быть закрыто аварией")
	}
	past, _ := sessions.Recent(ctx, "room-1", 10)
	if len(past) != 1 || !past[0].Interrupted() {
		t.Fatalf("занятие не сохранено с причиной: %+v", past)
	}
	// В карточке участника отметка именно об аварии: по сценарию §7
	// её отличают от обычного досрочного завершения.
	if got := past[0].Status(); got != "Аварийная остановка" {
		t.Errorf("статус занятия %q, ожидалась отметка об аварии", got)
	}

	// Оборудованию ушла команда перевода в безопасное состояние.
	if alarmed != 1 {
		t.Errorf("команд аварии = %d, ожидалась одна на комнату", alarmed)
	}

	// Событие записано в журнал вместе с прерванным занятием.
	events, err := svc.Alarms(ctx, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("журнал аварий = %+v, err %v", events, err)
	}
	if events[0].Reason != "сработала кнопка" || len(events[0].Sessions) != 1 {
		t.Errorf("запись журнала неполна: %+v", events[0])
	}
	if !events[0].Active() {
		t.Error("авария не снята — запись должна быть действующей")
	}

	// Из аварии — только в ожидание, и только вручную.
	if _, err := svc.SetState(ctx, "room-1", layout.StateReady, "", "Дежурный"); err == nil {
		t.Error("из аварии в готовность переходить нельзя")
	}
	if _, err := svc.SetState(ctx, "room-1", layout.StateWaiting, "причина устранена", "Дежурный"); err != nil {
		t.Errorf("возврат в ожидание отклонён: %v", err)
	}

	// При выходе из аварии оборудованию разрешается работа, а запись
	// в журнале закрывается.
	if released != 1 {
		t.Errorf("команд снятия = %d, ожидалась одна", released)
	}
	if events, _ := svc.Alarms(ctx, 10); len(events) != 1 || events[0].Active() {
		t.Errorf("авария в журнале не закрыта: %+v", events)
	}
}

func TestPreparationReachesTarget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := repo.NewMemory()

	var heatOn bool
	svc := New(stubLayout{obj: object()}, state, repo.NewStates(), repo.NewSessions(),
		stubStreams{},
		func(context.Context, layout.Room, string) error { heatOn = true; return nil },
		nil, nil, nil, nil, repo.NewAlarms(), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Холодная комната: цель ещё не достигнута.
	save(t, state, "heater-1", telemetry.Telemetry{TempC: 22})

	// Состав записывают до подготовки: без него прогрев не начинают.
	if _, err := svc.SetRoster(ctx, "room-1", "Звено 1", []string{"900315501234"}, "Дежурный"); err != nil {
		t.Fatalf("SetRoster() вернул ошибку: %v", err)
	}

	st, err := svc.StartPreparation(ctx, "room-1", "Дежурный")
	if err != nil {
		t.Fatalf("StartPreparation() вернул ошибку: %v", err)
	}
	if st.State != layout.StatePreparing || !st.Prep.Running() {
		t.Fatalf("подготовка не началась: %+v", st)
	}
	if st.Prep.TargetC != 40 {
		t.Errorf("цель = %v, ожидалось 40", st.Prep.TargetC)
	}
	if !heatOn {
		t.Error("нагрев не включён командой")
	}
	if left := st.Prep.Left(time.Now()); left < 29*time.Minute {
		t.Errorf("таймер = %s, ожидалось около 30 минут", left)
	}

	view, _, err := svc.Room(ctx, "room-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.State.Prep.Done() {
		t.Error("цель отмечена достигнутой при 22 °C")
	}
	if msg := view.State.Prep.Message(time.Now()); msg != "Идёт прогрев" {
		t.Errorf("сообщение = %q", msg)
	}

	// Прогрелись: цель отмечается, но «Готова» сама не выставляется —
	// её подтверждает администратор (сценарий §3).
	save(t, state, "heater-1", telemetry.Telemetry{TempC: 40.5})

	view, _, err = svc.Room(ctx, "room-1")
	if err != nil {
		t.Fatal(err)
	}
	if !view.State.Prep.Done() {
		t.Fatal("цель не отмечена достигнутой при 40.5 °C")
	}
	if view.State.State != layout.StatePreparing {
		t.Errorf("состояние = %q, «Готова» выставляться само не должно", view.State.State)
	}
	if msg := view.State.Prep.Message(time.Now()); msg != "Комната готова" {
		t.Errorf("сообщение = %q, ожидалось «Комната готова»", msg)
	}
}

func TestPreparationExpires(t *testing.T) {
	t.Parallel()

	now := time.Now()
	// Время вышло, цель не достигнута.
	p := layout.Preparation{TargetC: 40, Started: now.Add(-31 * time.Minute), Until: now.Add(-time.Minute)}
	if !p.Expired(now) {
		t.Error("подготовка должна считаться просроченной")
	}
	if msg := p.Message(now); msg != "Температура не достигнута" {
		t.Errorf("сообщение = %q", msg)
	}
	if left := p.Left(now); left != 0 {
		t.Errorf("таймер ушёл в минус: %s", left)
	}

	// Цель достигнута до истечения срока — просрочки нет.
	done := p
	done.Reached = now.Add(-2 * time.Minute)
	if done.Expired(now) {
		t.Error("достигнутая цель не может быть просрочена")
	}
}

func TestTrainingStartsOnlyWhenReady(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sessions := repo.NewSessions()
	svc := New(stubLayout{obj: object()}, repo.NewMemory(), repo.NewStates(), sessions,
		stubStreams{}, nil, nil, nil, nil, nil, repo.NewAlarms(), nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	// В «Ожидании» тренировку начинать нельзя: комнату не готовили.
	if err := svc.Ready(ctx, "room-1"); err == nil {
		t.Error("из «Ожидания» тренировка начинаться не должна")
	}

	if _, err := svc.SetState(ctx, "room-1", layout.StatePreparing, "", "Дежурный"); err != nil {
		t.Fatal(err)
	}
	// И в «Подготовке» тоже: готовность подтверждает администратор.
	if err := svc.Ready(ctx, "room-1"); err == nil {
		t.Error("из «Подготовки» тренировка начинаться не должна")
	}

	if _, err := svc.SetState(ctx, "room-1", layout.StateReady, "", "Дежурный"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Ready(ctx, "room-1"); err != nil {
		t.Errorf("из «Готова» тренировка должна начинаться: %v", err)
	}

	// Открытие занятия переводит комнату в «Тренировку».
	if err := sessions.Save(ctx, training.Session{
		ID: "s1", Room: "room-1", Started: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Began(ctx, "room-1", "Дежурный"); err != nil {
		t.Fatalf("перевод в тренировку: %v", err)
	}
	if st, _ := svc.State(ctx, "room-1"); st.State != layout.StateTraining {
		t.Errorf("состояние = %q, ожидалась тренировка", st.State)
	}

	// Завершение переводит в «Завершение».
	if err := svc.Ended(ctx, "room-1", "Дежурный"); err != nil {
		t.Fatalf("перевод в завершение: %v", err)
	}
	if st, _ := svc.State(ctx, "room-1"); st.State != layout.StateFinishing {
		t.Errorf("состояние = %q, ожидалось завершение", st.State)
	}
}

func TestPrepAndCodeSurviveTransitions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := New(stubLayout{obj: object()}, repo.NewMemory(), repo.NewStates(), repo.NewSessions(),
		stubStreams{}, nil, nil,
		func(context.Context, layout.Room, string, string, string) error { return nil },
		nil, nil, repo.NewAlarms(), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := svc.SetRoster(ctx, "room-1", "Звено 1", []string{"900315501234"}, "Дежурный"); err != nil {
		t.Fatalf("SetRoster() вернул ошибку: %v", err)
	}
	if _, err := svc.StartPreparation(ctx, "room-1", "Дежурный"); err != nil {
		t.Fatalf("StartPreparation() вернул ошибку: %v", err)
	}

	before, _ := svc.State(ctx, "room-1")
	if !before.Prep.Running() {
		t.Fatal("подготовка не началась")
	}

	// Переход в «Готова» не должен стирать ход подготовки и код:
	// занятие берёт код отсюда, а экран — прогрев.
	after, err := svc.SetState(ctx, "room-1", layout.StateReady, "проверено", "Дежурный")
	if err != nil {
		t.Fatalf("SetState() вернул ошибку: %v", err)
	}
	if !after.Prep.Running() {
		t.Error("сведения о подготовке потеряны при переходе в «Готова»")
	}
	if after.Code != before.Code {
		t.Errorf("код потерян при переходе: было %q, стало %q", before.Code, after.Code)
	}

	// Возврат в «Ожидание» начинает цикл заново.
	reset, err := svc.SetState(ctx, "room-1", layout.StateWaiting, "", "Дежурный")
	if err != nil {
		t.Fatal(err)
	}
	if reset.Prep.Running() || reset.Code != "" {
		t.Errorf("цикл не сброшен: %+v", reset)
	}
}

// Подготовку нельзя начать, пока не записан состав: прогрев и дым
// стоят времени и ресурса, и включать их, не зная, кто придёт, незачем.
func TestPreparationNeedsRoster(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := New(stubLayout{obj: object()}, repo.NewMemory(), repo.NewStates(), repo.NewSessions(),
		stubStreams{}, nil, nil, nil, nil, nil, repo.NewAlarms(), nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := svc.StartPreparation(ctx, "room-1", "Дежурный"); !errors.Is(err, ErrNoRoster) {
		t.Fatalf("подготовка без состава = %v, ожидалась ErrNoRoster", err)
	}

	if _, err := svc.SetRoster(ctx, "room-1", "", []string{"900315501234"}, "Дежурный"); err != nil {
		t.Fatalf("SetRoster() вернул ошибку: %v", err)
	}
	if _, err := svc.StartPreparation(ctx, "room-1", "Дежурный"); err != nil {
		t.Fatalf("подготовка с составом отклонена: %v", err)
	}

	// Возврат в ожидание начинает цикл заново: состав прошлой группы
	// следующей не принадлежит.
	if _, err := svc.SetState(ctx, "room-1", layout.StateWaiting, "отбой", "Дежурный"); err != nil {
		t.Fatalf("возврат в ожидание отклонён: %v", err)
	}
	st, err := svc.State(ctx, "room-1")
	if err != nil {
		t.Fatal(err)
	}
	if st.Roster.Set() {
		t.Errorf("состав остался после возврата в ожидание: %+v", st.Roster)
	}
}
