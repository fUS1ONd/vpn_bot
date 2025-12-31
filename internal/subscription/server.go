package subscription

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/threexui"
	"github.com/fus1ond/vpn_bot/internal/vless"
)

// Server handles VPN subscription requests
type Server struct {
	db      *database.DB
	clientA *threexui.Client
	clientC *threexui.Client
	config  *config.Config
}

// New creates a new subscription server
func New(db *database.DB, clientA, clientC *threexui.Client, cfg *config.Config) *Server {
	return &Server{
		db:      db,
		clientA: clientA,
		clientC: clientC,
		config:  cfg,
	}
}

// Start starts the HTTP server
func (s *Server) Start(port int) error {
	http.HandleFunc("/sub/", s.handleSubscription)

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	slog.Info("Web server started", "port", port)
	return http.ListenAndServe(addr, nil)
}

// handleSubscription handles subscription requests
func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	// Extract UUID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/sub/")
	clientUUID := strings.TrimSpace(path)

	slog.Info("Subscription request", "uuid", clientUUID)

	// Get user from database by UUID
	user, err := s.db.GetUserByUUID(clientUUID)
	if err != nil {
		slog.Error("Failed to get user", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	clientName := user.Email

	// Generate VLESS links for all servers
	linkA, linkB, linkC := vless.GenerateLinks(clientUUID, s.config.ServerA, s.config.ServerB, s.config.ServerC)
	configText := fmt.Sprintf("%s\n%s\n%s", linkA, linkB, linkC)
	configBase64 := base64.StdEncoding.EncodeToString([]byte(configText))

	// Get traffic stats from Server A (where limits are set)
	// Need to login first as session may have expired
	if err := s.clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
	}

	var usedTraffic int64
	var limit int64 = s.config.ServerA.LimitBytes
	var expire int64

	status, err := s.clientA.GetClientStatus(s.config.ServerA.InboundID, clientName)
	if err != nil {
		slog.Error("Failed to get client status", "error", err)
	} else {
		usedTraffic = status.UsedTraffic
		if status.TotalGB > 0 {
			limit = status.TotalGB
		}
		if status.ExpiryTime > 0 {
			expire = status.ExpiryTime / 1000
		}
	}

	// Set response headers
	w.Header().Set("Content-Disposition", `attachment; filename="fus1ond-VPN"`)
	w.Header().Set("Profile-Title", "fus1ond-VPN")
	w.Header().Set("Profile-Update-Interval", "1")
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d", usedTraffic, limit, expire))
	w.Header().Set("Content-Type", "text/plain")

	slog.Info("Sending subscription", "client", clientName, "traffic_mb", usedTraffic/1024/1024)

	// Write base64 encoded config
	w.Write([]byte(configBase64))
}
