# CI/CD Auto-Deploy Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Настроить автоматический деплой VPN бота на VPS через GitHub Actions с использованием Docker Hub

**Architecture:** При пуше в main ветку GitHub Actions собирает Docker образ, пушит в Docker Hub, подключается по SSH к VPS и обновляет контейнер, скачав новый образ. .env файл хранится на сервере и не запекается в образ.

**Tech Stack:** GitHub Actions, Docker Hub, SSH, docker-compose

---

## Предварительная подготовка

### Что нужно иметь/получить перед началом:

1. **Docker Hub аккаунт** - для хранения образов
2. **VPS с установленным Docker** - целевой сервер
3. **Доступ к GitHub репозиторию** - для настройки Secrets

---

## Task 1: Подготовка Docker Hub

**Цель:** Создать репозиторий на Docker Hub для хранения образов бота

**Шаги:**

**Step 1: Создать репозиторий на Docker Hub**

1. Зайти на https://hub.docker.com/
2. Войти или зарегистрироваться
3. Нажать "Create Repository"
4. Заполнить:
   - Name: `vpn-bot`
   - Visibility: `Private` (если нужна приватность) или `Public`
5. Нажать "Create"

**Step 2: Получить Access Token для Docker Hub**

1. Зайти в Account Settings → Security → https://hub.docker.com/settings/security
2. Нажать "New Access Token"
3. Description: `GitHub Actions CI/CD`
4. Access permissions: `Read, Write, Delete`
5. Нажать "Generate"
6. **ВАЖНО:** Скопировать токен (он больше не будет показан)

**Результат:**
- Docker Hub репозиторий создан (например: `username/vpn-bot`)
- Access Token получен и сохранен

---

## Task 2: Генерация SSH ключей для доступа к VPS

**Цель:** Создать SSH ключ для безопасного подключения GitHub Actions к VPS

**Step 1: Сгенерировать SSH ключ на локальной машине**

```bash
ssh-keygen -t ed25519 -C "github-actions-deploy" -f ~/.ssh/github_actions_deploy -N ""
```

Expected output:
```
Generating public/private ed25519 key pair.
Your identification has been saved in ~/.ssh/github_actions_deploy
Your public key has been saved in ~/.ssh/github_actions_deploy.pub
```

**Step 2: Скопировать приватный ключ**

```bash
cat ~/.ssh/github_actions_deploy
```

Скопировать весь вывод (включая `-----BEGIN OPENSSH PRIVATE KEY-----` и `-----END OPENSSH PRIVATE KEY-----`)

**Step 3: Добавить публичный ключ на VPS**

```bash
ssh user@your-vps-ip "mkdir -p ~/.ssh && chmod 700 ~/.ssh"
cat ~/.ssh/github_actions_deploy.pub | ssh user@your-vps-ip "cat >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"
```

**Step 4: Проверить доступ**

```bash
ssh -i ~/.ssh/github_actions_deploy user@your-vps-ip "echo 'SSH connection successful'"
```

Expected: `SSH connection successful`

**Результат:**
- Приватный ключ сохранен (будет добавлен в GitHub Secrets)
- Публичный ключ добавлен на VPS
- SSH доступ проверен и работает

---

## Task 3: Настройка GitHub Secrets

**Цель:** Добавить чувствительные данные в GitHub репозиторий для использования в Actions

**Step 1: Открыть настройки Secrets**

1. Перейти в репозиторий на GitHub
2. Settings → Secrets and variables → Actions
3. Нажать "New repository secret"

**Step 2: Добавить Docker Hub credentials**

Создать два секрета:

**Secret 1: DOCKERHUB_USERNAME**
- Name: `DOCKERHUB_USERNAME`
- Value: `ваш-username-на-docker-hub`
- Нажать "Add secret"

**Secret 2: DOCKERHUB_TOKEN**
- Name: `DOCKERHUB_TOKEN`
- Value: [токен из Task 1, Step 2]
- Нажать "Add secret"

**Step 3: Добавить VPS SSH credentials**

**Secret 3: VPS_HOST**
- Name: `VPS_HOST`
- Value: `IP адрес или домен вашего VPS`
- Нажать "Add secret"

**Secret 4: VPS_USER**
- Name: `VPS_USER`
- Value: `username для SSH подключения` (обычно `root` или `ubuntu`)
- Нажать "Add secret"

**Secret 5: VPS_SSH_KEY**
- Name: `VPS_SSH_KEY`
- Value: [приватный ключ из Task 2, Step 2]
- Нажать "Add secret"

**Результат:**
- 5 секретов добавлено в GitHub:
  - DOCKERHUB_USERNAME
  - DOCKERHUB_TOKEN
  - VPS_HOST
  - VPS_USER
  - VPS_SSH_KEY

---

## Task 4: Обновление Dockerfile (опционально)

**Цель:** Убедиться что Dockerfile оптимизирован для production и не включает .env

**Files:**
- Modify: `Dockerfile` (если требуется)

**Step 1: Проверить текущий Dockerfile**

```bash
cat Dockerfile
```

**Step 2: Убедиться что .env НЕ копируется в образ**

Dockerfile НЕ должен содержать:
```dockerfile
COPY .env /app/  # ❌ НЕПРАВИЛЬНО
```

Правильный подход - .env монтируется через docker-compose или volume.

**Step 3: Убедиться что .dockerignore содержит .env**

```bash
cat .dockerignore
```

Если файл не существует или не содержит `.env`, создать/обновить:

```bash
echo ".env" >> .dockerignore
echo ".env.example" >> .dockerignore
echo ".git" >> .dockerignore
echo "*.md" >> .dockerignore
```

**Результат:**
- Dockerfile не копирует .env файл
- .dockerignore защищает от случайного включения .env

---

## Task 5: Создание GitHub Actions Workflow

**Цель:** Создать workflow для автоматической сборки, публикации и деплоя

**Files:**
- Create: `.github/workflows/deploy.yml`

**Step 1: Создать директорию для workflows**

```bash
mkdir -p .github/workflows
```

**Step 2: Создать файл deploy.yml**

Create: `.github/workflows/deploy.yml`

```yaml
name: Build and Deploy

on:
  push:
    branches:
      - main

env:
  DOCKER_IMAGE: ${{ secrets.DOCKERHUB_USERNAME }}/vpn-bot

jobs:
  build-and-push:
    name: Build Docker Image and Push to Docker Hub
    runs-on: ubuntu-latest

    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Login to Docker Hub
        uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}

      - name: Extract metadata for Docker
        id: meta
        uses: docker/metadata-action@v5
        with:
          images: ${{ env.DOCKER_IMAGE }}
          tags: |
            type=raw,value=latest
            type=sha,prefix={{branch}}-

      - name: Build and push Docker image
        uses: docker/build-push-action@v5
        with:
          context: .
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=registry,ref=${{ env.DOCKER_IMAGE }}:buildcache
          cache-to: type=registry,ref=${{ env.DOCKER_IMAGE }}:buildcache,mode=max

  deploy:
    name: Deploy to VPS
    needs: build-and-push
    runs-on: ubuntu-latest

    steps:
      - name: Deploy to VPS via SSH
        uses: appleboy/ssh-action@v1.0.3
        with:
          host: ${{ secrets.VPS_HOST }}
          username: ${{ secrets.VPS_USER }}
          key: ${{ secrets.VPS_SSH_KEY }}
          script: |
            cd /home/${{ secrets.VPS_USER }}/projects/vpn_bot
            docker compose pull vpn-bot
            docker compose up -d --no-deps vpn-bot
            docker image prune -af
```

**Step 3: Зафиксировать изменения**

```bash
git add .github/workflows/deploy.yml .dockerignore
git commit -m "ci: add GitHub Actions workflow for auto-deploy"
```

**Результат:**
- Workflow создан и закоммичен
- При пуше в main будет:
  1. Собираться Docker образ
  2. Публиковаться в Docker Hub
  3. Деплоиться на VPS

---

## Task 6: Обновление docker-compose.yml на VPS

**Цель:** Настроить docker-compose на VPS для использования образа из Docker Hub

**Files:**
- Modify: `docker-compose.yml` (на VPS)

**Step 1: Подключиться к VPS**

```bash
ssh user@your-vps-ip
```

**Step 2: Перейти в директорию проекта**

```bash
cd /home/user/projects/vpn_bot
```

**Step 3: Сделать бэкап текущего docker-compose.yml**

```bash
cp docker-compose.yml docker-compose.yml.backup
```

**Step 4: Обновить docker-compose.yml**

Изменить секцию `vpn-bot`:

**БЫЛО:**
```yaml
vpn-bot:
  build: .
  container_name: vpn-bot
  # ...
```

**СТАЛО:**
```yaml
vpn-bot:
  image: your-dockerhub-username/vpn-bot:latest
  container_name: vpn-bot
  # ... остальное остается без изменений
```

**Step 5: Проверить что .env файл на месте**

```bash
ls -la .env
```

Expected: файл существует

**Step 6: Первый запуск с новым образом**

```bash
docker compose pull vpn-bot
docker compose up -d vpn-bot
docker compose logs -f vpn-bot
```

Expected: Бот успешно запущен

**Результат:**
- docker-compose.yml обновлен для использования образа из Docker Hub
- .env файл остался на сервере
- Бот работает с новым образом

---

## Task 7: Тестирование CI/CD Pipeline

**Цель:** Проверить что весь процесс работает end-to-end

**Step 1: Сделать тестовое изменение**

На локальной машине:

```bash
# Добавить комментарий в main.go
echo "// CI/CD test comment" >> cmd/bot/main.go
```

**Step 2: Закоммитить и запушить в main**

```bash
git add cmd/bot/main.go
git commit -m "test: CI/CD pipeline test"
git push origin main
```

**Step 3: Проверить GitHub Actions**

1. Открыть репозиторий на GitHub
2. Перейти в Actions
3. Найти новый workflow run
4. Проверить что оба job (build-and-push и deploy) успешны

**Step 4: Проверить что образ появился на Docker Hub**

1. Зайти на https://hub.docker.com/
2. Открыть репозиторий vpn-bot
3. Проверить что есть новый тег `latest` с недавней датой

**Step 5: Проверить на VPS что бот обновился**

```bash
ssh user@your-vps-ip
cd /home/user/projects/vpn_bot
docker compose logs --tail=50 vpn-bot
```

Expected: В логах виден рестарт и бот работает

**Step 6: Откатить тестовое изменение**

```bash
git revert HEAD
git push origin main
```

**Результат:**
- CI/CD pipeline полностью работает
- Изменения автоматически доставляются на VPS
- Бот перезапускается с новым образом

---

## Task 8: Добавление уведомлений (опционально)

**Цель:** Получать уведомления о статусе деплоя

**Files:**
- Modify: `.github/workflows/deploy.yml`

**Step 1: Добавить Telegram уведомления (если нужно)**

Добавить в конец файла `.github/workflows/deploy.yml`:

```yaml
      - name: Notify on success
        if: success()
        uses: appleboy/telegram-action@master
        with:
          to: ${{ secrets.TELEGRAM_CHAT_ID }}
          token: ${{ secrets.TELEGRAM_BOT_TOKEN }}
          message: |
            ✅ Deploy successful!

            Commit: ${{ github.sha }}
            Author: ${{ github.actor }}
            Message: ${{ github.event.head_commit.message }}

      - name: Notify on failure
        if: failure()
        uses: appleboy/telegram-action@master
        with:
          to: ${{ secrets.TELEGRAM_CHAT_ID }}
          token: ${{ secrets.TELEGRAM_BOT_TOKEN }}
          message: |
            ❌ Deploy failed!

            Commit: ${{ github.sha }}
            Author: ${{ github.actor }}
            Check: https://github.com/${{ github.repository }}/actions/runs/${{ github.run_id }}
```

**Step 2: Добавить секреты в GitHub**

- `TELEGRAM_CHAT_ID` - ID чата для уведомлений
- `TELEGRAM_BOT_TOKEN` - токен бота для отправки уведомлений

**Step 3: Закоммитить изменения**

```bash
git add .github/workflows/deploy.yml
git commit -m "ci: add Telegram notifications for deploy status"
git push origin main
```

**Результат:**
- При успешном деплое приходит уведомление в Telegram
- При ошибке деплоя приходит уведомление с ссылкой на логи

---

## Важные замечания

### Безопасность:

1. **НЕ коммитить .env файл** - он должен быть только на VPS
2. **Использовать GitHub Secrets** для всех чувствительных данных
3. **SSH ключ должен быть уникальным** для GitHub Actions (не использовать свой личный)
4. **Docker Hub токен** должен иметь минимальные необходимые права

### Оптимизация:

1. **Docker build cache** используется для ускорения сборки
2. **docker image prune** очищает старые образы на VPS
3. **--no-deps vpn-bot** обновляет только контейнер бота, не трогая другие сервисы

### Troubleshooting:

**Если GitHub Actions не может подключиться к VPS:**
- Проверить что SSH ключ правильно добавлен в Secrets (с переносами строк)
- Проверить что публичный ключ добавлен в `~/.ssh/authorized_keys` на VPS
- Проверить firewall на VPS (должен быть открыт порт 22)

**Если образ не пушится в Docker Hub:**
- Проверить что токен валиден и имеет права Write
- Проверить что имя образа правильное: `username/vpn-bot`

**Если бот не запускается после деплоя:**
- Проверить логи: `docker compose logs vpn-bot`
- Убедиться что .env файл на месте и содержит все необходимые переменные
- Проверить что docker-compose.yml правильно монтирует volume с БД

---

## Структура финальных файлов

После выполнения всех задач структура проекта:

```
vpn_bot/
├── .github/
│   └── workflows/
│       └── deploy.yml          # GitHub Actions workflow
├── .dockerignore                # Исключения для Docker build
├── Dockerfile                   # Без COPY .env
├── docker-compose.yml           # На VPS: использует image из Docker Hub
├── docs/
│   └── plans/
│       └── 2026-01-15-ci-cd-autodeploy.md
└── ...
```

На VPS:
```
/home/user/projects/vpn_bot/
├── docker-compose.yml           # image: username/vpn-bot:latest
├── .env                         # Переменные окружения (НЕ в git)
└── data/
    └── users.db                 # База данных (volume)
```

---

## Checklist перед началом

- [ ] Создан Docker Hub аккаунт
- [ ] Создан репозиторий на Docker Hub
- [ ] Получен Docker Hub Access Token
- [ ] Сгенерированы SSH ключи
- [ ] Публичный ключ добавлен на VPS
- [ ] Проверено SSH подключение
- [ ] Добавлены все 5 Secrets в GitHub
- [ ] .env файл есть на VPS и содержит все переменные
- [ ] Docker и docker-compose установлены на VPS

---

## Время выполнения

- Task 1: ~5 минут
- Task 2: ~10 минут
- Task 3: ~5 минут
- Task 4: ~5 минут
- Task 5: ~10 минут
- Task 6: ~10 минут
- Task 7: ~15 минут
- Task 8 (опционально): ~10 минут

**Итого:** ~60-70 минут для полной настройки
