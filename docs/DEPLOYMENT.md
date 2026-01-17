# VPN Bot - Инструкция по развертыванию на VPS

Этот документ содержит пошаговую инструкцию по развертыванию VPN Bot на Linux VPS.

## Предварительные требования

1. **VPS с Linux** (Ubuntu 20.04+, Debian 10+, CentOS 8+)
2. **Установленный Docker и Docker Compose**
3. **Доступ по SSH к VPS**
4. **Три панели 3X-UI** со своими учётными данными
5. **Telegram Bot Token** (от @BotFather)
6. **Ваш Telegram ID** (от @userinfobot)

## Шаг 1: Подготовка VPS

### Установка Docker (если ещё не установлен)

```bash
# Обновить список пакетов
sudo apt update

# Установить зависимости
sudo apt install -y apt-transport-https ca-certificates curl gnupg lsb-release

# Добавить GPG ключ Docker
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg

# Добавить репозиторий Docker
echo "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

# Обновить список пакетов
sudo apt update

# Установить Docker и Docker Compose
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# Проверить установку
docker --version
docker compose version
```

### Добавить пользователя в группу docker (опционально, для запуска без sudo)

```bash
sudo usermod -aG docker $USER
# Выполнить вход заново или выполнить: newgrp docker
```

## Шаг 2: Клонирование репозитория

```bash
# Перейти в директорию проектов
cd ~/projects  # или другая директория по вашему выбору

# Клонировать репозиторий
git clone https://github.com/fus1ond/vpn_bot.git
cd vpn_bot
```

**Важно:** Путь по умолчанию в deploy.yml - `~/vpn_bot`. Если вы используете другой путь, отредактируйте `.github/workflows/deploy.yml`.

## Шаг 3: Подготовка конфигурации

### Создание .env файла

```bash
# Скопировать шаблон
cp .env.example .env

# Открыть в редакторе
nano .env  # или vi, или любой другой редактор
```

### Заполнение .env файла

Отредактируйте все необходимые переменные:

```env
# Telegram
BOT_TOKEN=your_bot_token_from_botfather
ADMIN_ID=your_telegram_id

# Подписки
SUBSCRIPTION_HOST=your.domain.com  # или IP адрес VPS
SUB_PORT=8000

# Server A (Россия/Каскад)
SERVER_A_BASE_URL=https://panel-a.your-domain.com:2053
SERVER_A_WEB_PATH=/xxui  # или /admin, или другой путь
SERVER_A_USERNAME=admin
SERVER_A_PASSWORD=password
SERVER_A_INBOUND_ID=1
SERVER_A_LIMIT_BYTES=32212254720  # 30GB
SERVER_A_PUBLIC_KEY=your_public_key
SERVER_A_SNI=music.yandex.ru
SERVER_A_SID=your_sid

# Server B (Европа/Прямой)
SERVER_B_BASE_URL=https://panel-b.your-domain.com:2053
SERVER_B_WEB_PATH=/xxui
SERVER_B_USERNAME=admin
SERVER_B_PASSWORD=password
SERVER_B_INBOUND_ID=1
SERVER_B_LIMIT_BYTES=0  # Безлимит
SERVER_B_PUBLIC_KEY=your_public_key
SERVER_B_SNI=google.com
SERVER_B_SID=your_sid

# Server C
SERVER_C_BASE_URL=https://panel-c.your-domain.com:2053
SERVER_C_WEB_PATH=/xxui
SERVER_C_USERNAME=admin
SERVER_C_PASSWORD=password
SERVER_C_INBOUND_ID=1
SERVER_C_LIMIT_BYTES=0  # Безлимит
SERVER_C_PUBLIC_KEY=your_public_key
SERVER_C_SNI=google.com
SERVER_C_SID=your_sid
```

**Безопасность:** Никогда не коммитьте `.env` файл! Он содержит чувствительные данные.

## Шаг 4: Запуск контейнеров

```bash
# Для локальной разработки (с build из исходников)
docker compose up -d --build

# Для продакшена (с образом из Docker Hub, если настроен CI/CD)
docker compose pull vpn-bot
docker compose up -d
```

### Проверка статуса

```bash
# Просмотр статуса контейнеров
docker compose ps

# Просмотр логов
docker compose logs -f vpn-bot

# Проверка, что бот запустился без ошибок
docker compose logs --tail=50 vpn-bot
```

Если видите ошибки подключения к 3X-UI панелям:
- Проверьте корректность данных в .env
- Убедитесь, что панели доступны по HTTPS
- Проверьте credentials (логин/пароль)

## Шаг 5: Открытие портов в файрволе

Убедитесь, что открыты необходимые порты:

```bash
# Для Ubuntu/Debian с ufw
sudo ufw allow 8000/tcp  # Порт подписок
sudo ufw allow 22/tcp    # SSH (если нужен)

# Для CentOS/RHEL с firewalld
sudo firewall-cmd --permanent --add-port=8000/tcp
sudo firewall-cmd --permanent --add-port=22/tcp
sudo firewall-cmd --reload
```

## Шаг 6: Проверка работоспособности

### Проверить доступность сервера подписок

```bash
# С самого VPS
curl -s http://localhost:8000/sub/test-uuid | head -c 100

# Или с удаленной машины
curl -s http://your-vps-ip:8000/sub/test-uuid | head -c 100
```

### Протестировать бот в Telegram

1. Откройте Telegram
2. Найдите вашего бота (по BOT_TOKEN)
3. Отправьте команду `/start`
4. Проверьте, что бот ответил главным меню

## Шаг 7: Настройка резервных копий

### Автоматические бэкапы БД

```bash
# Создать директорию для бэкапов
mkdir -p ~/vpn_bot/backups

# Создать cron задачу (редактор откроется)
crontab -e

# Добавить строку (ежедневный бэкап в 02:00)
0 2 * * * cd ~/vpn_bot && docker run --rm -v vpn_bot_vpn_data:/data -v $(pwd)/backups:/backup alpine cp /data/users.db /backup/users_$(date +\%Y\%m\%d_\%H\%M\%S).db
```

### Проверка бэкапов

```bash
# Просмотр созданных бэкапов
ls -lh ~/vpn_bot/backups/

# Восстановление из бэкапа (если нужно)
docker compose down
docker run --rm -v vpn_bot_vpn_data:/data -v ~/vpn_bot/backups:/backup alpine cp /backup/users_20260115_020000.db /data/users.db
docker compose up -d
```

## Шаг 8: Настройка CI/CD деплоя (опционально)

Если вы хотите автоматический деплой при push в main:

1. Создайте Docker Hub репозиторий
2. Создайте SSH ключ для GitHub Actions
3. Добавьте GitHub Secrets
4. Отредактируйте docker-compose.yml на VPS:
   ```yaml
   vpn-bot:
     image: your-username/vpn-bot:latest  # Вместо: build: .
   ```
5. Отредактируйте `.github/workflows/deploy.yml` с правильным путём к проекту

Подробные инструкции находятся в `/docs/plans/2026-01-15-ci-cd-autodeploy.md`

## Шаг 9: Мониторинг и обслуживание

### Просмотр логов в реальном времени

```bash
# Логи бота
docker compose logs -f vpn-bot

# Последние 100 строк
docker compose logs --tail=100 vpn-bot

# Конкретное количество времени
docker compose logs --since 1h vpn-bot
```

### Перезапуск бота при необходимости

```bash
# Мягкий перезапуск
docker compose restart vpn-bot

# Жесткий перезапуск (с пересборкой образа)
docker compose up -d --build vpn-bot
```

### Обновление кода

```bash
# Стянуть последние изменения
git pull origin main

# Если требуется пересборка образа
docker compose up -d --build

# Если используется образ из Docker Hub
docker compose pull vpn-bot && docker compose up -d
```

### Очистка старых образов и контейнеров

```bash
# Удалить неиспользуемые образы
docker image prune -a

# Удалить остановленные контейнеры
docker container prune

# Удалить неиспользуемые volumes (ОСТОРОЖНО!)
docker volume prune
```

## Troubleshooting

### Бот не запускается

1. Проверьте логи: `docker compose logs vpn-bot`
2. Убедитесь что все переменные в .env заполнены
3. Проверьте синтаксис .env файла (без пробелов вокруг =)

### Ошибка подключения к 3X-UI

```
Failed to login to Server A: Connection refused
```

Решение:
- Проверьте URL панели: `SERVER_A_BASE_URL`
- Убедитесь, что панель доступна: `curl -k https://your-panel-url:2053`
- Проверьте credentials
- Убедитесь в наличии интернета на VPS

### Порт 8000 не открыт

```bash
# Проверить, что контейнер слушает порт 8000
docker compose exec vpn-bot netstat -tlnp | grep 8000

# Проверить файрволл
sudo ufw status
sudo firewall-cmd --list-ports

# Проверить, что NAT правильно настроен (если за NAT)
curl http://localhost:8000/sub/test
```

### БД не сохраняется между перезапусками

```bash
# Проверить наличие volume
docker volume ls | grep vpn_data

# Проверить информацию о volume
docker volume inspect vpn_bot_vpn_data

# Проверить, что volume правильно монтируется
docker compose ps -a
```

### Нужен веб-интерфейс для просмотра БД

```bash
# Запустить SQLite Web UI
docker compose --profile tools up -d sqlite-web

# Доступен по http://localhost:8080
# Остановить
docker compose --profile tools stop sqlite-web
```

## Безопасность

1. **Никогда не коммитьте .env** - он содержит секретные данные
2. **Используйте strong passwords** для 3X-UI панелей
3. **SSH ключ для GitHub Actions** - создавайте отдельный ключ, не используйте личный
4. **Firewall** - оставляйте открытыми только необходимые порты
5. **SSL сертификаты** - используйте для доменов (Letsencrypt, nginx proxy и т.д.)
6. **Docker Hub токен** - используйте минимально необходимые права
7. **Регулярные бэкапы** - сохраняйте БД в безопасное место

## Полезные команды

```bash
# Общее состояние
docker compose ps
docker compose stats

# Логи
docker compose logs -f vpn-bot
docker compose logs --since 1h vpn-bot

# Управление
docker compose up -d          # Запуск
docker compose down           # Остановка
docker compose restart        # Перезапуск
docker compose pull vpn-bot   # Обновить образ

# Доступ в контейнер
docker compose exec vpn-bot sh

# Бэкап БД
make backup

# Просмотр БД
make db-ui

# Очистка
docker compose down -v        # Остановить и удалить volumes
docker image prune -a         # Удалить старые образы
```

## Дополнительная информация

- Основная документация: [README.md](../README.md)
- Быстрый старт: [QUICK_START.md](../QUICK_START.md)
- CI/CD деплой: [docs/plans/2026-01-15-ci-cd-autodeploy.md](./plans/2026-01-15-ci-cd-autodeploy.md)
- Инструкции для разработчиков: [CLAUDE.md](../CLAUDE.md)
