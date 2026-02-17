package ffprobe

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Unit tests — no ffprobe binary needed
// ---------------------------------------------------------------------------

func TestProbeEmptyPath(t *testing.T) {
	p := New("")
	tests := []struct {
		name string
		path string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Probe(context.Background(), tc.path)
			if err == nil {
				t.Fatal("expected error for empty path, got nil")
			}
			if err.Error() != "file path is required" {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestProbeReaderNilReader(t *testing.T) {
	p := New("")
	_, err := p.ProbeReader(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil reader, got nil")
	}
	if err.Error() != "reader is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetTagCaseInsensitive(t *testing.T) {
	tests := []struct {
		name string
		tags map[string]string
		key  string
		want string
	}{
		{
			name: "exact match",
			tags: map[string]string{"language": "eng"},
			key:  "language",
			want: "eng",
		},
		{
			name: "uppercase match",
			tags: map[string]string{"LANGUAGE": "eng"},
			key:  "language",
			want: "eng",
		},
		{
			name: "lowercase match from mixed key",
			tags: map[string]string{"title": "Director's Commentary"},
			key:  "TITLE",
			want: "Director's Commentary",
		},
		{
			name: "no match",
			tags: map[string]string{"codec": "aac"},
			key:  "language",
			want: "",
		},
		{
			name: "exact takes priority over upper",
			tags: map[string]string{"language": "exact", "LANGUAGE": "upper"},
			key:  "language",
			want: "exact",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getTag(tc.tags, tc.key)
			if got != tc.want {
				t.Fatalf("getTag(%v, %q) = %q, want %q", tc.tags, tc.key, got, tc.want)
			}
		})
	}
}

func TestGetTagEmptyMap(t *testing.T) {
	tests := []struct {
		name string
		tags map[string]string
	}{
		{"nil map", nil},
		{"empty map", map[string]string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getTag(tc.tags, "language")
			if got != "" {
				t.Fatalf("getTag(%v, \"language\") = %q, want empty string", tc.tags, got)
			}
		})
	}
}

func TestNewDefaultBinary(t *testing.T) {
	tests := []struct {
		name   string
		binary string
		want   string
	}{
		{"empty defaults to ffprobe", "", "ffprobe"},
		{"whitespace defaults to ffprobe", "   ", "ffprobe"},
		{"custom binary preserved", "/usr/local/bin/ffprobe", "/usr/local/bin/ffprobe"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := New(tc.binary)
			if p.binary != tc.want {
				t.Fatalf("New(%q).binary = %q, want %q", tc.binary, p.binary, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration tests — skipped when ffprobe is unavailable
// ---------------------------------------------------------------------------

func ffprobeAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe binary not available, skipping integration test")
	}
}

func TestProbeValidFile(t *testing.T) {
	ffprobeAvailable(t)

	// Generate a minimal test file with ffmpeg if available.
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg binary not available, cannot generate test fixture")
	}

	tmpFile := t.TempDir() + "/test.mkv"
	cmd := exec.Command(ffmpegPath,
		"-f", "lavfi", "-i", "testsrc=duration=1:size=64x64:rate=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac",
		"-metadata:s:a:0", "language=eng",
		"-metadata:s:a:0", "title=English",
		"-y", tmpFile,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg failed to create test file: %v\n%s", err, out)
	}

	p := New("")
	info, err := p.Probe(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("Probe() error: %v", err)
	}

	if info.Duration <= 0 {
		t.Fatalf("expected positive duration, got %f", info.Duration)
	}

	foundAudio := false
	for _, track := range info.Tracks {
		if track.Type == "audio" {
			foundAudio = true
			if track.Codec != "aac" {
				t.Fatalf("expected audio codec aac, got %q", track.Codec)
			}
			if track.Language != "eng" {
				t.Fatalf("expected audio language eng, got %q", track.Language)
			}
		}
	}
	if !foundAudio {
		t.Fatal("expected at least one audio track")
	}
}

func TestProbeTimeout(t *testing.T) {
	ffprobeAvailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Let the tiny timeout expire.
	time.Sleep(2 * time.Millisecond)

	p := New("")
	_, err := p.Probe(ctx, "/dev/null")
	if err == nil {
		t.Fatal("expected error from expired context, got nil")
	}
}
