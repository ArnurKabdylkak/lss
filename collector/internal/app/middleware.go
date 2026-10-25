package app

import (
	"log/slog"
	"net/http"
	"time"
)

// recovering не даёт панике в обработчике уронить сборщик.
//
// Узел стоит на объекте без присмотра: перезапуск процесса из-за ошибки
// в панели означал бы разрыв приёма телеметрии.
func recovering(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error("паника в обработчике", "path", r.URL.Path, "panic", v)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// logging пишет запросы в журнал на уровне отладки: панель опрашивает
// API постоянно, и на уровне INFO это заглушило бы всё остальное.
func logging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}

		next.ServeHTTP(rec, r)

		log.Debug("запрос", "method", r.Method, "path", r.URL.Path,
			"code", rec.code, "took", time.Since(started).Round(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}
