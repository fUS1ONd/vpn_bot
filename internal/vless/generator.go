package vless

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/fus1ond/vpn_bot/internal/config"
)

// GenerateLinks creates VLESS subscription links for all servers
func GenerateLinks(uuid string, serverA, serverB, serverC config.ServerConfig) (linkA, linkB, linkC string) {
	// Extract IP from base URL (remove https:// and port)
	ipA := extractIP(serverA.BaseURL)
	ipB := extractIP(serverB.BaseURL)
	ipC := extractIP(serverC.BaseURL)

	// Link A (Russia cascade server with 30GB limit, exit in Germany)
	linkA = fmt.Sprintf("vless://%s@%s:443?security=reality&encryption=none&pbk=%s&fp=chrome&type=tcp&flow=xtls-rprx-vision&sni=%s&sid=%s&spx=%s#%s",
		uuid,
		ipA,
		url.QueryEscape(serverA.PublicKey),
		url.QueryEscape(serverA.SNI),
		url.QueryEscape(serverA.SID),
		url.QueryEscape("/"),
		url.QueryEscape("🇷🇺→🇩🇪 | 30GB"),
	)

	// Link B (Germany server unlimited)
	linkB = fmt.Sprintf("vless://%s@%s:443?security=reality&encryption=none&pbk=%s&fp=chrome&type=tcp&flow=xtls-rprx-vision&sni=%s&sid=%s&spx=%s#%s",
		uuid,
		ipB,
		url.QueryEscape(serverB.PublicKey),
		url.QueryEscape(serverB.SNI),
		url.QueryEscape(serverB.SID),
		url.QueryEscape("/"),
		url.QueryEscape("🇩🇪 DE | ∞"),
	)

	// Link C (Netherlands server unlimited)
	linkC = fmt.Sprintf("vless://%s@%s:443?security=reality&encryption=none&pbk=%s&fp=chrome&type=tcp&flow=xtls-rprx-vision&sni=%s&sid=%s&spx=%s#%s",
		uuid,
		ipC,
		url.QueryEscape(serverC.PublicKey),
		url.QueryEscape(serverC.SNI),
		url.QueryEscape(serverC.SID),
		url.QueryEscape("/"),
		url.QueryEscape("🇳🇱 NL | ∞"),
	)

	return linkA, linkB, linkC
}

// extractIP extracts IP address from URL (removes https:// and :port)
func extractIP(baseURL string) string {
	// Remove https:// or http://
	ip := strings.TrimPrefix(baseURL, "https://")
	ip = strings.TrimPrefix(ip, "http://")

	// Remove port if present
	if idx := strings.Index(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}

	return ip
}
