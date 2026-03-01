# CI в GitHub Actions Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Добавить стадию `ci` в GitHub Actions пайплайн, которая выполняет форматирование, vet, сборку бинаря и тесты перед сборкой Docker-образа.

**Architecture:** Добавляется один новый job `ci` перед `build-and-push`. Job использует `go-version-file: go.mod` для определения версии Go. Сборка Docker запускается только если CI прошёл (`needs: ci`).

**Tech Stack:** GitHub Actions, Go 1.25, go vet, gofmt, go build, go test

---

## Итоговая цепочка пайплайна

```
push в main → ci (fmt + vet + build + tests) → build-and-push → deploy
```

---

### Task 1: Добавить job `ci` и зависимость в `build-and-push`

**Files:**
- Modify: `.github/workflows/deploy.yml`

**Шаг 1: Прочитать текущий файл**

```bash
cat .github/workflows/deploy.yml
```

**Шаг 2: Добавить job `ci` перед `build-and-push`**

Вставить в раздел `jobs:` перед `build-and-push`:

```yaml
  ci:
    name: Lint and Test
    runs-on: ubuntu-latest

    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Check formatting
        run: |
          if [ -n "$(gofmt -l .)" ]; then
            echo "Следующие файлы не отформатированы:"
            gofmt -l .
            exit 1
          fi

      - name: Run vet
        run: go vet ./...

      - name: Build binary
        run: go build ./cmd/bot/...

      - name: Run tests
        run: go test ./...
```

**Шаг 3: Добавить зависимость в job `build-and-push`**

В секцию `build-and-push:` добавить строку `needs: ci` после `name:`:

```yaml
  build-and-push:
    name: Build Docker Image and Push to Docker Hub
    needs: ci
    runs-on: ubuntu-latest
```

**Шаг 4: Проверить итоговый файл**

Убедиться что структура корректная (YAML-синтаксис, порядок jobs: ci → build-and-push → deploy).

Итоговый файл `.github/workflows/deploy.yml` должен выглядеть так:

```yaml
name: Build and Deploy

on:
  push:
    branches:
      - main

env:
  DOCKER_IMAGE: ${{ secrets.DOCKERHUB_USERNAME }}/vpn-bot

jobs:
  ci:
    name: Lint and Test
    runs-on: ubuntu-latest

    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Check formatting
        run: |
          if [ -n "$(gofmt -l .)" ]; then
            echo "Следующие файлы не отформатированы:"
            gofmt -l .
            exit 1
          fi

      - name: Run vet
        run: go vet ./...

      - name: Build binary
        run: go build ./cmd/bot/...

      - name: Run tests
        run: go test ./...

  build-and-push:
    name: Build Docker Image and Push to Docker Hub
    needs: ci
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
            cd ~/vpn_bot
            git pull origin main
            docker compose pull vpn-bot
            docker compose up -d --no-deps vpn-bot
            docker image prune -af
```

**Шаг 5: Назначение коммита**

`ci: добавить стадию CI (fmt, vet, build, test) в GitHub Actions`

---

## Проверка после мержа в main

1. В GitHub Actions появляется job `Lint and Test` перед `Build Docker Image and Push to Docker Hub`
2. При падении теста/vet/build job `build-and-push` не запускается
3. При успехе всё работает как раньше: ci → build-and-push → deploy
