# Сборщик телеметрии на объекте.
SHELL := /bin/bash

CONFIG ?= config.yml

.DEFAULT_GOAL := help

.PHONY: help
help: ## показать список целей
	@echo "Цели:"
	@grep -hE '^[a-z0-9-]+:.*?## ' $(MAKEFILE_LIST) | sort \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: ## запустить сборщик
	go run ./cmd -config $(CONFIG)

.PHONY: build
build: ## собрать бинарь в bin/collector
	go build -trimpath -o bin/collector ./cmd

.PHONY: test
test: ## прогнать тесты
	go test ./...

.PHONY: vet
vet: ## статические проверки
	go vet ./...

.PHONY: clean
clean: ## убрать артефакты
	rm -rf bin buffer.jsonl buffer.jsonl.tmp
	go clean -testcache

.PHONY: check-configs
check-configs: ## проверить настройки всех трёх сборщиков без запуска
	@cd deploy && for host in room1 room2 room3; do \
		ansible-playbook site.yml -l $$host --tags config --check >/dev/null \
			&& echo "  $$host: настройка собирается" || exit 1; \
	done

.PHONY: deploy
deploy: ## перенести сборщик на объекты (HOST=room1 — только на него)
	cd deploy && ansible-playbook site.yml $(if $(HOST),-l $(HOST),) $(ANSIBLE_ARGS)

.PHONY: deploy-check
deploy-check: ## показать, что изменится на объектах, ничего не меняя
	cd deploy && ansible-playbook site.yml --check --diff $(if $(HOST),-l $(HOST),) $(ANSIBLE_ARGS)

.PHONY: build-arm64
build-arm64: ## собрать бинарь под RPi (статический, aarch64)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build -trimpath -ldflags="-s -w" -o bin/collector-arm64 ./cmd

.PHONY: card
card: build-arm64 ## подготовить карту нового сборщика (нужен sudo)
	sudo tools/provision-card.sh
