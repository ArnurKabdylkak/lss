# Пульт управления тренажёрным комплексом.
SHELL := /bin/bash

CONFIG ?= pult.yml

.DEFAULT_GOAL := help

.PHONY: help
help: ## показать список целей
	@echo "Цели:"
	@grep -hE '^[a-z0-9-]+:.*?## ' $(MAKEFILE_LIST) | sort \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: ## запустить пульт (нужна шина I2C)
	go run ./cmd/pultd -config $(CONFIG)

.PHONY: build
build: ## собрать бинарь в bin/pultd
	go build -trimpath -o bin/pultd ./cmd/pultd

.PHONY: build-arm64
build-arm64: ## собрать бинарь под RPi (статический, aarch64)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build -trimpath -ldflags="-s -w" -o bin/pultd-arm64 ./cmd/pultd

.PHONY: test
test: ## прогнать тесты
	go test ./...

.PHONY: vet
vet: ## статические проверки
	go vet ./...

.PHONY: deploy
deploy: ## перенести пульт на устройство (нужны PULT_PASS и MCHS_ALARM_TOKEN)
	cd deploy && ansible-playbook site.yml $(ANSIBLE_ARGS)

.PHONY: deploy-check
deploy-check: ## показать, что изменится, ничего не меняя
	cd deploy && ansible-playbook site.yml --check --diff $(ANSIBLE_ARGS)

.PHONY: clean
clean: ## убрать артефакты
	rm -rf bin
	go clean -testcache
