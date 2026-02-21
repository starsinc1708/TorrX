package apihttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"torrentstream/internal/domain"
)

func TestSubtitleVTTRoute(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg binary not available")
	}

	dir := t.TempDir()
	videoPath := buildSubtitleFixture(t, ffmpegPath, dir)
	info, statErr := os.Stat(videoPath)
	if statErr != nil {
		t.Fatalf("stat fixture: %v", statErr)
	}

	repo := &fakeRepo{
		get: domain.TorrentRecord{
			ID: "t1",
			Files: []domain.FileRef{
				{
					Index:          0,
					Path:           filepath.Base(videoPath),
					Length:         info.Size(),
					BytesCompleted: info.Size(),
				},
			},
		},
	}

	server := NewServer(
		&fakeCreateTorrent{},
		WithRepository(repo),
		WithStreamTorrent(&fakeStreamTorrent{}),
		WithMediaProbe(&fakeMediaProbe{}, dir),
	)
	server.hls.ffmpegPath = ffmpegPath

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/subtitles/0/0.vtt", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(strings.ToLower(got), "text/vtt") {
		t.Fatalf("content type = %q, want text/vtt", got)
	}
	if !strings.Contains(rec.Body.String(), "WEBVTT") {
		t.Fatalf("response body should contain WEBVTT header, got: %s", rec.Body.String())
	}
}

func TestSubtitleVTTRouteMissingTrack(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg binary not available")
	}

	dir := t.TempDir()
	videoPath := buildSubtitleFixture(t, ffmpegPath, dir)
	info, statErr := os.Stat(videoPath)
	if statErr != nil {
		t.Fatalf("stat fixture: %v", statErr)
	}

	repo := &fakeRepo{
		get: domain.TorrentRecord{
			ID: "t1",
			Files: []domain.FileRef{
				{
					Index:          0,
					Path:           filepath.Base(videoPath),
					Length:         info.Size(),
					BytesCompleted: info.Size(),
				},
			},
		},
	}

	server := NewServer(
		&fakeCreateTorrent{},
		WithRepository(repo),
		WithStreamTorrent(&fakeStreamTorrent{}),
		WithMediaProbe(&fakeMediaProbe{}, dir),
	)
	server.hls.ffmpegPath = ffmpegPath

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/subtitles/0/99.vtt", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func buildSubtitleFixture(t *testing.T, ffmpegPath, dir string) string {
	t.Helper()

	srtPath := filepath.Join(dir, "subs.srt")
	srtData := "1\n00:00:00,000 --> 00:00:00,900\nHello subtitle\n"
	if err := os.WriteFile(srtPath, []byte(srtData), 0o644); err != nil {
		t.Fatalf("write srt: %v", err)
	}

	outPath := filepath.Join(dir, "movie.mkv")
	cmd := exec.Command(
		ffmpegPath,
		"-y",
		"-f", "lavfi",
		"-i", "color=c=black:s=320x240:d=1",
		"-f", "lavfi",
		"-i", "anullsrc=r=48000:cl=stereo",
		"-i", srtPath,
		"-shortest",
		"-c:v", "libx264",
		"-c:a", "aac",
		"-c:s", "srt",
		outPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg fixture generation failed: %v\n%s", err, string(out))
	}
	return outPath
}
