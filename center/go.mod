module github.com/ArnurKabdylkak/lss/center

go 1.25.10

require (
	github.com/ArnurKabdylkak/lss/telemetry v0.0.0
	github.com/go-pdf/fpdf v0.9.0
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
	go.mongodb.org/mongo-driver/v2 v2.9.1
	golang.org/x/crypto v0.53.0
	golang.org/x/sys v0.46.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.2.0 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/text v0.39.0 // indirect
)

// Контур закрытый, общего git-хостинга нет: модуль подключается с диска.
replace github.com/ArnurKabdylkak/lss/telemetry => ../telemetry
