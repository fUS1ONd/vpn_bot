package vless

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/fus1ond/vpn_bot/internal/config"
)

// GenerateLinks creates VLESS subscription links for both servers
func GenerateLinks(uuid, email string, serverA, serverB config.ServerConfig) (linkA, linkB string) {
	// Extract IP from base URL (remove https:// and port)
	ipA := extractIP(serverA.BaseURL)
	ipB := extractIP(serverB.BaseURL)

	// Link A (Russia server with 30GB limit)
	linkA = fmt.Sprintf("vless://%s@%s:443?security=reality&encryption=none&pbk=%s&fp=chrome&type=tcp&flow=xtls-rprx-vision&sni=%s&sid=%s&spx=%s#%s",
		uuid,
		ipA,
		url.QueryEscape(serverA.PublicKey),
		url.QueryEscape(serverA.SNI),
		url.QueryEscape(serverA.SID),
		url.QueryEscape("/"),
		url.QueryEscape(fmt.Sprintf("🇷🇺 %s | 30GB", email)),
	)

	// Link B (Germany server unlimited)
	linkB = fmt.Sprintf("vless://%s@%s:443?security=reality&encryption=none&pbk=%s&fp=chrome&type=tcp&flow=xtls-rprx-vision&sni=%s&sid=%s&spx=%s#%s",
		uuid,
		ipB,
		url.QueryEscape(serverB.PublicKey),
		url.QueryEscape(serverB.SNI),
		url.QueryEscape(serverB.SID),
		url.QueryEscape("/"),
		url.QueryEscape(fmt.Sprintf("🇩🇪 %s | FR", email)),
	)

	return linkA, linkB
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
