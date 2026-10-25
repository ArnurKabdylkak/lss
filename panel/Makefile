# Панель оператора.
SHELL := /bin/bash

.DEFAULT_GOAL := help

.PHONY: help
help: ## показать список целей
	@echo "Цели:"
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | sort \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

.PHONY: install
install: ## поставить зависимости
	npm install --no-audit --no-fund

.PHONY: dev
dev: ## запустить в разработке (прокси на центр localhost:8080)
	npm run dev

.PHONY: build
build: ## собрать статику в dist/
	npm run build

.PHONY: check-offline
check-offline: build ## убедиться, что в бандле нет внешних загрузок
	@echo "Загружаемые ресурсы:"
	@grep -oE '(src|href)="[^"]*"' dist/index.html
	@if grep -qoE '(@import|url\()[^;)]*https?://' dist/assets/*.css; then \
		echo "ОШИБКА: в CSS есть внешние ссылки" >&2; exit 1; fi
	@if grep -qoE 'fonts\.(googleapis|gstatic)' dist/assets/*; then \
		echo "ОШИБКА: подключены внешние шрифты" >&2; exit 1; fi
	@echo "внешних загрузок нет — годится для закрытого контура"

.PHONY: clean
clean: ## убрать сборку
	rm -rf dist
