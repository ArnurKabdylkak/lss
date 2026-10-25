package layout

import (
	"errors"
	"testing"
)

func TestStateTransitions(t *testing.T) {
	t.Parallel()

	good := [][2]State{
		{StateWaiting, StatePreparing},
		{StatePreparing, StateReady},
		{StatePreparing, StateWaiting},
		{StateReady, StateTraining},
		{StateReady, StatePreparing},
		{StateTraining, StateFinishing},
		{StateFinishing, StateWaiting},
		{StateFinishing, StatePreparing},
		{StateAlarm, StateWaiting},
	}
	for _, p := range good {
		if err := p[0].Move(p[1]); err != nil {
			t.Errorf("переход %s → %s отклонён: %v", p[0], p[1], err)
		}
	}

	bad := [][2]State{
		// Тренировка не начинается из ожидания: комнату сначала
		// готовят, и готовность подтверждает администратор.
		{StateWaiting, StateTraining},
		{StateWaiting, StateReady},
		{StatePreparing, StateTraining},
		{StateTraining, StateWaiting},
		// Из аварии — только в ожидание, вручную.
		{StateAlarm, StateTraining},
		{StateAlarm, StateReady},
	}
	for _, p := range bad {
		if err := p[0].Move(p[1]); !errors.Is(err, ErrBadTransition) {
			t.Errorf("переход %s → %s разрешён, а не должен", p[0], p[1])
		}
	}
}

func TestAlarmFromAnywhere(t *testing.T) {
	t.Parallel()

	// Авария объявляется из любого состояния: она случается
	// независимо от того, чем комната занята.
	for _, s := range []State{StateWaiting, StatePreparing, StateReady, StateTraining, StateFinishing} {
		if err := s.Move(StateAlarm); err != nil {
			t.Errorf("авария из %s отклонена: %v", s, err)
		}
	}
	if err := StateAlarm.Move(StateAlarm); err != nil {
		t.Errorf("повторная авария должна проходить молча: %v", err)
	}
}

func TestStateHelpers(t *testing.T) {
	t.Parallel()

	if !StateTraining.Busy() || StateWaiting.Busy() || StateAlarm.Busy() {
		t.Error("занятость комнаты определена неверно")
	}
	if StateReady.Title() != "Готова" || StateAlarm.Title() != "Авария" {
		t.Error("названия состояний не совпадают со сценарием")
	}
	if State("unknown").Known() {
		t.Error("неизвестное состояние принято")
	}
	if err := StateWaiting.Move("unknown"); !errors.Is(err, ErrBadTransition) {
		t.Error("переход в неизвестное состояние должен отклоняться")
	}
}
