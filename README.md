# LSS — тренажёрный комплекс МЧС

Общий репозиторий всего комплекса: от прошивки плат до панели оператора.

```
STM32 ──UDP 5005──▶ сборщик (RPi) ──HTTP──▶ центр ──▶ панель
                        ▲                     ▲
                        └──── пульт (RPi) ────┘
```

| Каталог      | Что это                                                     | Модуль Go                                        |
|--------------|-------------------------------------------------------------|--------------------------------------------------|
| `center/`    | Центр: приём телеметрии, сценарии комнат, видео, звук, журнал | `github.com/ArnurKabdylkak/lss/center`           |
| `panel/`     | Панель оператора (React)                                    | —                                                |
| `collector/` | Сборщик на Raspberry Pi, по одному на помещение             | `github.com/ArnurKabdylkak/lss/collector`        |
| `pult/`      | Пульт управления на Raspberry Pi с MCP23017                 | `github.com/ArnurKabdylkak/lss/pult`             |
| `telemetry/` | Общий контракт: телеметрия, команды, подтверждения          | `github.com/ArnurKabdylkak/lss/telemetry`        |
| `firmware/`  | Прошивки STM32 (PlatformIO + libopencm3): стойка кода, очаги, молот | —                                         |

Модули Go связаны через `go.work` в корне: правка контракта в
`telemetry/` сразу видна центру и сборщику, без публикации версий.

## Сборка

```bash
go build ./center/... ./collector/... ./pult/... ./telemetry/...
```

Центр с панелью в Docker — из `center/` (`docker compose up -d --build`).
Сборщики и пульт выкладываются Ansible из `collector/deploy` и `pult/deploy`,
прошивки — PlatformIO из `firmware/`. Подробности — в README каждого каталога.

## Секреты

В репозиторий не пишутся: пароль камер живёт в `center/.env`, пароль пульта
и токен прямой аварии — в переменных окружения при выкладке
(`PULT_PASS`, `MCHS_ALARM_TOKEN`), учётные записи — в `center/users.json`.
