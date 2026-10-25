module github.com/ArnurKabdylkak/lss/collector

go 1.25.10

require gopkg.in/yaml.v3 v3.0.1

require github.com/ArnurKabdylkak/lss/telemetry v0.0.0-00010101000000-000000000000

// Контур закрытый, общего git-хостинга нет: модуль подключается с диска.
replace github.com/ArnurKabdylkak/lss/telemetry => ../telemetry
