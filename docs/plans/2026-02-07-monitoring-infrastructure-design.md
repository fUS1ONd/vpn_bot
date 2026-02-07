# Инфраструктура мониторинга нод (VictoriaMetrics + Node Exporter)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Развернуть стек мониторинга (VictoriaMetrics + vmagent) на сервере бота и автоматически собирать метрики с Node Exporter на каждой ноде Remnawave.

**Architecture:** Бот генерирует `targets.json` из Remnawave API (список нод с IP). vmagent читает этот файл через file_sd_configs и скрейпит Node Exporter (порт 9100) на каждой ноде. Метрики пишутся в VictoriaMetrics, откуда бот их читает через PromQL API.

**Tech Stack:** VictoriaMetrics, vmagent, Node Exporter, Docker Compose, file_sd_configs (Prometheus SD)

---

## Схема взаимодействия

```
┌─────────────────── Центральный сервер ───────────────────┐
│                                                           │
│  ┌─────────┐  targets.json  ┌─────────┐  write  ┌─────┐ │
│  │ VPN Bot │ ────────────→  │ vmagent │ ──────→ │ VM  │ │
│  │  (Go)   │                │         │         │ DB  │ │
│  └────┬────┘                └────┬────┘         └──┬──┘ │
│       │ PromQL query             │ scrape :9100    │     │
│       └──────────────────────────┼─────────────────┘     │
│                                  │                        │
└──────────────────────────────────┼────────────────────────┘
                                   │
                    ┌──────────────┼──────────────┐
                    │              │              │
               ┌────▼───┐   ┌────▼───┐   ┌────▼───┐
               │ Node 1 │   │ Node 2 │   │ Node N │
               │ :9100  │   │ :9100  │   │ :9100  │
               └────────┘   └────────┘   └────────┘
```

---

## Ответ Remnawave API `GET /api/nodes`

Бот использует следующие поля из ответа:

```json
{
  "response": [
    {
      "uuid": "...",
      "name": "DE-Frankfurt-1",
      "address": "10.0.0.1",
      "port": null,
      "isConnected": true,
      "isDisabled": false,
      "countryCode": "DE",
      "tags": ["bw:1000", "premium"],
      "usersOnline": 42
    }
  ]
}
```

**Ключевые поля для мониторинга:**
- `address` — IP ноды (для targets.json, Node Exporter скрейпится на `address:9100`)
- `name` — человекочитаемое имя (label `hostname` в targets)
- `countryCode` — код страны (label `country`)
- `tags` — массив строк; бот парсит тег с префиксом `bw:` для bandwidth (Mbps)
- `isDisabled` — отключённые ноды исключаются из мониторинга
- `isConnected` — статус подключения к панели

**Конвенция тегов bandwidth:** Тег формата `bw:<число>`, где число — пропускная способность канала в Mbps. Пример: `bw:1000` = 1 Gbit. Если тега нет — дефолт 1000 Mbps.

---

## Task 1: Docker Compose — добавить VictoriaMetrics и vmagent

**Files:**
- Modify: `docker-compose.yml`
- Create: `monitoring/prometheus.yml`

**Step 1: Создать директорию monitoring и конфиг prometheus.yml**

Создать файл `monitoring/prometheus.yml`:

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'vps_nodes'
    file_sd_configs:
      - files:
          - '/etc/prometheus/sd_configs/targets.json'
        refresh_interval: 30s
```

**Step 2: Обновить docker-compose.yml**

Добавить сервисы `victoriametrics` и `vmagent`, общую папку `sd_configs`:

```yaml
services:
  vpn-bot:
    image: fus1ond/vpn-bot:latest
    container_name: vpn-bot
    restart: unless-stopped
    volumes:
      - ./data:/app/data
      - ./sd_configs:/app/sd_configs  # Общая папка для targets.json
    environment:
      - BOT_TOKEN=${BOT_TOKEN}
      - ADMIN_ID=${ADMIN_ID}
      - REMNAWAVE_URL=${REMNAWAVE_URL}
      - REMNAWAVE_API_TOKEN=${REMNAWAVE_API_TOKEN}
      - REMNAWAVE_DEFAULT_SQUAD_UUID=${REMNAWAVE_DEFAULT_SQUAD_UUID}
      - DB_PATH=${DB_PATH:-/app/data/bot.db}
      - DONATE_TEXT=${DONATE_TEXT}
      - SD_CONFIGS_PATH=${SD_CONFIGS_PATH:-/app/sd_configs}
      - VICTORIA_METRICS_URL=${VICTORIA_METRICS_URL:-http://victoriametrics:8428}
    env_file:
      - .env
    networks:
      - vpn-network
    depends_on:
      - victoriametrics
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"

  victoriametrics:
    image: victoriametrics/victoria-metrics:latest
    container_name: vm_db
    restart: unless-stopped
    ports:
      - "127.0.0.1:8428:8428"
    volumes:
      - ./vm-data:/storage
    command:
      - "--storageDataPath=/storage"
      - "--retentionPeriod=30d"
    networks:
      - vpn-network
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"

  vmagent:
    image: victoriametrics/vmagent:latest
    container_name: vm_agent
    restart: unless-stopped
    depends_on:
      - victoriametrics
    volumes:
      - ./monitoring/prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - ./sd_configs:/etc/prometheus/sd_configs:ro
    command:
      - "--promscrape.config=/etc/prometheus/prometheus.yml"
      - "--remoteWrite.url=http://victoriametrics:8428/api/v1/write"
    networks:
      - vpn-network
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"

networks:
  vpn-network:
    driver: bridge
```

**Step 3: Создать пустую папку sd_configs**

```bash
mkdir -p sd_configs
echo '[]' > sd_configs/targets.json
```

**Step 4: Проверить запуск стека**

```bash
docker compose up -d
docker compose ps  # Все 3 сервиса running
curl -s http://localhost:8428/api/v1/status/tsdb | head  # VM отвечает
```

**Step 5: Commit**

```
feat: добавить VictoriaMetrics и vmagent в docker-compose
```

---

## Task 2: Скрипт установки Node Exporter на ноды

**Files:**
- Create: `scripts/install-node-exporter.sh`

**Step 1: Написать скрипт установки**

```bash
#!/bin/bash
set -euo pipefail

# Скрипт установки Node Exporter на VPS-ноду
# Использование: ./install-node-exporter.sh <IP_ЦЕНТРАЛЬНОГО_СЕРВЕРА>

NODE_EXPORTER_VERSION="1.8.2"
CENTRAL_IP="${1:?Укажите IP центрального сервера: ./install-node-exporter.sh 1.2.3.4}"

echo "=== Установка Node Exporter v${NODE_EXPORTER_VERSION} ==="

# Скачиваем и устанавливаем
cd /tmp
wget -q "https://github.com/prometheus/node_exporter/releases/download/v${NODE_EXPORTER_VERSION}/node_exporter-${NODE_EXPORTER_VERSION}.linux-amd64.tar.gz"
tar xzf "node_exporter-${NODE_EXPORTER_VERSION}.linux-amd64.tar.gz"
sudo mv "node_exporter-${NODE_EXPORTER_VERSION}.linux-amd64/node_exporter" /usr/local/bin/
rm -rf "node_exporter-${NODE_EXPORTER_VERSION}.linux-amd64"*

# Создаём systemd-сервис
cat <<EOF | sudo tee /etc/systemd/system/node_exporter.service
[Unit]
Description=Node Exporter
After=network.target

[Service]
User=nobody
ExecStart=/usr/local/bin/node_exporter

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now node_exporter

# Настройка firewall — порт 9100 только для центрального сервера
if command -v ufw &>/dev/null; then
    sudo ufw allow from "${CENTRAL_IP}" to any port 9100 proto tcp
    sudo ufw reload
    echo "UFW: порт 9100 открыт для ${CENTRAL_IP}"
elif command -v firewall-cmd &>/dev/null; then
    sudo firewall-cmd --permanent --add-rich-rule="rule family=ipv4 source address=${CENTRAL_IP} port port=9100 protocol=tcp accept"
    sudo firewall-cmd --reload
    echo "firewalld: порт 9100 открыт для ${CENTRAL_IP}"
else
    echo "ВНИМАНИЕ: firewall не найден. Вручную откройте порт 9100 для ${CENTRAL_IP}"
fi

# Проверка
sleep 2
if curl -sf http://localhost:9100/metrics | head -1 | grep -q "HELP"; then
    echo "=== Node Exporter установлен и работает ==="
else
    echo "ОШИБКА: Node Exporter не отвечает на localhost:9100"
    exit 1
fi
```

**Step 2: Commit**

```
feat: скрипт установки Node Exporter на ноды
```

---

## Task 3: Добавить метод GetAllNodes в Remnawave клиент

**Files:**
- Modify: `internal/remnawave/client.go`

**Step 1: Добавить структуру Node и метод GetAllNodes**

В `internal/remnawave/client.go` добавить после существующих типов:

```go
// Node — данные ноды из Remnawave
type Node struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Port        *int     `json:"port"`
	IsConnected bool     `json:"isConnected"`
	IsDisabled  bool     `json:"isDisabled"`
	CountryCode string   `json:"countryCode"`
	Tags        []string `json:"tags"`
	UsersOnline *int     `json:"usersOnline"`
}
```

Добавить метод:

```go
// GetAllNodes получает список всех нод
func (c *Client) GetAllNodes() ([]Node, error) {
	resp, err := c.doRequest("GET", "/api/nodes", nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Response []Node `json:"response"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal nodes response: %w", err)
	}

	return result.Response, nil
}
```

**Step 2: Commit**

```
feat: метод GetAllNodes в Remnawave клиенте
```

---

## Task 4: Добавить конфиг-переменные для мониторинга

**Files:**
- Modify: `internal/config/config.go`

**Step 1: Расширить Config**

Добавить поля в структуру `Config`:

```go
// Мониторинг
SDConfigsPath      string // Путь к папке sd_configs для targets.json
VictoriaMetricsURL string // URL VictoriaMetrics API
```

В функции `Load()` добавить:

```go
SDConfigsPath:      getEnvOrDefault("SD_CONFIGS_PATH", "/app/sd_configs"),
VictoriaMetricsURL: getEnvOrDefault("VICTORIA_METRICS_URL", "http://victoriametrics:8428"),
```

**Step 2: Commit**

```
feat: конфиг мониторинга (SD_CONFIGS_PATH, VICTORIA_METRICS_URL)
```

---

## Task 5: SyncNodes — генерация targets.json

**Files:**
- Create: `internal/monitoring/sync.go`

**Step 1: Реализовать SyncNodes**

```go
package monitoring

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

const (
	// DefaultBandwidthMbps — дефолтная пропускная способность если тег bw: не найден
	DefaultBandwidthMbps = 1000
	// NodeExporterPort — порт Node Exporter на нодах
	NodeExporterPort = 9100
)

// Target — элемент файла targets.json (Prometheus file_sd_configs)
type Target struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// ParseBandwidthTag парсит тег bw:<число> из массива тегов ноды.
// Возвращает DefaultBandwidthMbps если тег не найден.
func ParseBandwidthTag(tags []string) int {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "bw:") {
			var bw int
			if _, err := fmt.Sscanf(tag, "bw:%d", &bw); err == nil && bw > 0 {
				return bw
			}
		}
	}
	return DefaultBandwidthMbps
}

// SyncNodes получает список нод из Remnawave и записывает targets.json.
// Возвращает количество активных нод.
func SyncNodes(client *remnawave.Client, sdConfigsPath string) (int, error) {
	nodes, err := client.GetAllNodes()
	if err != nil {
		return 0, fmt.Errorf("не удалось получить список нод: %w", err)
	}

	var targets []Target
	for _, node := range nodes {
		// Пропускаем отключённые ноды
		if node.IsDisabled {
			continue
		}

		bw := ParseBandwidthTag(node.Tags)

		target := Target{
			Targets: []string{fmt.Sprintf("%s:%d", node.Address, NodeExporterPort)},
			Labels: map[string]string{
				"hostname":     node.Name,
				"country":      node.CountryCode,
				"bandwidth_mb": fmt.Sprintf("%d", bw),
				"node_uuid":    node.UUID,
			},
		}
		targets = append(targets, target)
	}

	data, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("не удалось сериализовать targets: %w", err)
	}

	targetFile := filepath.Join(sdConfigsPath, "targets.json")
	if err := os.WriteFile(targetFile, data, 0644); err != nil {
		return 0, fmt.Errorf("не удалось записать %s: %w", targetFile, err)
	}

	slog.Info("Targets synced", "active_nodes", len(targets), "path", targetFile)
	return len(targets), nil
}
```

**Step 2: Commit**

```
feat: SyncNodes — генерация targets.json из Remnawave API
```

---

## Task 6: Фоновый цикл синхронизации targets

**Files:**
- Create: `internal/monitoring/scheduler.go`

**Step 1: Реализовать StartSyncLoop**

```go
package monitoring

import (
	"context"
	"log/slog"
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

const syncInterval = 60 * time.Second

// StartSyncLoop запускает фоновый цикл синхронизации targets.json
func StartSyncLoop(ctx context.Context, client *remnawave.Client, sdConfigsPath string) {
	slog.Info("Запуск фоновой синхронизации targets", "interval", syncInterval)

	// Синхронизация при старте
	if _, err := SyncNodes(client, sdConfigsPath); err != nil {
		slog.Error("Ошибка первичной синхронизации targets", "error", err)
	}

	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Синхронизация targets остановлена")
			return
		case <-ticker.C:
			if _, err := SyncNodes(client, sdConfigsPath); err != nil {
				slog.Error("Ошибка синхронизации targets", "error", err)
			}
		}
	}
}
```

**Step 2: Подключить в main.go**

В `cmd/bot/main.go` после создания `remnawaveClient` добавить:

```go
import "github.com/fus1ond/vpn_bot/internal/monitoring"

// ... в main(), после go telegramBot.StartScheduler(ctx):
go monitoring.StartSyncLoop(ctx, remnawaveClient, cfg.SDConfigsPath)
```

**Step 3: Commit**

```
feat: фоновая синхронизация targets.json каждые 60 секунд
```

---

## Task 7: Клиент VictoriaMetrics для PromQL запросов

**Files:**
- Create: `internal/monitoring/metrics.go`

**Step 1: Реализовать клиент метрик**

```go
package monitoring

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// MetricsClient — клиент для запросов к VictoriaMetrics API
type MetricsClient struct {
	baseURL string
	http    *http.Client
}

// NewMetricsClient создаёт новый клиент метрик
func NewMetricsClient(baseURL string) *MetricsClient {
	return &MetricsClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// NodeStats — статистика одной ноды
type NodeStats struct {
	Hostname    string
	Country     string
	BandwidthMb int
	CpuPercent  float64
	NetOutMbps  float64
	NetInMbps   float64
	PktLoss     float64
	LoadIndex   float64
	StatusEmoji string
	IsUp        bool
}

// promResult — структура ответа VictoriaMetrics на PromQL instant query
type promResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]interface{}    `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// query выполняет PromQL instant query
func (m *MetricsClient) query(promql string) (*promResult, error) {
	u := fmt.Sprintf("%s/api/v1/query?query=%s", m.baseURL, url.QueryEscape(promql))

	resp, err := m.http.Get(u)
	if err != nil {
		return nil, fmt.Errorf("ошибка запроса к VM: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения ответа VM: %w", err)
	}

	var result promResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("ошибка парсинга ответа VM: %w", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("VM вернула статус: %s", result.Status)
	}

	return &result, nil
}

// extractByHostname достаёт значение метрики по hostname из результата PromQL
func extractByHostname(result *promResult) map[string]float64 {
	out := make(map[string]float64)
	for _, r := range result.Data.Result {
		hostname := r.Metric["hostname"]
		if hostname == "" {
			continue
		}
		if valStr, ok := r.Value[1].(string); ok {
			if val, err := strconv.ParseFloat(valStr, 64); err == nil {
				out[hostname] = val
			}
		}
	}
	return out
}

// PromQL запросы для метрик нод
const (
	// CPU — процент использования CPU (100 - idle)
	queryCPU = `100 - (avg by (hostname) (rate(node_cpu_seconds_total{mode="idle"}[2m])) * 100)`

	// NET OUT — исходящий трафик в битах/сек
	queryNetOut = `sum(rate(node_network_transmit_bytes_total{device!="lo"}[2m])) by (hostname) * 8`

	// NET IN — входящий трафик в битах/сек
	queryNetIn = `sum(rate(node_network_receive_bytes_total{device!="lo"}[2m])) by (hostname) * 8`

	// PACKET LOSS — ретрансмиссии TCP (proxy для потерь пакетов)
	queryPktLoss = `rate(node_netstat_Tcp_RetransSegs[2m])`

	// UP — нода жива (Node Exporter отвечает)
	queryUp = `up{job="vps_nodes"}`
)

// GetAllNodeStats получает текущие метрики всех нод
func (m *MetricsClient) GetAllNodeStats(nodes []Target) ([]NodeStats, error) {
	// Параллельно запрашиваем все метрики
	cpuResult, err := m.query(queryCPU)
	if err != nil {
		return nil, fmt.Errorf("ошибка CPU query: %w", err)
	}

	netOutResult, err := m.query(queryNetOut)
	if err != nil {
		return nil, fmt.Errorf("ошибка NET OUT query: %w", err)
	}

	netInResult, err := m.query(queryNetIn)
	if err != nil {
		return nil, fmt.Errorf("ошибка NET IN query: %w", err)
	}

	pktLossResult, err := m.query(queryPktLoss)
	if err != nil {
		return nil, fmt.Errorf("ошибка PKT LOSS query: %w", err)
	}

	upResult, err := m.query(queryUp)
	if err != nil {
		return nil, fmt.Errorf("ошибка UP query: %w", err)
	}

	// Извлекаем значения по hostname
	cpuMap := extractByHostname(cpuResult)
	netOutMap := extractByHostname(netOutResult)
	netInMap := extractByHostname(netInResult)
	pktLossMap := extractByHostname(pktLossResult)
	upMap := extractByHostname(upResult)

	// Собираем статистику для каждой ноды из targets
	var stats []NodeStats
	for _, target := range nodes {
		hostname := target.Labels["hostname"]
		country := target.Labels["country"]
		bwStr := target.Labels["bandwidth_mb"]
		bw, _ := strconv.Atoi(bwStr)
		if bw == 0 {
			bw = DefaultBandwidthMbps
		}

		netOutBps := netOutMap[hostname]       // биты/сек
		netOutMbps := netOutBps / 1_000_000    // Mbps

		netInBps := netInMap[hostname]
		netInMbps := netInBps / 1_000_000

		node := NodeStats{
			Hostname:    hostname,
			Country:     country,
			BandwidthMb: bw,
			CpuPercent:  cpuMap[hostname],
			NetOutMbps:  netOutMbps,
			NetInMbps:   netInMbps,
			PktLoss:     pktLossMap[hostname],
			IsUp:        upMap[hostname] == 1,
		}

		node = CalculateLoadIndex(node)
		stats = append(stats, node)
	}

	return stats, nil
}
```

**Step 2: Commit**

```
feat: клиент VictoriaMetrics с PromQL запросами метрик
```

---

## Task 8: Формула Load Index

**Files:**
- Create: `internal/monitoring/loadindex.go`

**Step 1: Реализовать формулу**

```go
package monitoring

import "math"

// CalculateLoadIndex вычисляет индекс нагрузки ноды.
// Формула: max(CPU%, NET%) + штраф за потери пакетов.
func CalculateLoadIndex(stats NodeStats) NodeStats {
	// Процент загрузки сети (исходящий трафик / лимит канала)
	netLoadPercent := 0.0
	if stats.BandwidthMb > 0 {
		netLoadPercent = (stats.NetOutMbps / float64(stats.BandwidthMb)) * 100
	}

	// Базовая нагрузка — максимум между CPU и сетью
	baseLoad := math.Max(stats.CpuPercent, netLoadPercent)

	// Штраф за потери пакетов (TCP retransmissions/sec)
	penalty := 0.0
	if stats.PktLoss > 0.5 {
		penalty += 10
	}
	if stats.PktLoss > 2.0 {
		penalty += 40
	}

	stats.LoadIndex = math.Min(baseLoad+penalty, 100)

	// Определяем эмодзи статуса
	switch {
	case !stats.IsUp:
		stats.StatusEmoji = "⚫"
	case stats.LoadIndex >= 80:
		stats.StatusEmoji = "🔴"
	case stats.LoadIndex >= 50:
		stats.StatusEmoji = "🟡"
	default:
		stats.StatusEmoji = "🟢"
	}

	return stats
}
```

**Step 2: Commit**

```
feat: формула Load Index с учётом CPU, сети и потерь пакетов
```

---

## Итог

После выполнения этих 8 задач:
- VictoriaMetrics и vmagent развёрнуты в docker-compose
- Бот автоматически генерирует targets.json из списка нод Remnawave каждые 60 сек
- vmagent автоматически скрейпит Node Exporter на нодах
- Бот может запрашивать метрики из VictoriaMetrics через PromQL
- Load Index рассчитывается по формуле max(CPU, NET%) + penalty за потери
- Скрипт для быстрой установки Node Exporter на новые ноды

Следующий план (Bot Dashboard) использует `MetricsClient` и `NodeStats` из этого плана для отображения Live-дашборда в Telegram.
