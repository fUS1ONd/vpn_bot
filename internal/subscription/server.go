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
	db       *database.DB
	clientA  *threexui.Client
	config   *config.Config
}

// New creates a new subscription server
func New(db *database.DB, clientA *threexui.Client, cfg *config.Config) *Server {
	return &Server{
		db:       db,
		clientA:  clientA,
		config:   cfg,
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

	// Generate VLESS links
	linkA, linkB := vless.GenerateLinks(clientUUID, clientName, s.config.ServerA, s.config.ServerB)
	configText := fmt.Sprintf("%s\n%s", linkA, linkB)
	configBase64 := base64.StdEncoding.EncodeToString([]byte(configText))

	// Get traffic stats from Server A (where limits are set)
	// Need to login first as session may have expired
	if err := s.clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
	}

	usedTraffic, err := s.clientA.GetClientTraffic(s.config.ServerA.InboundID, clientName)
	if err != nil {
		slog.Error("Failed to get client traffic", "error", err)
		usedTraffic = 0
	}

	limit := s.config.ServerA.LimitBytes

	// Set response headers
	w.Header().Set("Content-Disposition", `attachment; filename="fus1ond-VPN"`)
	w.Header().Set("Profile-Title", "fus1ond-VPN")
	w.Header().Set("Profile-Update-Interval", "1")
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=0; download=%d; total=%d; expire=0", usedTraffic, limit))
	w.Header().Set("Content-Type", "text/plain")

	slog.Info("Sending subscription", "client", clientName, "traffic_mb", usedTraffic/1024/1024)

	// Write base64 encoded config
	w.Write([]byte(configBase64))
}
