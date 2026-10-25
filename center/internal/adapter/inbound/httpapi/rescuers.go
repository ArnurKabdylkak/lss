package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/auth"
	apprescuers "github.com/ArnurKabdylkak/lss/center/internal/app/rescuers"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
)

// Карточки спасателей: завести, найти, поправить, посмотреть историю.
//
// Заводит и правит карточки администратор — это действие, а не
// наблюдение, поэтому требуется роль «оператор». Читать список вправе
// любой вошедший: без этого нельзя назначить участников на занятие.

type cardDTO struct {
	IIN      string    `json:"iin"`
	Name     string    `json:"name"`
	Unit     string    `json:"unit,omitempty"`
	Created  time.Time `json:"created"`
	Author   string    `json:"author,omitempty"`
	Archived bool      `json:"archived"`

	// Trainings — история тренировок; заполняется при запросе одной
	// карточки, в списке не передаётся.
	Trainings []sessionDTO `json:"trainings,omitempty"`
}

func toCardDTO(c rescuer.Card) cardDTO {
	return cardDTO{
		IIN:      string(c.IIN),
		Name:     string(c.Name),
		Unit:     c.Unit,
		Created:  c.Created,
		Author:   c.Author,
		Archived: c.Archived,
	}
}

type rescuersHandler struct {
	rescuers *apprescuers.Service
}

type cardRequest struct {
	IIN  string `json:"iin"`
	Name string `json:"name"`
	Unit string `json:"unit"`
}

// create заводит карточку: POST /api/v1/rescuers
func (h rescuersHandler) create(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req cardRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	card, err := h.rescuers.Create(r.Context(), req.IIN, req.Name, req.Unit, operatorName(sess, ""))
	switch {
	case errors.Is(err, rescuer.ErrDuplicate):
		// 409: карточка на этого человека уже есть, и заводить вторую
		// нельзя — история разошлась бы по двум записям.
		fail(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, rescuer.ErrNoIIN), errors.Is(err, rescuer.ErrBadIIN),
		errors.Is(err, rescuer.ErrNoName), errors.Is(err, rescuer.ErrBadName):
		fail(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, toCardDTO(card))
}

// update правит ФИО и подразделение: PUT /api/v1/rescuers/{iin}
func (h rescuersHandler) update(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var req cardRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	card, err := h.rescuers.Update(r.Context(),
		rescuer.IIN(r.PathValue("iin")), req.Name, req.Unit)
	switch {
	case errors.Is(err, rescuer.ErrNotFound):
		fail(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, rescuer.ErrNoName), errors.Is(err, rescuer.ErrBadName):
		fail(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toCardDTO(card))
}

type archiveRequest struct {
	Archived bool `json:"archived"`
}

// archive выводит карточку в архив и возвращает обратно:
// POST /api/v1/rescuers/{iin}/archive
func (h rescuersHandler) archive(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var req archiveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	card, err := h.rescuers.Archive(r.Context(), rescuer.IIN(r.PathValue("iin")), req.Archived)
	switch {
	case errors.Is(err, rescuer.ErrNotFound):
		fail(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toCardDTO(card))
}

// list возвращает картотеку: GET /api/v1/rescuers?q=&archived=1
func (h rescuersHandler) list(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	cards, err := h.rescuers.List(r.Context(),
		r.URL.Query().Get("q"), r.URL.Query().Get("archived") == "1")
	if err != nil {
		fail(w, http.StatusInternalServerError, "не удалось прочитать картотеку")
		return
	}

	out := make([]cardDTO, 0, len(cards))
	for _, c := range cards {
		out = append(out, toCardDTO(c))
	}
	writeJSON(w, http.StatusOK, out)
}

// get возвращает карточку с историей тренировок:
// GET /api/v1/rescuers/{iin}?limit=
func (h rescuersHandler) get(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	full, ok, err := h.rescuers.WithHistory(r.Context(),
		rescuer.IIN(r.PathValue("iin")), parseInt(r.URL.Query().Get("limit"), 0))
	if err != nil {
		fail(w, http.StatusInternalServerError, "не удалось прочитать карточку")
		return
	}
	if !ok {
		fail(w, http.StatusNotFound, rescuer.ErrNotFound.Error())
		return
	}

	dto := toCardDTO(full.Card)
	dto.Trainings = make([]sessionDTO, 0, len(full.Trainings))
	for _, s := range full.Trainings {
		dto.Trainings = append(dto.Trainings, toSessionDTO(s))
	}
	writeJSON(w, http.StatusOK, dto)
}
