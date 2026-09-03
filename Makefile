.PHONY: help up down tests fmt logs status stand-up stand-down stand-restart stand-logs stand-reset stand-env

# Тестовый стенд поднимается только явными -p и -f. Без них docker compose
# подхватил бы docker-compose.yml и остановил боевые контейнеры, поэтому
# команды стенда вынесены в цели, а не оставлены на память запускающего.
STAND := docker compose -p vpn-bot-test -f docker-compose.test.yml --env-file .env.test

# --env-file вшит в STAND, поэтому без файла падают ВСЕ цели стенда, включая
# stand-down. Оператор, которому нечем остановить поднятый стенд, потянется к
# голому docker compose down — то есть к команде, которая убьёт прод. Поэтому
# проверка висит на каждой цели, а не только на stand-up.
stand-env:
	@test -f .env.test || { echo "Нет .env.test — скопируйте .env.test.example и заполните"; exit 1; }

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

stand-up: stand-env ## Поднять тестовый стенд (нужен заполненный .env.test)
	@# Каталог targets бот сам не создаёт, а stand-reset сносит data-test целиком.
	@mkdir -p data-test/sd_configs
	$(STAND) up -d --build

stand-down: stand-env ## Остановить тестовый стенд
	$(STAND) down

stand-restart: stand-env ## Перезапустить бота на стенде (первый проход планировщика идёт при старте)
	$(STAND) restart vpn-bot-test

stand-logs: stand-env ## Показать логи тестового стенда
	$(STAND) logs -f

stand-reset: stand-env ## Остановить стенд и стереть его базу (сбрасывает уведомления и регистрации)
	$(STAND) down
	rm -rf data-test
