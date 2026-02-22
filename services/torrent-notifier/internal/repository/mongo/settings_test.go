package mongorepo_test

import (
	"testing"

	"torrentstream/notifier/internal/domain"
)

func TestIntegrationSettings_Defaults(t *testing.T) {
	s := domain.IntegrationSettings{}
	if s.Jellyfin.Enabled {
		t.Error("Jellyfin should be disabled by default")
	}
	if s.Emby.Enabled {
		t.Error("Emby should be disabled by default")
	}
	if s.QBT.Enabled {
		t.Error("QBT should be disabled by default")
	}
}

func TestIntegrationSettings_Fields(t *testing.T) {
	s := domain.IntegrationSettings{
		Jellyfin: domain.MediaServerConfig{
			Enabled: true,
			URL:     "http://jellyfin:8096",
			APIKey:  "testkey",
		},
	}
	if s.Jellyfin.URL != "http://jellyfin:8096" {
		t.Errorf("unexpected URL: %s", s.Jellyfin.URL)
	}
}
