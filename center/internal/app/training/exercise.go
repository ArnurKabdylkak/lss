package training

import (
	"context"
	"fmt"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

// Упражнения: кто что делает на тренажёре прямо сейчас.
//
// Показатели считаются как прирост от момента назначения: счётчики на
// платах накопительные и между заездами не обнуляются. Поэтому они
// закрепляются за тем, кто назначен сейчас, а результат предыдущего
// остаётся при нём (сценарий §5).

// Exercise — упражнение одного спасателя на одном тренажёре.
type Exercise struct {
	Device layout.Device
	// IIN — чьи показатели. Пусто — тренажёр свободен.
	IIN string

	Since   time.Time
	Elapsed time.Duration

	// Progress — сделанное за упражнение: прирост счётчиков.
	Progress training.Counters
	Target   training.Target
	// Left — сколько осталось до конца отведённого срока.
	Left time.Duration
	Done bool
}

// Busy сообщает, что на тренажёре кто-то есть.
func (e Exercise) Busy() bool { return e.IIN != "" }

// Exercises возвращает упражнения комнаты — по одному на тренажёр.
//
// Свободные тренажёры тоже попадают в список: экран показывает их
// пустыми, а не прячет, иначе непонятно, чего не хватает.
func (s *Service) Exercises(ctx context.Context, id layout.RoomID) ([]Exercise, error) {
	room, ok, err := s.room(ctx, id)
	if err != nil || !ok {
		return nil, err
	}

	sess, active, err := s.active(ctx, id)
	if err != nil {
		return nil, err
	}

	trainers := room.Trainers()
	out := make([]Exercise, 0, len(trainers))

	for _, dev := range trainers {
		ex := Exercise{Device: dev}
		if active {
			ex.Target = sess.Target(dev.ID)
		}

		if active {
			for _, a := range sess.Assignments {
				if a.Device != dev.ID || !a.Running() {
					continue
				}
				now, err := s.deviceCounters(ctx, dev)
				if err != nil {
					return nil, err
				}

				ex.IIN = a.IIN
				ex.Since = a.Since
				ex.Elapsed = time.Since(a.Since)
				ex.Progress = s.progress(dev, now.Sub(a.AtStart))
				ex.Done = ex.Target.Done(ex.Progress, ex.Elapsed)
				ex.Left = ex.Target.Left(ex.Elapsed)
				break
			}
		}

		out = append(out, ex)
	}
	return out, nil
}

// progress приводит прирост к виду, в котором его показывают: этажи
// лестницы считаются по правилу из настроек тренажёра.
func (s *Service) progress(dev layout.Device, c training.Counters) training.Counters {
	if dev.Kind == layout.KindStairs {
		c.Floors = dev.Floors(c.Steps, c.Floors)
	}
	return c
}

// SetTarget задаёт задание тренажёру.
//
// Цель относится к тренажёру, а не к человеку: сменившийся спасатель
// выполняет то же задание.
func (s *Service) SetTarget(ctx context.Context, id layout.RoomID,
	device string, target training.Target) (training.Session, error) {

	room, ok, err := s.room(ctx, id)
	if err != nil {
		return training.Session{}, err
	}
	if !ok {
		return training.Session{}, fmt.Errorf("комната %s неизвестна", id)
	}

	dev, ok := room.Device(device)
	if !ok || !dev.Kind.Trainer() {
		return training.Session{}, fmt.Errorf("%w: в комнате «%s» нет тренажёра %s",
			training.ErrNotAssigned, room.Title(), device)
	}

	sess, ok, err := s.active(ctx, id)
	if err != nil {
		return training.Session{}, err
	}
	if !ok {
		return training.Session{}, training.ErrNoSession
	}

	// Велоэргометр и лестница зачитываются по времени: задание в
	// подъёмах там бессмысленно, и молча принять его нельзя.
	if target.Reps > 0 && dev.Kind != layout.KindLoad {
		return training.Session{}, fmt.Errorf(
			"%w: «%s» зачитывается по времени, а не по подъёмам",
			training.ErrNotAssigned, dev.Title())
	}

	if sess.Targets == nil {
		sess.Targets = make(map[string]training.Target, len(room.Trainers()))
	}
	if target.Set() {
		sess.Targets[device] = target
	} else {
		// Пустое задание снимает цель: упражнение идёт без зачёта.
		delete(sess.Targets, device)
	}

	if err := s.sessions.Save(ctx, sess); err != nil {
		return training.Session{}, err
	}

	s.log.Info("задание тренажёру", "room", id, "device", device,
		"срок", target.Duration, "подъёмов", target.Reps)
	return sess, nil
}
