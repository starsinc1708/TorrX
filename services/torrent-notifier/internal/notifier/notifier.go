package notifier

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"torrentstream/notifier/internal/domain"
)

// Notifier sends library refresh requests to media servers.
type Notifier struct {
	client *http.Client
}

func New() *Notifier {
	return &Notifier{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// NotifyMediaServer sends POST /Library/Refresh to a configured media server.
// If disabled or URL is empty, it is a no-op.
func (n *Notifier) NotifyMediaServer(ctx context.Context, cfg domain.MediaServerConfig) error {
	if !cfg.Enabled || strings.TrimSpace(cfg.URL) == "" {
		return nil
	}
	url := strings.TrimRight(cfg.URL, "/") + "/Library/Refresh"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Emby-Token", cfg.APIKey)

	resp, err := n.client.Do(req)
	if err != nil {
		// Log but do not fail — completion is already persisted.
		log.Printf("notifier: POST %s failed: %v", url, err)
		return nil
	}
	defer func() {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}()
	if resp.StatusCode >= 400 {
		log.Printf("notifier: POST %s returned %d", url, resp.StatusCode)
	}
	return nil
}

// TestConnection checks if the media server is reachable and the API key works.
// Returns an error message string (empty = success).
func (n *Notifier) TestConnection(ctx context.Context, cfg domain.MediaServerConfig) string {
	if strings.TrimSpace(cfg.URL) == "" {
		return "URL is required"
	}
	url := strings.TrimRight(cfg.URL, "/") + "/Library/Refresh"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err.Error()
	}
	req.Header.Set("X-Emby-Token", cfg.APIKey)
	resp, err := n.client.Do(req)
	if err != nil {
		return err.Error()
	}
	defer func() {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusUnauthorized {
		return "invalid API key"
	}
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("server returned %d", resp.StatusCode)
	}
	return ""
}
