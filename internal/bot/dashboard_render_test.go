package bot

import (
	"strings"
	"testing"

	"github.com/fus1ond/vpn_bot/internal/monitoring"
)

func TestCountryFlagEmoji(t *testing.T) {
	tests := []struct {
		name        string
		countryCode string
		want        string
	}{
		{name: "uppercase ISO code", countryCode: "SE", want: "🇸🇪"},
		{name: "lowercase ISO code", countryCode: "fi", want: "🇫🇮"},
		{name: "surrounding whitespace", countryCode: " DE ", want: "🇩🇪"},
		{name: "empty code", countryCode: "", want: ""},
		{name: "invalid code", countryCode: "SWE", want: ""},
		{name: "non alphabetic code", countryCode: "1A", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countryFlagEmoji(tt.countryCode); got != tt.want {
				t.Fatalf("countryFlagEmoji(%q) = %q, want %q", tt.countryCode, got, tt.want)
			}
		})
	}
}

func TestRenderNodeBlockAddsPanelCountryFlag(t *testing.T) {
	stats := monitoring.NodeStats{
		Hostname:    "veesp-se",
		Country:     "SE",
		IsUp:        true,
		BandwidthMb: 1000,
		StatusEmoji: "🟢",
	}

	block := renderNodeBlock(stats)
	if !strings.Contains(block, "<b>🇸🇪 veesp-se</b>") {
		t.Fatalf("rendered node block must include country flag from panel: %q", block)
	}
}

func TestRenderNodeBlockShowsFlagForOfflineNode(t *testing.T) {
	block := renderNodeBlock(monitoring.NodeStats{
		Hostname: "veesp-se",
		Country:  "SE",
		IsUp:     false,
	})
	if got, want := block, "<b>🇸🇪 veesp-se</b> ⚫ Оффлайн"; got != want {
		t.Fatalf("offline block = %q, want %q", got, want)
	}
}
