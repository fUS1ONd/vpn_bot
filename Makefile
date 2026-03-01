.PHONY: help up down tests fmt logs status 

help: ## Показать справку
	@echo "Доступные команды:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

up: ## Запустить бота
	docker compose up -d --build

down: ## Остановить бота
	docker compose down

tests: ## Запустить тесты
	go test ./...

fmt: ## Проверить код на стиль и отформатировать его
	go vet ./...
	go fmt ./...

logs: ## Показать логи бота
	docker compose logs -f vpn-bot

status: ## Показать статус контейнеров
	docker compose ps
