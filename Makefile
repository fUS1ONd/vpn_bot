.PHONY: help build up down restart logs clean backup db-ui

help: ## Показать справку
	@echo "Доступные команды:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## Собрать Docker образ
	docker compose build

up: ## Запустить бота
	docker compose up -d --build

down: ## Остановить бота
	docker compose down

restart: ## Перезапустить бота
	docker compose restart

logs: ## Показать логи бота
	docker compose logs -f vpn-bot

clean: ## Остановить и удалить все контейнеры и volumes
	docker compose down -v
	docker volume prune -f

backup: ## Создать бэкап базы данных
	@mkdir -p backups
	@docker run --rm -v vpn_bot_vpn_data:/data -v $(PWD)/backups:/backup alpine cp /data/users.db /backup/users_$$(date +%Y%m%d_%H%M%S).db
	@echo "Бэкап создан в директории backups/"

db-ui: ## Запустить SQLite Web UI на http://localhost:8080
	docker compose --profile tools up -d sqlite-web
	@echo "SQLite Web UI доступен по адресу: http://localhost:8080"

db-ui-stop: ## Остановить SQLite Web UI
	docker compose --profile tools stop sqlite-web

rebuild: ## Пересобрать и перезапустить бота
	docker compose up -d --build

status: ## Показать статус контейнеров
	docker compose ps

shell: ## Войти в контейнер бота
	docker compose exec vpn-bot /bin/sh

volume-info: ## Показать информацию о volume с данными
	docker volume inspect vpn_bot_vpn_data
