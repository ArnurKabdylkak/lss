package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Run поднимает сборщик целиком и работает до отмены контекста.
//
// Порядок остановки важен: сперва встаёт приём, затем выгрузка делает
// последнюю попытку, и только потом закрывается буфер.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	buf, err := OpenBuffer(cfg.Buffer.Path, cfg.Buffer.MaxRecords)
	if err != nil {
		return err
	}
	defer func() {
		if err := buf.Close(); err != nil {
			log.Error("закрытие буфера", "err", err)
		}
	}()

	if n := buf.Len(); n > 0 {
		log.Info("буфер восстановлен с диска", "records", n)
	}

	// Выходы и свой датчик — местные: надзор за живостью к ним не
	// применяется, они не «молчат».
	state := NewState(cfg.Silence, append(cfg.LocalOutputIDs(), cfg.Sensors.ID)...)
	receiver := NewReceiver(cfg, state, buf, log)

	outputs, closeOutputs := buildOutputs(cfg, log)
	defer closeOutputs()

	receiver.outputs = outputs

	// Помещение: узлы, завалы, замки и акустика. Команды помещения
	// центр адресует сборщику, а не отдельным реле.
	room := NewRoom(cfg.Room, outputs, log)

	router := NewRouter(cfg, outputs, room, receiver, state, buf, log)
	uplink := NewUplink(cfg, buf, router, log)
	tasks := NewTasks(cfg, state, buf, receiver, uplink, log)
	tasks.outputs = outputs
	tasks.router = router

	sensor := NewSensor(cfg, state, buf, log)
	sensor.outputs = outputs

	// Отказ приёмника фатален: сборщик без приёма молча делает вид,
	// что работает, а телеметрия в это время теряется безвозвратно.
	// Поэтому он останавливает весь узел, а не только свою горутину.
	ctx, fatal := context.WithCancel(ctx)
	defer fatal()

	var wg sync.WaitGroup
	var recvErr, httpErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := receiver.Run(ctx); err != nil {
			recvErr = err
			log.Error("приём телеметрии остановлен, останавливаю сборщик", "err", err)
			fatal()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		uplink.Run(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		tasks.Run(ctx, receiver.Acks())
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		sensor.Run(ctx)
	}()

	if cfg.HTTP != "" {
		api := NewAPI(cfg, state, uplink, buf, receiver, outputs, room, log)
		srv := NewServer(cfg.HTTP, api.Routes(), log)

		wg.Add(1)
		go func() {
			defer wg.Done()
			httpErr = srv.Run(ctx)
		}()
	}

	wg.Wait()

	if recvErr != nil {
		return recvErr
	}
	if httpErr != nil {
		return fmt.Errorf("локальное API: %w", httpErr)
	}
	return nil
}

// buildOutputs поднимает реле объекта.
//
// Недоступный вывод GPIO не должен мешать сбору телеметрии: сборщик
// продолжает работать, но выход переходит в режим учёта без реле, и об
// этом громко сообщается — молча «управлять» несуществующим реле
// опаснее, чем не управлять вовсе. Отказ одного выхода не отменяет
// остальные: стробоскоп не должен пропадать из-за неисправной пушки.
func buildOutputs(cfg Config, log *slog.Logger) (*Outputs, func()) {
	if !cfg.Outputs.Enabled || len(cfg.Outputs.Lines) == 0 {
		return nil, func() {}
	}

	outs := &Outputs{
		byID:    make(map[string]Output, len(cfg.Outputs.Lines)),
		localID: cfg.Sensors.ID,
	}

	for _, oc := range cfg.Outputs.Lines {
		id := cfg.OutputDeviceID(oc.ID)
		source := fmt.Sprintf("%s:%d", cfg.Outputs.Chip, oc.Line)
		activeLow, openDrain := cfg.Outputs.Polarity(oc)

		line, err := OpenGPIOOutput(GPIOOutput{
			Chip:      cfg.Outputs.Chip,
			Line:      oc.Line,
			ActiveLow: activeLow,
			OpenDrain: openDrain,
			Initial:   false, // любой выход поднимается снятым
			Consumer:  "mchs-" + oc.ID,
		})
		if err != nil {
			log.Error("реле недоступно, работаю без управления этим выходом",
				"output", id, "chip", cfg.Outputs.Chip, "line", oc.Line, "err", err)
			line = nil
		} else {
			log.Info("реле подключено", "output", id, "kind", oc.Kind,
				"chip", cfg.Outputs.Chip, "line", oc.Line,
				"active_low", activeLow, "open_drain", openDrain)
		}

		// nil-линия даёт выход в режиме учёта: состояние считается и
		// публикуется, но железа за ним нет.
		var sw Switch
		if line != nil {
			sw = line
		}

		switch oc.Kind {
		case OutputHeater:
			h := NewHeater(sw, oc.Limits(), log)
			h.id, h.source, h.line = id, source, line
			outs.list = append(outs.list, h)
			outs.byID[id] = h
			// Источники температуры перечисляются идентификаторами
			// устройств как есть: это платы объекта или свой датчик
			// сборщика, а не короткие имена выходов.
			outs.heaters = append(outs.heaters, heaterSource{h: h, from: oc.TempFrom})

			log.Info("тепловая защита выхода", "output", id,
				"limit_c", oc.LimitC, "restore_c", oc.RestoreC,
				"temp_from", oc.TempFrom)

		default:
			r := NewRelay(id, source, sw, line, log)
			outs.list = append(outs.list, r)
			outs.byID[id] = r
		}
	}

	return outs, func() {
		// Выходы гасятся при остановке: оставлять нагрев или свет
		// включёнными, когда за ними никто не следит, нельзя.
		if err := outs.Close(); err != nil {
			log.Error("закрытие выходов", "err", err)
		}
	}
}

// Run обслуживает запросы до отмены контекста, затем гасит сервер мягко.
func (s *Server) Run(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() {
		s.log.Info("локальная панель слушает", "addr", s.srv.Addr)
		errc <- s.srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutdown)
	}
}
