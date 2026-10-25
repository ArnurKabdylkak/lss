# Интерфейс к сервису сбора телеметрии.
#
#   make            список целей
#   make up         поднять в контейнере
#   make smoke      сквозная проверка на отдельном стеке

SHELL := /bin/bash

IMAGE   ?= mchs/telemetryd:dev
LISTEN  ?= :5005
HTTP    ?= :8080
CSV     ?= telemetry.csv
# Адрес API для целей health/smoke; порт берётся из MCHS_HTTP_PORT.
HTTP_PORT ?= 8080
API     ?= http://localhost:$(HTTP_PORT)

.DEFAULT_GOAL := help

.PHONY: help
help: ## показать список целей
	@echo "Цели:"
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

# --- разработка без контейнера ---

.PHONY: test
test: ## прогнать тесты
	go test ./...

.PHONY: test-store
test-store: ## прогнать тесты хранилища против настоящей MongoDB
	@scripts/test-store.sh

.PHONY: db-dump
db-dump: ## выгрузить базу центра в ./backups
	@scripts/db-dump.sh

.PHONY: db-restore
db-restore: ## восстановить базу из выгрузки: make db-restore FILE=backups/...
	@scripts/db-restore.sh $(FILE)

.PHONY: vet
vet: ## статические проверки
	go vet ./...

.PHONY: fmt
fmt: ## отформатировать код
	go fmt ./...

.PHONY: build
build: ## собрать бинарь в bin/telemetryd
	go build -trimpath -o bin/telemetryd ./cmd/telemetryd

.PHONY: run
run: ## запустить локально, без контейнера
	go run ./cmd/telemetryd -listen $(LISTEN) -http $(HTTP) -csv $(CSV)

# --- контейнер ---

.PHONY: image
image: ## собрать образ
	docker build --build-context shared=../telemetry -t $(IMAGE) .

.PHONY: image-center
image-center: ## собрать образ центра (ffmpeg и панель внутри)
	docker build --build-context shared=../telemetry \
		--build-context panel-src=../panel \
		--target video -t $(IMAGE)-center .

.PHONY: up
up: ## поднять сервис в фоне
	docker compose up --build -d
	@$(MAKE) --no-print-directory health

.PHONY: down
down: ## остановить сервис, том сохранить
	docker compose down

.PHONY: destroy
destroy: ## остановить сервис и удалить том с CSV
	docker compose down -v

.PHONY: restart
restart: ## перезапустить сервис
	docker compose restart
	@$(MAKE) --no-print-directory health

.PHONY: logs
logs: ## смотреть журнал (Ctrl-C для выхода)
	docker compose logs -f

.PHONY: ps
ps: ## состояние контейнеров
	docker compose ps

.PHONY: health
health: ## дождаться готовности API
	@scripts/wait-healthy.sh $(API)

.PHONY: devices
devices: ## показать состояние устройств
	@curl -fsS $(API)/api/v1/devices | (jq . 2>/dev/null || cat)

.PHONY: csv
csv: ## показать CSV из тома
	@scripts/show-csv.sh

# --- проверка ---

.PHONY: simulate
simulate: ## слать телеметрию как плата (Ctrl-C для остановки)
	@scripts/simulate.sh

.PHONY: simulate-room
simulate-room: ## слать телеметрию всей комнаты (велосипед, лестница, груз, пушка)
	scripts/simulate-room.sh http://127.0.0.1:$(or $(MCHS_HTTP_PORT),8080)

.PHONY: simulate-room-docker
simulate-room-docker: ## то же, но в контейнере (хосту не нужен Python)
	MCHS_PASSWORD=$(MCHS_PASSWORD) docker compose --profile sim up simulator

.PHONY: smoke
smoke: ## сквозная проверка на отдельном стеке
	@scripts/smoke.sh

.PHONY: clean
clean: ## убрать артефакты сборки
	rm -rf bin
	go clean -testcache
