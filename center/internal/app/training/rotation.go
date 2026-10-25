package training

import (
	"context"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
)

// Смена тренажёров внутри звена.
//
// Занятие в теплокамере идёт по кругу: выполнив задание на своём
// тренажёре, человек переходит на следующий, а его место занимает
// товарищ. Так звено проходит все тренажёры, а не по одному каждый.
//
// Круг двигает центр, а не оператор: смена происходит каждые несколько
// минут, и дежурный, занятый людьми в зале, успевал бы нажимать не
// всегда. За ним остаётся право поправить — ручное назначение никуда
// не делось и действует поверх.
//
// Момент смены: когда **все** занятые тренажёры отчитались о
// выполнении. Двигать круг по одному нельзя — человек пришёл бы на
// занятый тренажёр, и двое считались бы на одних показаниях.

// rotationTick — как часто проверяется готовность к смене. Задания
// меряются минутами, поэтому секунды достаточно.
const rotationTick = time.Second

// RunRotation ведёт смену тренажёров до отмены контекста.
func (s *Service) RunRotation(ctx context.Context) {
	t := time.NewTicker(rotationTick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.stepRotation(ctx)
	}
}

func (s *Service) stepRotation(ctx context.Context) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return
	}

	for _, room := range obj.Rooms {
		// Смена нужна там, где тренажёров больше одного: в дымокамере
		// звено идёт маршрут целиком, и меняться там нечем.
		if len(room.Trainers()) < 2 {
			continue
		}
		if err := s.rotate(ctx, room); err != nil {
			s.log.Error("смена тренажёров не удалась", "room", room.ID, "err", err)
		}
	}
}

// rotate сдвигает звено на один тренажёр, если все выполнили задание.
func (s *Service) rotate(ctx context.Context, room layout.Room) error {
	sess, active, err := s.active(ctx, room.ID)
	if err != nil || !active {
		return err
	}

	exercises, err := s.Exercises(ctx, room.ID)
	if err != nil {
		return err
	}

	// Занятые тренажёры в порядке раскладки: круг идёт по нему, и
	// порядок должен совпадать с тем, как тренажёры стоят в зале.
	var devices []string
	var crew []string
	for _, ex := range exercises {
		if !ex.Busy() {
			continue
		}
		if !ex.Done {
			// Кто-то ещё работает — круг не двигаем.
			return nil
		}
		devices = append(devices, ex.Device.ID)
		crew = append(crew, ex.IIN)
	}

	// Меняться не с кем: одному человеку переходить некуда, а пустой
	// зал и подавно не двигают.
	if len(crew) < 2 {
		return nil
	}

	// Сначала снимаем всех, потом рассаживаем заново. Иначе проверка
	// «один человек — один тренажёр» отвергнет переход: пока прежнее
	// назначение не закрыто, человек числится на старом месте.
	for _, dev := range devices {
		if err := s.assign(ctx, room, &sess, dev, ""); err != nil {
			return err
		}
	}
	for i, dev := range devices {
		// Сдвиг на один: кто был на первом тренажёре, идёт на второй.
		next := crew[(i+len(crew)-1)%len(crew)]
		if err := s.assign(ctx, room, &sess, dev, next); err != nil {
			return err
		}
	}

	if err := s.sessions.Save(ctx, sess); err != nil {
		return err
	}

	s.log.Info("смена тренажёров", "room", room.ID,
		"тренажёров", len(devices), "кто", crew)
	return nil
}

// Rotations — сколько раз звено сменило тренажёры в идущем занятии.
// Ноль, если занятия нет: показывать прошлые круги на пустой комнате
// незачем.
func (s *Service) Rotations(ctx context.Context, room layout.RoomID) int {
	sess, active, err := s.active(ctx, room)
	if err != nil || !active {
		return 0
	}
	return sess.Rotations()
}
