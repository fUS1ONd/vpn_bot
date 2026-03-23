package monitoring

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBandwidthTag(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want int
	}{
		{"uppercase BW:1000", []string{"BW:1000"}, 1000},
		{"lowercase bw:500", []string{"bw:500"}, 500},
		{"mixed case Bw:1500", []string{"Bw:1500"}, 1500},
		{"mixed case bW:2000", []string{"bW:2000"}, 2000},
		{"среди других тегов", []string{"PREMIUM", "BW:750", "EU"}, 750},
		{"без тега — дефолт", []string{"PREMIUM", "EU"}, DefaultBandwidthMbps},
		{"пустой список", []string{}, DefaultBandwidthMbps},
		{"nil список", nil, DefaultBandwidthMbps},
		{"невалидное значение", []string{"BW:abc"}, DefaultBandwidthMbps},
		{"нулевое значение", []string{"BW:0"}, DefaultBandwidthMbps},
		{"отрицательное значение", []string{"BW:-100"}, DefaultBandwidthMbps},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseBandwidthTag(tt.tags)
			if got != tt.want {
				t.Errorf("ParseBandwidthTag(%v) = %d, want %d", tt.tags, got, tt.want)
			}
		})
	}
}

func TestWriteFileAtomicallyReplacesContentAndCleansTempFile(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "targets.json")

	if err := os.WriteFile(targetFile, []byte(`old-content`), 0644); err != nil {
		t.Fatalf("подготовка старого файла: %v", err)
	}

	newContent := []byte(`[{"targets":["127.0.0.1:9100"]}]`)
	if err := writeFileAtomically(targetFile, newContent, 0644); err != nil {
		t.Fatalf("writeFileAtomically вернул ошибку: %v", err)
	}

	got, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("чтение итогового файла: %v", err)
	}
	if string(got) != string(newContent) {
		t.Fatalf("итоговый файл = %q, want %q", string(got), string(newContent))
	}

	if _, err := os.Stat(targetFile + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("временный файл не должен оставаться после успешной записи")
	}
}
