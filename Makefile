.PHONY: help up down tests fmt logs status stand-up stand-down stand-logs stand-reset

# Тестовый стенд поднимается только явными -p и -f. Без них docker compose
# подхватил бы docker-compose.yml и остановил боевые контейнеры, поэтому
# команды стенда вынесены в цели, а не оставлены на память запускающего.
STAND := docker compose -p vpn-bot-test -f docker-compose.test.yml --env-file .env.test

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

stand-up: ## Поднять тестовый стенд (нужен заполненный .env.test)
	@test -f .env.test || { echo "Нет .env.test — скопируйте .env.test.example и заполните"; exit 1; }
	$(STAND) up -d --build

stand-down: ## Остановить тестовый стенд
	$(STAND) down

stand-logs: ## Показать логи тестового стенда
	$(STAND) logs -f

stand-reset: ## Остановить стенд и стереть его базу (сбрасывает уведомления и регистрации)
	$(STAND) down
	rm -rf data-test
