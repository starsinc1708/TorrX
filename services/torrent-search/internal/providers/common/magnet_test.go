package common

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// NormalizeInfoHash
// ---------------------------------------------------------------------------

func TestNormalizeInfoHash(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercase hex", "abcdef1234567890", "abcdef1234567890"},
		{"uppercase hex", "ABCDEF1234567890", "abcdef1234567890"},
		{"mixed case", "AbCdEf1234567890", "abcdef1234567890"},
		{"with urn:btih: prefix", "urn:btih:abcdef1234567890", "abcdef1234567890"},
		{"with URN:BTIH: prefix uppercase", "URN:BTIH:ABCDEF1234567890", "abcdef1234567890"},
		{"empty string", "", ""},
		{"whitespace only", "   ", ""},
		{"whitespace around hash", "  abcdef1234567890  ", "abcdef1234567890"},
		{"urn:btih: only", "urn:btih:", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeInfoHash(tc.input)
			if got != tc.want {
				t.Errorf("NormalizeInfoHash(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildMagnet
// ---------------------------------------------------------------------------

func TestBuildMagnetBasic(t *testing.T) {
	magnet := BuildMagnet("abcdef1234567890", "Test Torrent", nil)
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:abcdef1234567890") {
		t.Fatalf("unexpected magnet: %s", magnet)
	}
	if !strings.Contains(magnet, "dn=Test+Torrent") {
		t.Fatalf("expected encoded name in magnet: %s", magnet)
	}
}

func TestBuildMagnetWithTrackers(t *testing.T) {
	trackers := []string{"udp://tracker1:1337", "udp://tracker2:6969"}
	magnet := BuildMagnet("abcdef1234567890", "Test", trackers)
	trCount := strings.Count(magnet, "&tr=")
	if trCount != 2 {
		t.Fatalf("expected 2 tracker params, got %d in: %s", trCount, magnet)
	}
}

func TestBuildMagnetEmptyInfoHash(t *testing.T) {
	magnet := BuildMagnet("", "Test", nil)
	if magnet != "" {
		t.Fatalf("expected empty magnet for empty hash, got: %s", magnet)
	}
}

func TestBuildMagnetEmptyName(t *testing.T) {
	magnet := BuildMagnet("abcdef1234567890", "", nil)
	if strings.Contains(magnet, "dn=") {
		t.Fatalf("expected no dn= param for empty name: %s", magnet)
	}
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:") {
		t.Fatalf("unexpected magnet format: %s", magnet)
	}
}

func TestBuildMagnetWhitespaceName(t *testing.T) {
	magnet := BuildMagnet("abcdef1234567890", "   ", nil)
	if strings.Contains(magnet, "dn=") {
		t.Fatalf("expected no dn= param for whitespace-only name: %s", magnet)
	}
}

func TestBuildMagnetEmptyTrackers(t *testing.T) {
	trackers := []string{"", "  ", ""}
	magnet := BuildMagnet("abcdef1234567890", "Test", trackers)
	if strings.Contains(magnet, "&tr=") {
		t.Fatalf("expected no tracker params for empty trackers: %s", magnet)
	}
}

func TestBuildMagnetNormalizesHash(t *testing.T) {
	magnet := BuildMagnet("ABCDEF1234567890", "Test", nil)
	if !strings.Contains(magnet, "urn:btih:abcdef1234567890") {
		t.Fatalf("expected normalized (lowercase) hash in magnet: %s", magnet)
	}
}

func TestBuildMagnetWithUrnPrefix(t *testing.T) {
	magnet := BuildMagnet("urn:btih:abcdef1234567890", "Test", nil)
	// Should not double the prefix
	if strings.Contains(magnet, "urn:btih:urn:btih:") {
		t.Fatalf("double urn:btih prefix in magnet: %s", magnet)
	}
	if !strings.Contains(magnet, "urn:btih:abcdef1234567890") {
		t.Fatalf("expected normalized hash: %s", magnet)
	}
}

func TestBuildMagnetSpecialCharsInName(t *testing.T) {
	magnet := BuildMagnet("abcdef1234567890", "Test & Torrent [2024]", nil)
	// The name should be URL-encoded
	if strings.Contains(magnet, "Test & Torrent") {
		t.Fatalf("expected URL-encoded name in magnet: %s", magnet)
	}
	if !strings.Contains(magnet, "dn=") {
		t.Fatalf("expected dn= param: %s", magnet)
	}
}

func TestBuildMagnetMixedTrackers(t *testing.T) {
	trackers := []string{"udp://valid:1337", "", "udp://also-valid:6969", "  "}
	magnet := BuildMagnet("abcdef1234567890", "Test", trackers)
	trCount := strings.Count(magnet, "&tr=")
	if trCount != 2 {
		t.Fatalf("expected 2 tracker params (skipping empty), got %d in: %s", trCount, magnet)
	}
}
