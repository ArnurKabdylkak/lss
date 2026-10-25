// Package center собирает центр целиком и ведёт его до остановки.
//
// Вынесено из команды, потому что запускают его двояко: службой на
// сервере и десктопным приложением на Windows, где рядом с центром
// живёт ещё и окно с панелью. Оба входа собирают одно и то же — иначе
// они разойдутся в поведении.
//
// Прежнее описание команды — центральный сервис сбора телеметрии.
//
// Здесь и только здесь собираются зависимости слоёв: транспорты, кодек,
// потребители, хранилище, очередь команд и прикладные сценарии.
//
//	go run ./cmd/telemetryd -http :8080 -csv telemetry.csv
package center

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/inbound/collector"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/inbound/httpapi"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/inbound/udp"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/audio"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/bcast"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/camyaml"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/ffmpeg"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/filestore"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/layoutyaml"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/mongostore"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/pdfreport"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/queue"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/repo"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/sink"
	"github.com/ArnurKabdylkak/lss/center/internal/app/auth"
	"github.com/ArnurKabdylkak/lss/center/internal/app/command"
	"github.com/ArnurKabdylkak/lss/center/internal/app/ingest"
	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/app/query"
	apprescuers "github.com/ArnurKabdylkak/lss/center/internal/app/rescuers"
	approoms "github.com/ArnurKabdylkak/lss/center/internal/app/rooms"
	apptraining "github.com/ArnurKabdylkak/lss/center/internal/app/training"
	appvideo "github.com/ArnurKabdylkak/lss/center/internal/app/video"
	"github.com/ArnurKabdylkak/lss/center/internal/config"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/telemetry"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Run поднимает центр и работает, пока не отменят контекст.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	state := repo.NewMemory()

	sinks, history, closers, err := buildStorage(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		for _, c := range closers {
			if err := c.Close(); err != nil {
				logger.Error("закрытие потребителя", "err", err)
			}
		}
	}()

	// Перезапуск центра не должен обнулять панель: состояние, записанное
	// в базу, поднимается обратно в память.
	if store, ok := history.(*sink.Mongo); ok {
		restored, err := store.Restore(ctx)
		if err != nil {
			logger.Error("восстановление состояния", "err", err)
		}
		for _, p := range restored {
			if err := state.Save(ctx, p); err != nil {
				logger.Error("восстановление состояния", "device", p.Device, "err", err)
			}
		}
		if len(restored) > 0 {
			logger.Info("состояние восстановлено", "devices", len(restored))
		}
	}

	stream := bcast.NewSSE()
	commands := command.New(queue.NewMemory(), logger)

	// Звук объекта: фоновые эффекты залов и аварийное оповещение.
	// Записи лежат у центра — у него акустика, и сборщики о звуковых
	// файлах не знают вовсе.
	sounds := audio.New(cfg.SoundsDir, cfg.SoundPlayer, logger)
	defer sounds.StopAll()
	if sounds.Enabled() {
		logger.Info("звук объекта", "каталог", cfg.SoundsDir)
	}
	ingestion := ingest.New(state, sinks, stream, logger, ingest.Options{Silence: cfg.Silence})
	reading := query.New(state, history)

	var wg sync.WaitGroup
	var httpErr error

	// Учётные записи операторов тренажёра. Без файла API остаётся
	// открытым — это режим отладки внутри контура, и о нём говорится
	// вслух, чтобы он не уехал в работу незамеченным.
	var users *auth.Service
	if cfg.UsersPath != "" {
		users, err = auth.LoadFile(cfg.UsersPath)
		if err != nil {
			return err
		}
		// Сессии операторов — туда же: перезапуск центра не должен
		// выкидывать смену из панели.
		if db, ok := mongoDatabase(history); ok {
			store, err := mongostore.NewAuthSessions(ctx, db)
			if err != nil {
				return err
			}
			if err := users.WithStore(ctx, store, logger); err != nil {
				return err
			}
		}

		logger.Info("вход по логину и паролю включён", "users", cfg.UsersPath)
	} else {
		logger.Warn("учётные записи не заданы: API открыт без пароля")
	}

	// Видеонаблюдение поднимается только когда камеры описаны:
	// объекты без камер не должны тянуть за собой ffmpeg.
	cameras, layoutRepo, err := buildVideo(cfg, logger)
	if err != nil {
		return err
	}

	// Комнаты объекта. Без раскладки панель показывает плоский
	// список устройств — так было до появления комнат.
	var roomsView *approoms.Service
	var lessons *apptraining.Service
	var cards *apprescuers.Service
	var recordRoot string

	// Объект берётся из раскладки, а не из флага: команды адресуются
	// по site_id, и расхождение означало бы, что сборщик их не заберёт.
	site := cfg.Site
	if layoutRepo != nil {
		// Состояния комнат переживают перезапуск: комната, оставшаяся
		// в аварии, не должна «забыть» об этом от перезагрузки.
		roomStates, err := buildStateStore(ctx, history, logger)
		if err != nil {
			return err
		}

		if obj, err := layoutRepo.Object(ctx); err == nil && obj.Site != "" {
			if obj.Site != site {
				logger.Info("объект взят из раскладки", "site", obj.Site, "вместо", site)
			}
			site = obj.Site
		}

		// Занятия должны пережить перезапуск: в них зачёт. Без базы
		// остаются в памяти, и об этом говорится вслух.
		sessions, err := buildSessionStore(ctx, history, logger)
		if err != nil {
			return err
		}
		// Картотека спасателей: к карточке привязана история занятий,
		// поэтому она живёт там же, где журнал.
		cardStore, err := buildCardStore(ctx, history, logger)
		if err != nil {
			return err
		}
		cards = apprescuers.New(cardStore, sessions, logger)

		// Видеозапись занятий: отдельные процессы ffmpeg параллельно
		// трансляциям. Без камер и без каталога записи не ведутся.
		var recorder port.Recorder
		if cfg.RecordRoot != "" {
			rec, err := ffmpeg.NewRecorder(ffmpeg.RecorderConfig{
				Binary:      cfg.FFmpeg,
				Root:        cfg.RecordRoot,
				MaxDuration: time.Duration(cfg.RecordHours) * time.Hour,
			}, logger)
			if err != nil {
				return err
			}
			defer rec.StopAll()
			recorder = rec
			recordRoot = rec.Root()
			logger.Info("видеозапись занятий включена", "каталог", recordRoot)
		}

		lessons = apptraining.New(layoutRepo, sessions, state, cards, recorder, history, logger)

		// Отчёты складываются рядом с роликами, на том же томе:
		// перенос ролика в папку отчёта должен быть переименованием,
		// а не копированием гигабайта.
		if cfg.RecordRoot != "" {
			lessons.WithReports(pdfreport.Writer{Root: cfg.RecordRoot, Log: logger})
		}
		// Включение нагрева при старте подготовки: команда уходит в
		// ту же очередь, что и управление из панели, и исполняет её
		// сборщик комнаты.
		// Тепловая пушка включается подготовкой, а не человеком:
		// уставки защиты берутся из раскладки объекта и уходят той же
		// подготовкой. Раньше их набирали в панели — значения,
		// которыми определяется безопасность зала, не должны зависеть
		// от того, что сегодня напечатал дежурный.
		heatOn := func(ctx context.Context, room layout.Room, author string) error {
			if _, err := commands.Issue(ctx, site, telemetry.Command{
				Device:   telemetry.DeviceID(room.Heater.Device),
				Kind:     telemetry.CmdSetLimits,
				LimitC:   room.Heater.LimitC,
				RestoreC: room.Heater.ResumeC,
				Operator: author,
			}); err != nil {
				// Уставки не ушли — нагрев не включаем: защита важнее
				// прогрева, а причина видна в журнале.
				return fmt.Errorf("уставки защиты не отправлены: %w", err)
			}

			_, err := commands.Issue(ctx, site, telemetry.Command{
				Device:   telemetry.DeviceID(room.Heater.Device),
				Kind:     telemetry.CmdHeaterOn,
				Operator: author,
			})
			return err
		}

		// Ручное управление узлом: дым добавить, прожекторы погасить,
		// вытяжку пустить раньше срока. Команда адресуется сборщику —
		// какое реле за что отвечает, знает он.
		unitCmd := func(ctx context.Context, room layout.Room,
			unit string, on bool, author string) error {

			// Звук не реле: узел «Аудиоэффекты» — это дорожка,
			// которую крутит сам центр. До сборщика команда не
			// доходит, и реле под неё на объекте нет.
			if unit == layout.UnitSound {
				if !sounds.Enabled() {
					return fmt.Errorf("звук на объекте не настроен")
				}
				if on {
					return sounds.Loop(string(room.ID), layout.UnitSound)
				}
				sounds.Stop(string(room.ID), layout.UnitSound)
				return nil
			}

			if room.Collector == "" {
				return fmt.Errorf("у комнаты %s не задан сборщик", room.ID)
			}
			state := "off"
			if on {
				state = "on"
			}
			_, err := commands.Issue(ctx, site, telemetry.Command{
				Device:   telemetry.DeviceID(room.Collector),
				Kind:     telemetry.CmdEquipment,
				Unit:     unit,
				State:    state,
				Operator: author,
			})
			return err
		}

		// Авария закрывает занятие тем же путём, что и кнопка
		// «Завершить»: иначе видеозапись осталась бы незакрытой.
		stopSession := func(ctx context.Context, room layout.RoomID,
			reason string, alarm bool) error {

			if alarm {
				_, err := lessons.FinishByAlarm(ctx, room, reason)
				return err
			}
			_, err := lessons.FinishWithReason(ctx, room, reason)
			return err
		}

		// Аварийная остановка оборудования: одна команда сборщику
		// комнаты. Он знает свою обвязку и переводит выходы в
		// безопасное состояние сам — центру не нужно знать, на каком
		// выводе какое реле.
		alarmCmd := func(ctx context.Context, room layout.Room, author string, on bool) error {
			if room.Collector == "" {
				return fmt.Errorf("у комнаты %s не задан сборщик", room.ID)
			}
			kind := telemetry.CmdAlarmReset
			if on {
				kind = telemetry.CmdAlarm
			}
			_, err := commands.Issue(ctx, site, telemetry.Command{
				Device:   telemetry.DeviceID(room.Collector),
				Kind:     kind,
				Operator: author,
			})
			return err
		}

		// Включение и выключение оборудования подготовки: одна команда
		// сборщику комнаты, состав оборудования знает он.
		prepareCmd := func(ctx context.Context, room layout.Room, author string, on bool) error {
			if room.Collector == "" {
				return fmt.Errorf("у комнаты %s не задан сборщик", room.ID)
			}
			kind := telemetry.CmdPrepareStop
			if on {
				kind = telemetry.CmdPrepare
			}
			_, err := commands.Issue(ctx, site, telemetry.Command{
				Device:   telemetry.DeviceID(room.Collector),
				Kind:     kind,
				Operator: author,
			})
			return err
		}

		// Код задания «Набор чисел» уходит на панель той же командой,
		// что и из панели оператора: плата сама сверяет ввод.
		codeCmd := func(ctx context.Context, _ layout.Room, device, code, author string) error {
			_, err := commands.Issue(ctx, site, telemetry.Command{
				Device:   telemetry.DeviceID(device),
				Kind:     telemetry.CmdSetCode,
				Code:     code,
				Operator: author,
			})
			return err
		}

		// Разовые действия оборудования: отпустить трубы механизма
		// «Завалы» и воспроизвести сообщение через акустику.
		actions := roomActions{commands: commands, site: site, sounds: sounds}

		journal, err := buildAlarmJournal(ctx, history, logger)
		if err != nil {
			return err
		}

		// Хранение файлом: занятия, картотека, состояния комнат и
		// журнал аварий переживают перезапуск без отдельной базы.
		// Включается, только когда хранилища действительно в памяти —
		// с MongoDB дублировать их незачем.
		if cfg.StatePath != "" {
			keeper := fileKeeper(cfg.StatePath, sessions, cardStore,
				roomStates, journal, logger)
			if keeper != nil {
				if err := keeper.Load(); err != nil {
					return err
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					keeper.Run(ctx)
				}()
			}
		}

		roomsView = approoms.New(layoutRepo, state, roomStates, sessions,
			cameras, heatOn, prepareCmd, codeCmd, actions, alarmCmd, journal,
			stopSession, lessons, logger)

		// Занятия и комнаты знают друг о друге: тренировка начинается
		// только в готовой комнате и переводит её в «Тренировку».
		lessons.WithRooms(roomsView)
		roomsView.WithUnits(unitCmd)

		// Очаги зажигает подготовка: команда идёт самой плате очага,
		// а не сборщику — реле и кнопка тушения висят на ней.
		roomsView.WithFires(func(ctx context.Context, room layout.Room,
			device string, on bool, author string) error {

			state := "off"
			if on {
				state = "on"
			}
			_, err := commands.Issue(ctx, site, telemetry.Command{
				Device:   telemetry.DeviceID(device),
				Kind:     telemetry.CmdFire,
				State:    state,
				Operator: author,
			})
			return err
		})

		// Подтверждение сборщика закрывает ожидание по узлу. Без
		// этого центр показывал бы желаемое: команда ушла — значит
		// включено, даже если реле не щёлкнуло.
		rooms := roomsView
		commands.Observe(func(ctx context.Context,
			cmd telemetry.Command, ack telemetry.Ack) {

			if cmd.Kind != telemetry.CmdEquipment || cmd.Unit == "" {
				return
			}
			obj, err := layoutRepo.Object(ctx)
			if err != nil {
				return
			}
			for _, room := range obj.Rooms {
				if room.Collector != string(cmd.Device) {
					continue
				}
				if err := rooms.Confirm(ctx, room.ID, cmd.Unit,
					cmd.State == "on", ack.Accepted, ack.Reason); err != nil {

					logger.Error("состояние узла не обновлено",
						"room", room.ID, "узел", cmd.Unit, "err", err)
				}
				return
			}
		})
	}
	// Смена тренажёров внутри звена: выполнив задание, человек
	// переходит на следующий тренажёр, а его место занимает товарищ.
	if lessons != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lessons.RunRotation(ctx)
		}()
	}

	// Автоматика узлов: выдержка «Завалов» и мигание прожекторов.
	// Живёт в центре, потому что это часть сценария помещения, а
	// сборщик исполняет узлы и не знает, зачем их включают.
	if roomsView != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			roomsView.RunAutomation(ctx)
		}()
	}

	if cameras != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cameras.Run(ctx); err != nil {
				logger.Error("видеонаблюдение остановлено", "err", err)
			}
		}()
	}

	if cfg.HTTPAddr != "" {
		// Команды уходят тому сборщику, к которому подключено
		// устройство: на объекте их несколько, а очередь общая.
		batches := collector.New(ingestion, commands, logger)
		batches.WithOwner(func(ctx context.Context, device string) (string, bool) {
			obj, err := layoutRepo.Object(ctx)
			if err != nil {
				return "", false
			}
			return obj.CollectorOf(device)
		})

		api := httpapi.New(httpapi.Config{
			Addr:        cfg.HTTPAddr,
			Site:        site,
			VideoRoot:   videoRoot(cfg, cameras),
			VideoPrefix: cfg.VideoPrefix,
			RecordRoot:  recordRoot,
			PanelRoot:   cfg.PanelRoot,
		}, reading, commands, batches, stream,
			users, cameras, roomsView, lessons, cards, logger)

		wg.Add(1)
		go func() {
			defer wg.Done()
			httpErr = api.Run(ctx)
		}()
	}

	// UDP-приём остаётся для отладки и объектов без сборщика. Когда он
	// выключен, центр живёт только батчами, и ждать нечего, кроме сигнала.
	var udpErr error
	if cfg.Listen != "" {
		source := udp.New(udp.Config{Listen: cfg.Listen, Buffer: cfg.Buffer}, logger)
		udpErr = ingestion.Run(ctx, source)
	} else {
		<-ctx.Done()
	}

	wg.Wait()

	if udpErr != nil {
		return udpErr
	}
	if httpErr != nil {
		return fmt.Errorf("HTTP API: %w", httpErr)
	}
	return nil
}

// buildStorage собирает потребителей и, если задана MongoDB, хранилище истории.
func buildStorage(ctx context.Context, cfg config.Config, logger *slog.Logger) (
	[]port.Sink, port.HistoryRepository, []port.Closer, error) {

	var sinks []port.Sink
	var closers []port.Closer
	var history port.HistoryRepository

	if !cfg.Quiet {
		sinks = append(sinks, sink.NewConsole(os.Stdout))
	}
	if cfg.CSVPath != "" {
		w, err := sink.NewCSV(cfg.CSVPath)
		if err != nil {
			return nil, nil, nil, err
		}
		sinks = append(sinks, w)
		closers = append(closers, w)
	}

	if cfg.MongoURI != "" {
		store, err := sink.NewMongo(ctx, sink.MongoConfig{
			URI:      cfg.MongoURI,
			Database: cfg.MongoDB,
		}, logger)
		if err != nil {
			// База — не причина терять поток: CSV и консоль работают
			// независимо от неё. Но молчать об этом нельзя.
			if !errors.Is(err, sink.ErrMongoUnavailable) {
				return nil, nil, nil, err
			}
			logger.Error("MongoDB недоступна, работаем без истории", "err", err)
		} else {
			sinks = append(sinks, store)
			closers = append(closers, store)
			history = store
		}
	}

	return sinks, history, closers, nil
}

// buildVideo собирает конвейер видеонаблюдения и, если задана
// раскладка объекта, отдаёт её вторым значением. Возвращает nil,
// когда камеры не настроены.
func buildVideo(cfg config.Config, logger *slog.Logger) (
	*appvideo.Supervisor, port.LayoutRepository, error) {

	if cfg.VideoSource() == "" {
		return nil, nil, nil
	}

	var cams port.CameraRepository
	var rooms port.LayoutRepository

	if cfg.RoomsPath != "" {
		object, err := layoutyaml.Load(cfg.RoomsPath)
		if err != nil {
			return nil, nil, err
		}
		cams, rooms = object, object
		logger.Info("раскладка объекта загружена", "file", object.Path())
	} else {
		flat, err := camyaml.Load(cfg.CamerasPath)
		if err != nil {
			return nil, nil, err
		}
		cams = flat
		logger.Info("камеры загружены", "file", flat.Path())
	}

	publisher, err := ffmpeg.NewHLS(ffmpeg.Config{
		Binary:    cfg.FFmpeg,
		Root:      cfg.VideoRoot,
		Segment:   cfg.Segment,
		Window:    cfg.VideoWindow,
		URLPrefix: cfg.VideoPrefix,
	}, logger)
	if err != nil {
		return nil, nil, err
	}

	return appvideo.New(cams, publisher, logger, appvideo.Options{}), rooms, nil
}

// videoRoot отдаёт каталог сегментов только когда видео действительно
// поднято: иначе HTTP раздавал бы пустую директорию.
func videoRoot(cfg config.Config, cameras *appvideo.Supervisor) string {
	if cameras == nil {
		return ""
	}
	return cfg.VideoRoot
}

// buildSessionStore выбирает, где держать занятия: в базе, если она
// подключена, иначе в памяти процесса.
func buildSessionStore(ctx context.Context, history port.HistoryRepository,
	logger *slog.Logger) (port.SessionRepository, error) {

	db, ok := mongoDatabase(history)
	if !ok {
		logger.Warn("занятия хранятся в памяти: без MongoDB журнал заездов теряется при перезапуске")
		return repo.NewSessions(), nil
	}

	sessions, err := mongostore.NewSessions(ctx, db)
	if err != nil {
		return nil, err
	}
	logger.Info("журнал занятий хранится в MongoDB")
	return sessions, nil
}

// mongoDatabase достаёт подключение из хранилища истории: база одна,
// и второй пул соединений к тому же серверу не нужен.
func mongoDatabase(history port.HistoryRepository) (*mongo.Database, bool) {
	store, ok := history.(*sink.Mongo)
	if !ok {
		return nil, false
	}
	return store.Database(), true
}

// buildCardStore выбирает, где держать картотеку: в базе, если она
// подключена, иначе в памяти процесса.
func buildCardStore(ctx context.Context, history port.HistoryRepository,
	logger *slog.Logger) (port.CardRepository, error) {

	db, ok := mongoDatabase(history)
	if !ok {
		logger.Warn("картотека спасателей хранится в памяти: без MongoDB она теряется при перезапуске")
		return repo.NewCards(), nil
	}

	cards, err := mongostore.NewCards(ctx, db)
	if err != nil {
		return nil, err
	}
	logger.Info("картотека спасателей хранится в MongoDB")
	return cards, nil
}

// buildStateStore выбирает, где держать состояния комнат.
func buildStateStore(ctx context.Context, history port.HistoryRepository,
	logger *slog.Logger) (port.RoomStateRepository, error) {

	db, ok := mongoDatabase(history)
	if !ok {
		logger.Warn("состояния комнат хранятся в памяти: после перезапуска все комнаты окажутся в «Ожидании»")
		return repo.NewStates(), nil
	}

	states, err := mongostore.NewStates(ctx, db)
	if err != nil {
		return nil, err
	}
	return states, nil
}

// buildAlarmJournal выбирает, где держать журнал аварий.
func buildAlarmJournal(ctx context.Context, history port.HistoryRepository,
	logger *slog.Logger) (port.AlarmJournal, error) {

	db, ok := mongoDatabase(history)
	if !ok {
		logger.Warn("журнал аварий хранится в памяти: после перезапуска он теряется")
		return repo.NewAlarms(), nil
	}

	alarms, err := mongostore.NewAlarms(ctx, db)
	if err != nil {
		return nil, err
	}
	return alarms, nil
}

// roomActions — разовые действия оборудования комнаты. Команды идут
// сборщику: механизм и акустика подключены к нему.
type roomActions struct {
	commands *command.Service
	site     string
	sounds   *audio.Player
}

func (a roomActions) Rubble(ctx context.Context, room layout.Room, author string) error {
	return a.issue(ctx, room, telemetry.Command{Kind: telemetry.CmdRubble, Operator: author})
}

// Announce воспроизводит запись в зале.
//
// Играет центр: у него акустика и записанные фразы. Команда до
// сборщика не доходит — он о звуке не знает.
func (a roomActions) Announce(_ context.Context, room layout.Room, sound, _ string) error {
	if a.sounds == nil {
		return nil
	}
	return a.sounds.Once(string(room.ID), sound)
}

func (a roomActions) issue(ctx context.Context, room layout.Room, cmd telemetry.Command) error {
	if room.Collector == "" {
		return fmt.Errorf("у комнаты %s не задан сборщик", room.ID)
	}
	cmd.Device = telemetry.DeviceID(room.Collector)

	_, err := a.commands.Issue(ctx, a.site, cmd)
	return err
}

// fileKeeper собирает хранителя, если все хранилища — в памяти.
//
// Смешивать базу и файл нельзя: часть данных оказалась бы в одном
// месте, часть в другом, и при следующем запуске они разошлись бы.
func fileKeeper(path string, sessions port.SessionRepository,
	cards port.CardRepository, states port.RoomStateRepository,
	alarms port.AlarmJournal, log *slog.Logger) *filestore.Keeper {

	memSessions, ok := sessions.(*repo.Sessions)
	if !ok {
		return nil
	}
	memCards, ok := cards.(*repo.Cards)
	if !ok {
		return nil
	}
	memStates, ok := states.(*repo.States)
	if !ok {
		return nil
	}
	memAlarms, ok := alarms.(*repo.Alarms)
	if !ok {
		return nil
	}

	log.Info("хранилище файлом", "файл", path)
	return filestore.New(path, memSessions, memCards, memStates, memAlarms, log)
}
