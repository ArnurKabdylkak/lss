package ffmpeg

import "testing"

func TestReason(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Connection to tcp://192.168.1.64:554 failed: Connection refused": "камера не отвечает: соединение отклонено",
		"[tcp] No route to host":                          "камера недоступна по сети",
		"Connection timed out":                            "камера не ответила вовремя",
		"[rtsp] method DESCRIBE failed: 401 Unauthorized": "камера отклонила логин или пароль",
		"Server returned 404 Not Found":                   "на камере нет такого потока",
		"Invalid data found when processing input":        "поток с камеры не удалось разобрать",
		"": "видеоконвейер остановился, подробности в журнале сервиса",
	}

	for stderr, want := range tests {
		if got := reason(stderr); got != want {
			t.Errorf("reason(%q) = %q, ожидалось %q", stderr, got, want)
		}
	}
}
