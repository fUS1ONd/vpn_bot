package monitoring

import "testing"

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
