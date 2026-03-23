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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB max
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
	// Запрашиваем все метрики
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

		netOutBps := netOutMap[hostname]    // биты/сек
		netOutMbps := netOutBps / 1_000_000 // Mbps

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
