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

	// Получаем хосты для маппинга node UUID → remark (имя видимое пользователям)
	nodeDisplayName := make(map[string]string)
	hosts, err := client.GetAllHosts()
	if err != nil {
		slog.Warn("Не удалось получить хосты, используем имена нод", "error", err)
	} else {
		for _, host := range hosts {
			for _, nodeUUID := range host.Nodes {
				nodeDisplayName[nodeUUID] = host.Remark
			}
		}
	}

	var targets []Target
	for _, node := range nodes {
		// Пропускаем отключённые ноды
		if node.IsDisabled {
			continue
		}

		bw := ParseBandwidthTag(node.Tags)

		// Берём имя хоста (видимое пользователям), иначе имя ноды
		hostname := node.Name
		if name, ok := nodeDisplayName[node.UUID]; ok {
			hostname = name
		}

		target := Target{
			Targets: []string{fmt.Sprintf("%s:%d", node.Address, NodeExporterPort)},
			Labels: map[string]string{
				"hostname":     hostname,
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

// ReadTargets читает текущий targets.json
func ReadTargets(sdConfigsPath string) ([]Target, error) {
	targetFile := filepath.Join(sdConfigsPath, "targets.json")
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать %s: %w", targetFile, err)
	}

	var targets []Target
	if err := json.Unmarshal(data, &targets); err != nil {
		return nil, fmt.Errorf("не удалось распарсить targets: %w", err)
	}

	return targets, nil
}
