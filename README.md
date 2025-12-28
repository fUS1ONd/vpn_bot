# VPN Bot - Telegram бот для управления VPN подписками

Telegram бот для управления VPN-клиентами через 3X-UI панели с поддержкой двух серверов (Россия и Европа).

## Возможности

- Создание VPN-клиентов через Telegram команды
- Автоматическая генерация VLESS ссылок
- Web-сервер для подписок с автоматическим обновлением
- Отслеживание трафика пользователей
- Поддержка двух серверов (с лимитом и безлимитный)
- База данных SQLite для хранения пользователей

## Быстрый старт с Docker

### 1. Клонируйте репозиторий
```bash
git clone <your-repo-url>
cd vpn_bot
```

### 2. Создайте .env файл
```bash
cp .env.example .env
```

### 3. Настройте переменные окружения
Отредактируйте файл `.env` и заполните все необходимые параметры:
- `BOT_TOKEN` - токен вашего Telegram бота (получить у @BotFather)
- `ADMIN_ID` - ваш Telegram ID (узнать у @userinfobot)
- Настройки серверов A и B (IP адреса, учетные данные, ключи)

### 4. Создайте директорию для данных
```bash
mkdir -p data
```

### 5. Запустите бота
```bash
docker compose up -d
```

### 6. Проверьте логи
```bash
docker compose logs -f
```

## Управление

### Остановить бота
```bash
docker compose down
```

### Перезапустить бота
```bash
docker compose restart
```

### Пересобрать и запустить
```bash
docker compose up -d --build
```

### Просмотр логов
```bash
docker compose logs -f vpn-bot
```

## Управление через Makefile (рекомендуется)

Для упрощения работы создан Makefile с готовыми командами:

```bash
make help          # Показать все доступные команды
make up            # Запустить бота
make down          # Остановить бота
make restart       # Перезапустить бота
make logs          # Показать логи
make build         # Собрать Docker образ
make rebuild       # Пересобрать и перезапустить
make status        # Показать статус контейнеров
make shell         # Войти в контейнер бота
make db-ui         # Запустить SQLite Web UI
make db-ui-stop    # Остановить SQLite Web UI
make backup        # Создать бэкап БД
make volume-info   # Информация о volume
make clean         # Удалить все контейнеры и данные
```

## Работа с базой данных

База данных SQLite хранится в Docker volume `vpn_data`. Это обеспечивает сохранность данных даже при пересоздании контейнеров.

### SQLite Web UI

Для удобного просмотра и управления базой данных предусмотрен веб-интерфейс:

#### Запуск SQLite Web UI
```bash
docker compose --profile tools up -d
```

После запуска интерфейс будет доступен по адресу: http://localhost:8080

#### Остановить SQLite Web UI
```bash
docker compose --profile tools down
```

### Работа с volume

#### Посмотреть информацию о volume
```bash
docker volume inspect vpn_bot_vpn_data
```

#### Сделать бэкап базы данных
```bash
# Создать директорию для бэкапов
mkdir -p backups

# Скопировать БД из volume в локальную директорию
docker run --rm -v vpn_bot_vpn_data:/data -v $(pwd)/backups:/backup alpine cp /data/users.db /backup/users_$(date +%Y%m%d_%H%M%S).db
```

#### Восстановить базу данных из бэкапа
```bash
# Остановить бота
docker compose down

# Восстановить БД из бэкапа
docker run --rm -v vpn_bot_vpn_data:/data -v $(pwd)/backups:/backup alpine cp /backup/users_backup.db /data/users.db

# Запустить бота
docker compose up -d
```

#### Очистить все данные (ОСТОРОЖНО!)
```bash
# Остановить все контейнеры
docker compose down

# Удалить volume с данными
docker volume rm vpn_bot_vpn_data

# Запустить заново (создастся новая пустая БД)
docker compose up -d
```

## Команды бота

- `/start` - Проверка работоспособности бота
- `/create <имя>` - Создать нового VPN-клиента

## Структура проекта

```
vpn_bot/
├── bot.py              # Основной файл бота
├── requirements.txt    # Python зависимости
├── Dockerfile          # Docker образ
├── docker compose.yml  # Docker Compose конфигурация
├── .env.example        # Пример файла с переменными окружения
├── .gitignore          # Игнорируемые файлы
├── data/               # Директория для базы данных (создается автоматически)
│   └── users.db        # SQLite база данных
└── README.md           # Этот файл
```

## Переменные окружения

### Обязательные
- `BOT_TOKEN` - токен Telegram бота
- `ADMIN_ID` - ID администратора
- `SERVER_A_*` - настройки первого сервера
- `SERVER_B_*` - настройки второго сервера

### Опциональные
- `SUB_PORT` - порт для web-сервера подписок (по умолчанию 8000)
- `DB_PATH` - путь к файлу базы данных (по умолчанию /app/data/users.db)

## Безопасность

- Никогда не коммитьте файл `.env` в git
- Храните секретные ключи в безопасности
- Убедитесь, что порт подписок (8000) открыт в файрволе
- База данных автоматически сохраняется в volume `./data`

## Обновление

1. Остановите бота:
```bash
docker compose down
```

2. Обновите код:
```bash
git pull
```

3. Пересоберите и запустите:
```bash
docker compose up -d --build
```


## Требования

- Docker
- Docker Compose
- Открытый порт 8000 для веб-сервера подписок

## Поддержка

При возникновении проблем проверьте:
1. Корректность настроек в `.env`
2. Доступность 3X-UI панелей
3. Логи бота: `docker compose logs -f`

## Makefile команды

Для упрощения работы с проектом создан Makefile:

```bash
make help          # Показать все доступные команды
make up            # Запустить бота
make down          # Остановить бота
make restart       # Перезапустить бота
make logs          # Показать логи
make build         # Собрать Docker образ
make rebuild       # Пересобрать и перезапустить
make status        # Показать статус контейнеров
make shell         # Войти в контейнер бота
make db-ui         # Запустить SQLite Web UI на :8080
make db-ui-stop    # Остановить SQLite Web UI
make backup        # Создать бэкап БД в backups/
make volume-info   # Информация о Docker volume
make clean         # Удалить все контейнеры и данные (ОСТОРОЖНО!)
```

Примеры использования:
```bash
# Запустить проект
make up

# Посмотреть логи
make logs

# Сделать бэкап базы
make backup

# Открыть веб-интерфейс для просмотра БД
make db-ui
```
