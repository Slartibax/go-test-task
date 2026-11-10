.PHONY: help local-up local-down dev-up dev-down fmt lint test test-integration

.DEFAULT_GOAL := help

LOCAL_COMPOSE := docker-compose.yaml
DEV_COMPOSE := docker-compose-dev.yaml
COMPOSE := docker compose --env-file .env

help: ## Показать команды
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

local-up: ## Поднять локальный compose
	$(COMPOSE) -f $(LOCAL_COMPOSE) up -d --build

local-down: ## Остановить локальный compose
	$(COMPOSE) -f $(LOCAL_COMPOSE) down

dev-up: ## Поднять dev compose
	$(COMPOSE) -f $(DEV_COMPOSE) up -d --build

dev-down: ## Остановить dev compose
	$(COMPOSE) -f $(DEV_COMPOSE) down

test: ## Юнит-тесты
	go test -race ./...

test-integration: ## Интеграционные тесты, нужен Docker
	go test -tags integration -race -count=1 ./...

fmt: ## Форматировать Go-код
	go tool -modfile=tools/go.mod golangci-lint fmt ./...

lint: ## Проверить Go-код линтером
	go tool -modfile=tools/go.mod golangci-lint run --build-tags integration ./...
