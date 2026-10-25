package httpapi

import (
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/auth"
	appvideo "github.com/ArnurKabdylkak/lss/center/internal/app/video"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
)

// Видеонаблюдение для панели: список камер с состоянием и раздача
// самих трансляций.
//
// Плейлисты и сегменты отдаются тем же сервисом, что и API: у панели
// один адрес, и не нужно отдельно настраивать доступ к видео.

// cameraDTO — камера в том виде, в каком её ждёт панель.
//
// Адреса камеры с паролем здесь нет и быть не может: панель получает
// только ссылку на трансляцию.
type cameraDTO struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Site     string    `json:"site,omitempty"`
	Location string    `json:"location,omitempty"`
	Status   string    `json:"status"`
	Live     bool      `json:"live"`
	Since    time.Time `json:"since"`
	Restarts int       `json:"restarts"`
	Error    string    `json:"error,omitempty"`
	Playlist string    `json:"playlist,omitempty"`
	Audio    bool      `json:"audio"`
}

type videoHandler struct {
	sup *appvideo.Supervisor
}

func (v videoHandler) toDTO(st video.State) cameraDTO {
	dto := cameraDTO{
		ID:       string(st.Camera.ID),
		Name:     st.Camera.Title(),
		Site:     st.Camera.Site,
		Location: st.Camera.Location,
		Status:   string(st.Status),
		Live:     st.Live(),
		Since:    st.Since,
		Restarts: st.Restarts,
		Error:    st.LastError,
		Audio:    st.Camera.Audio,
	}
	// Ссылку отдаём только когда смотреть действительно есть что:
	// иначе плеер панели будет биться о несуществующий плейлист.
	if st.Live() {
		dto.Playlist = v.sup.PlaylistURL(st.Camera.ID)
	}
	return dto
}

func (v videoHandler) list(w http.ResponseWriter, _ *http.Request, _ auth.Session) {
	states := v.sup.States()

	out := make([]cameraDTO, 0, len(states))
	for _, st := range states {
		out = append(out, v.toDTO(st))
	}
	writeJSON(w, http.StatusOK, out)
}

func (v videoHandler) get(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	st, ok := v.sup.State(video.CameraID(r.PathValue("id")))
	if !ok {
		fail(w, http.StatusNotFound, "камера неизвестна")
		return
	}
	writeJSON(w, http.StatusOK, v.toDTO(st))
}

// hlsFiles раздаёт плейлисты и сегменты из каталога трансляций.
//
// Плейлист кешировать нельзя: он переписывается каждые несколько
// секунд, и закешированный означает замерший эфир. Сегменты, наоборот,
// неизменяемы — их кеш снимает основную нагрузку.
func hlsFiles(root, prefix string) http.Handler {
	files := http.FileServer(http.Dir(root))

	return http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.ToLower(path.Ext(r.URL.Path)) {
		case ".m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "no-store")
		case ".ts":
			w.Header().Set("Content-Type", "video/mp2t")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			// В каталоге трансляций больше ничего не лежит; отдавать
			// посторонние файлы оттуда незачем.
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}))
}

// recordFiles раздаёт видеозаписи занятий и PDF-отчёты.
//
// Ролики неизменяемы: записанное занятие не переписывается, и кеш
// браузера здесь работает в полную силу.
func recordFiles(root, prefix string) http.Handler {
	files := http.FileServer(http.Dir(root))

	return http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// В хранилище лежат ролики и PDF-отчёты, больше ничего
		// отдавать не нужно: каталог не для обхода.
		switch strings.ToLower(path.Ext(r.URL.Path)) {
		case ".mp4":
			w.Header().Set("Content-Type", "video/mp4")
		case ".pdf":
			w.Header().Set("Content-Type", "application/pdf")
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=86400")
		files.ServeHTTP(w, r)
	}))
}
