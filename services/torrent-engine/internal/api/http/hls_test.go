package apihttp

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
	"torrentstream/internal/usecase"
)

// ---------------------------------------------------------------------------
// hls_types.go — pure utility functions
// ---------------------------------------------------------------------------

func TestComputeProfileHash(t *testing.T) {
	tests := []struct {
		name         string
		preset       string
		crf          int
		audioBitrate string
		segDur       int
	}{
		{"default", "veryfast", 23, "128k", 2},
		{"medium", "medium", 28, "192k", 4},
		{"zero-crf", "ultrafast", 0, "96k", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := computeProfileHash(tc.preset, tc.crf, tc.audioBitrate, tc.segDur)
			if len(h) != 8 {
				t.Fatalf("hash length = %d, want 8", len(h))
			}
			// Deterministic: same inputs → same output.
			h2 := computeProfileHash(tc.preset, tc.crf, tc.audioBitrate, tc.segDur)
			if h != h2 {
				t.Fatalf("non-deterministic: %q != %q", h, h2)
			}
		})
	}
	// Different inputs → different hashes.
	h1 := computeProfileHash("veryfast", 23, "128k", 2)
	h2 := computeProfileHash("medium", 23, "128k", 2)
	if h1 == h2 {
		t.Fatalf("different presets should produce different hashes")
	}
}

func TestComputeVariants(t *testing.T) {
	tests := []struct {
		name         string
		sourceHeight int
		wantCount    int
		wantNil      bool
	}{
		{"1080p source", 1080, 3, false},
		{"720p source", 720, 2, false},
		{"480p source", 480, 0, true},     // single variant → nil
		{"360p source", 360, 0, true},     // below all presets
		{"4k source", 2160, 3, false},     // >= all presets
		{"exact 720", 720, 2, false},      // 480 + 720
		{"just under 720", 719, 0, true},  // only 480 → single variant → nil
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			variants := computeVariants(tc.sourceHeight)
			if tc.wantNil {
				if variants != nil {
					t.Fatalf("expected nil, got %d variants", len(variants))
				}
				return
			}
			if len(variants) != tc.wantCount {
				t.Fatalf("got %d variants, want %d", len(variants), tc.wantCount)
			}
			// Last variant should use CRF (empty VideoBitrate).
			last := variants[len(variants)-1]
			if last.VideoBitrate != "" {
				t.Fatalf("last variant should have empty VideoBitrate (CRF mode), got %q", last.VideoBitrate)
			}
			// Non-last variants should have bitrate set.
			for i := 0; i < len(variants)-1; i++ {
				if variants[i].VideoBitrate == "" {
					t.Fatalf("variant %d should have VideoBitrate set", i)
				}
			}
		})
	}
}

func TestQualityPresetValues(t *testing.T) {
	// Verify PRD-specified quality presets: 480p/1.5Mbps, 720p/3Mbps, 1080p/6Mbps.
	if len(qualityPresets) != 3 {
		t.Fatalf("expected 3 quality presets, got %d", len(qualityPresets))
	}
	expected := []struct {
		height  int
		bitrate string
	}{
		{480, "1500k"},
		{720, "3000k"},
		{1080, "6000k"},
	}
	for i, e := range expected {
		if qualityPresets[i].Height != e.height {
			t.Fatalf("preset[%d].Height = %d, want %d", i, qualityPresets[i].Height, e.height)
		}
		if qualityPresets[i].VideoBitrate != e.bitrate {
			t.Fatalf("preset[%d].VideoBitrate = %q, want %q", i, qualityPresets[i].VideoBitrate, e.bitrate)
		}
	}
}

func TestPlaylistHasEndList(t *testing.T) {
	dir := t.TempDir()

	// File with ENDLIST.
	complete := filepath.Join(dir, "complete.m3u8")
	os.WriteFile(complete, []byte("#EXTM3U\n#EXTINF:4,\nseg.ts\n#EXT-X-ENDLIST\n"), 0644)
	if !playlistHasEndList(complete) {
		t.Fatal("expected true for playlist with ENDLIST")
	}

	// File without ENDLIST.
	live := filepath.Join(dir, "live.m3u8")
	os.WriteFile(live, []byte("#EXTM3U\n#EXTINF:4,\nseg.ts\n"), 0644)
	if playlistHasEndList(live) {
		t.Fatal("expected false for playlist without ENDLIST")
	}

	// Non-existent file.
	if playlistHasEndList(filepath.Join(dir, "nonexist.m3u8")) {
		t.Fatal("expected false for non-existent file")
	}
}

func TestSafeSegmentPath(t *testing.T) {
	base := filepath.Join(os.TempDir(), "hls-test-base")

	tests := []struct {
		name    string
		segment string
		wantErr bool
	}{
		{"valid segment", "seg-00001.ts", false},
		{"variant segment", "v0/seg-00001.ts", false},
		{"path traversal", "../../../etc/passwd", true},
		{"absolute path", "/etc/passwd", true},
		{"double dot in name", "..hidden", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := safeSegmentPath(base, tc.segment)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got path %q", tc.segment, result)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for %q: %v", tc.segment, err)
				}
				if !strings.HasPrefix(result, base) {
					t.Fatalf("result %q should start with base %q", result, base)
				}
			}
		})
	}
}

func TestSubtitleFilterArg(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		track    int
		wantPart string
	}{
		{"simple path", "/data/movie.mkv", 0, "subtitles='/data/movie.mkv':si=0"},
		{"track 2", "/data/movie.mkv", 2, ":si=2"},
		{"colon in path", "C:/data/movie.mkv", 0, `C\:/data/movie.mkv`},
		{"backslash in path", `C:\data\movie.mkv`, 0, "C\\:/data/movie.mkv"},
		{"single quote in path", "/data/movie's.mkv", 0, `movie\'s.mkv`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := subtitleFilterArg(tc.path, tc.track)
			if !strings.Contains(result, tc.wantPart) {
				t.Fatalf("subtitleFilterArg(%q, %d) = %q, missing %q", tc.path, tc.track, result, tc.wantPart)
			}
		})
	}
}

func TestBuildMultiVariantFilterComplex(t *testing.T) {
	variants := []qualityVariant{
		{Height: 480}, {Height: 720}, {Height: 1080},
	}

	// Without subtitles.
	fc := buildMultiVariantFilterComplex(variants, "", -1)
	if !strings.Contains(fc, "split=3") {
		t.Fatalf("expected split=3, got %q", fc)
	}
	if !strings.Contains(fc, "[v0]") || !strings.Contains(fc, "[v1]") || !strings.Contains(fc, "[v2]") {
		t.Fatalf("missing split labels in %q", fc)
	}
	if !strings.Contains(fc, "scale=-2:480") {
		t.Fatalf("missing 480p scale in %q", fc)
	}
	if !strings.Contains(fc, "scale=-2:720") {
		t.Fatalf("missing 720p scale in %q", fc)
	}
	// Last variant uses null filter (no scale).
	if !strings.Contains(fc, "[v2]null[out2]") {
		t.Fatalf("last variant should use null filter, got %q", fc)
	}
	// Should NOT contain subtitles filter.
	if strings.Contains(fc, "subtitles=") {
		t.Fatalf("should not contain subtitles filter when track=-1")
	}

	// With subtitles.
	fc2 := buildMultiVariantFilterComplex(variants, "/data/movie.mkv", 1)
	if !strings.Contains(fc2, "subtitles=") {
		t.Fatalf("expected subtitles filter, got %q", fc2)
	}
	if !strings.Contains(fc2, ":si=1") {
		t.Fatalf("expected subtitle index 1, got %q", fc2)
	}
}

func TestParseM3U8Segments(t *testing.T) {
	dir := t.TempDir()
	playlist := filepath.Join(dir, "index.m3u8")

	content := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n" +
		"#EXTINF:4.000,\nseg-00000.ts\n" +
		"#EXTINF:3.500,\nseg-00001.ts\n" +
		"#EXTINF:2.100,\nseg-00002.ts\n" +
		"#EXT-X-ENDLIST\n"
	os.WriteFile(playlist, []byte(content), 0644)

	segments, err := parseM3U8Segments(playlist)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segments))
	}
	if segments[0].Filename != "seg-00000.ts" || segments[0].Duration != 4.0 {
		t.Fatalf("segment[0] = %+v", segments[0])
	}
	if segments[1].Duration != 3.5 {
		t.Fatalf("segment[1].Duration = %f, want 3.5", segments[1].Duration)
	}
	if segments[2].Filename != "seg-00002.ts" {
		t.Fatalf("segment[2].Filename = %q", segments[2].Filename)
	}

	// Non-existent file.
	_, err = parseM3U8Segments(filepath.Join(dir, "nonexist.m3u8"))
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}

	// Empty playlist.
	empty := filepath.Join(dir, "empty.m3u8")
	os.WriteFile(empty, []byte("#EXTM3U\n"), 0644)
	segs, err := parseM3U8Segments(empty)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(segs) != 0 {
		t.Fatalf("expected 0 segments for empty playlist, got %d", len(segs))
	}
}

func TestEstimateByteOffset(t *testing.T) {
	tests := []struct {
		name      string
		targetSec float64
		durSec    float64
		fileLen   int64
		want      int64
	}{
		{"mid-point", 50.0, 100.0, 1000, 500},
		{"start", 0.0, 100.0, 1000, -1},           // targetSec <= 0
		{"end", 100.0, 100.0, 1000, 1000},          // ratio = 1.0
		{"over-end", 150.0, 100.0, 1000, 1000},     // clamped to 1.0
		{"zero duration", 50.0, 0.0, 1000, -1},     // invalid
		{"zero file length", 50.0, 100.0, 0, -1},   // invalid
		{"negative target", -5.0, 100.0, 1000, -1},  // invalid
		{"quarter", 25.0, 100.0, 4000, 1000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := estimateByteOffset(tc.targetSec, tc.durSec, tc.fileLen)
			if got != tc.want {
				t.Fatalf("estimateByteOffset(%f, %f, %d) = %d, want %d",
					tc.targetSec, tc.durSec, tc.fileLen, got, tc.want)
			}
		})
	}
}

func TestSeekModeString(t *testing.T) {
	tests := []struct {
		mode SeekMode
		want string
	}{
		{SeekModeSoft, "soft"},
		{SeekModeHard, "hard"},
		{SeekMode(99), "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.mode.String(); got != tc.want {
				t.Fatalf("SeekMode(%d).String() = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

func TestRemuxCacheKey(t *testing.T) {
	key := remuxCacheKey("abc123", 2)
	if key != "abc123/2" {
		t.Fatalf("remuxCacheKey = %q, want %q", key, "abc123/2")
	}
}

func TestFindLastSegment(t *testing.T) {
	dir := t.TempDir()

	// Empty directory.
	path, size := findLastSegment(dir)
	if path != "" || size != 0 {
		t.Fatalf("expected empty for empty dir, got (%q, %d)", path, size)
	}

	// Single segment.
	seg1 := filepath.Join(dir, "seg-00000.ts")
	os.WriteFile(seg1, []byte("data1"), 0644)
	time.Sleep(10 * time.Millisecond)

	path, size = findLastSegment(dir)
	if path != seg1 || size != 5 {
		t.Fatalf("expected (%q, 5), got (%q, %d)", seg1, path, size)
	}

	// Non-.ts files ignored.
	os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("playlist"), 0644)
	path, size = findLastSegment(dir)
	if path != seg1 {
		t.Fatalf("non-.ts files should be ignored, got %q", path)
	}

	// Variant subdirectory.
	v0Dir := filepath.Join(dir, "v0")
	os.MkdirAll(v0Dir, 0755)
	time.Sleep(10 * time.Millisecond)
	seg2 := filepath.Join(v0Dir, "seg-00001.ts")
	os.WriteFile(seg2, []byte("longer-data"), 0644)

	path, size = findLastSegment(dir)
	if path != seg2 || size != 11 {
		t.Fatalf("expected variant segment (%q, 11), got (%q, %d)", seg2, path, size)
	}

	// Non-existent directory.
	path, size = findLastSegment(filepath.Join(dir, "nonexist"))
	if path != "" || size != 0 {
		t.Fatalf("expected empty for non-existent dir")
	}
}

// ---------------------------------------------------------------------------
// streaming_ffmpeg.go — FFmpegArgConfig and buildStreamingFFmpegArgs
// ---------------------------------------------------------------------------

func TestBuildStreamingFFmpegArgsSingleVariant(t *testing.T) {
	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		FFmpegPath:      "ffmpeg",
		Input:           "/data/movie.mkv",
		OutputDir:       "/tmp/hls",
		SegmentDuration: 4,
		Preset:          "veryfast",
		CRF:             23,
		AudioBitrate:    "128k",
		IsLocalFile:     true,
	})

	joined := strings.Join(args, " ")

	// Should contain basic flags.
	if !strings.Contains(joined, "-hide_banner") {
		t.Fatal("missing -hide_banner")
	}
	if !strings.Contains(joined, "-f hls") {
		t.Fatal("missing -f hls")
	}
	if !strings.Contains(joined, "-hls_time 4") {
		t.Fatal("missing -hls_time 4")
	}
	if !strings.Contains(joined, "-c:v libx264") {
		t.Fatal("missing -c:v libx264")
	}
	if !strings.Contains(joined, "-preset veryfast") {
		t.Fatal("missing -preset")
	}
	if !strings.Contains(joined, "-crf 23") {
		t.Fatal("missing -crf")
	}
	if !strings.Contains(joined, "-c:a aac") {
		t.Fatal("missing -c:a aac")
	}
	if !strings.Contains(joined, "-b:a 128k") {
		t.Fatal("missing -b:a 128k")
	}
	// Single variant should NOT have master playlist.
	if strings.Contains(joined, "master.m3u8") {
		t.Fatal("single variant should not have master playlist")
	}
}

func TestBuildStreamingFFmpegArgsStreamCopy(t *testing.T) {
	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		Input:           "/data/movie.mp4",
		SegmentDuration: 2,
		AudioBitrate:    "128k",
		StreamCopy:      true,
		IsAACSource:     true,
		IsLocalFile:     true,
	})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v copy") {
		t.Fatal("missing -c:v copy for stream copy mode")
	}
	if !strings.Contains(joined, "-c:a copy") {
		t.Fatal("missing -c:a copy for AAC source in stream copy mode")
	}
}

func TestBuildStreamingFFmpegArgsStreamCopyNonAAC(t *testing.T) {
	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		Input:           "/data/movie.mp4",
		SegmentDuration: 2,
		AudioBitrate:    "128k",
		StreamCopy:      true,
		IsAACSource:     false,
		IsLocalFile:     true,
	})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v copy") {
		t.Fatal("missing -c:v copy for stream copy mode")
	}
	// Non-AAC should transcode audio.
	if strings.Contains(joined, "-c:a copy") {
		t.Fatal("non-AAC source should not use -c:a copy")
	}
	if !strings.Contains(joined, "-c:a aac") {
		t.Fatal("missing -c:a aac for non-AAC source")
	}
}

func TestBuildStreamingFFmpegArgsMultiVariant(t *testing.T) {
	variants := []qualityVariant{
		{Height: 480, VideoBitrate: "1500k", MaxRate: "2000k", BufSize: "3000k"},
		{Height: 720, VideoBitrate: "3000k", MaxRate: "4000k", BufSize: "6000k"},
		{Height: 1080, VideoBitrate: "", MaxRate: "7500k", BufSize: "12000k"},
	}
	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		Input:           "/data/movie.mkv",
		SegmentDuration: 2,
		Preset:          "fast",
		CRF:             25,
		AudioBitrate:    "192k",
		MultiVariant:    true,
		Variants:        variants,
		SourceHeight:    1080,
		IsLocalFile:     true,
	})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-filter_complex") {
		t.Fatal("missing -filter_complex for multi-variant")
	}
	if !strings.Contains(joined, "master.m3u8") {
		t.Fatal("missing master.m3u8 for multi-variant")
	}
	if !strings.Contains(joined, "-var_stream_map") {
		t.Fatal("missing -var_stream_map for multi-variant")
	}
	// Check per-variant bitrate.
	if !strings.Contains(joined, "-b:v:0 1500k") {
		t.Fatal("missing bitrate for variant 0")
	}
	if !strings.Contains(joined, "-b:v:1 3000k") {
		t.Fatal("missing bitrate for variant 1")
	}
	// Last variant uses CRF, not bitrate.
	if strings.Contains(joined, "-b:v:2") {
		t.Fatal("last variant should use CRF, not bitrate")
	}
}

func TestBuildStreamingFFmpegArgsSeek(t *testing.T) {
	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		Input:           "pipe:0",
		SegmentDuration: 2,
		SeekSeconds:     30.5,
		Preset:          "veryfast",
		CRF:             23,
		AudioBitrate:    "128k",
		UseReader:       true,
	})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-ss 30.500") {
		t.Fatal("missing -ss seek flag")
	}
	// Pipe source should use smaller probe settings.
	if !strings.Contains(joined, "-analyzeduration 5000000") {
		t.Fatal("pipe source should use smaller analyzeduration")
	}
}

func TestBuildStreamingFFmpegArgsSubtitles(t *testing.T) {
	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		Input:           "/data/movie.mkv",
		SegmentDuration: 2,
		Preset:          "veryfast",
		CRF:             23,
		AudioBitrate:    "128k",
		SubtitleTrack:   1,
		SubtitleFile:    "/data/movie.mkv",
		IsLocalFile:     true,
	})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-vf") {
		t.Fatal("missing -vf for subtitle burn-in")
	}
	if !strings.Contains(joined, "subtitles=") {
		t.Fatal("missing subtitles filter")
	}
}

func TestBuildStreamingFFmpegArgsDefaultSegDur(t *testing.T) {
	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		Input:           "/data/movie.mkv",
		SegmentDuration: 0, // should default to 2
		Preset:          "veryfast",
		CRF:             23,
		AudioBitrate:    "128k",
		IsLocalFile:     true,
	})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-hls_time 2") {
		t.Fatal("default segment duration should be 2")
	}
}

func TestBuildStreamingFFmpegArgsHTTPInput(t *testing.T) {
	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		Input:           "http://example.com/video.ts",
		SegmentDuration: 2,
		Preset:          "veryfast",
		CRF:             23,
		AudioBitrate:    "128k",
	})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-reconnect 1") {
		t.Fatal("HTTP input should have -reconnect flags")
	}
}

func TestBuildStreamingFFmpegArgsAudioTrack(t *testing.T) {
	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		Input:           "/data/movie.mkv",
		SegmentDuration: 2,
		Preset:          "veryfast",
		CRF:             23,
		AudioBitrate:    "128k",
		AudioTrack:      2,
		IsLocalFile:     true,
	})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "0:a:2?") {
		t.Fatal("should map audio track 2")
	}
}

func TestBuildStreamingFFmpegArgsFPSKeyframes(t *testing.T) {
	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		Input:           "/data/movie.mkv",
		SegmentDuration: 2,
		Preset:          "veryfast",
		CRF:             23,
		AudioBitrate:    "128k",
		SourceFPS:       24.0,
		IsLocalFile:     true,
	})

	joined := strings.Join(args, " ")
	// With known FPS, should use -g (GOP size) instead of force_key_frames.
	if !strings.Contains(joined, "-g 48") {
		t.Fatalf("expected -g 48 for 24fps@2s segments, got: %s", joined)
	}
	if !strings.Contains(joined, "-sc_threshold 0") {
		t.Fatal("missing -sc_threshold for FPS-based keyframes")
	}
}

// ---------------------------------------------------------------------------
// streaming_rambuf.go — RAMBuffer
// ---------------------------------------------------------------------------

func TestRAMBufferBasicReadWrite(t *testing.T) {
	data := []byte("hello, world! this is test data for the RAM buffer")
	source := io.NopCloser(bytes.NewReader(data))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	buf := NewRAMBuffer(source, 1024, logger)
	defer buf.Close()

	// Read all data.
	result := make([]byte, len(data))
	n, err := io.ReadFull(buf, result)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if n != len(data) {
		t.Fatalf("read %d bytes, want %d", n, len(data))
	}
	if !bytes.Equal(result, data) {
		t.Fatalf("data mismatch")
	}
}

func TestRAMBufferPrebuffer(t *testing.T) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	source := io.NopCloser(bytes.NewReader(data))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	buf := NewRAMBuffer(source, 4096, logger)
	defer buf.Close()

	err := buf.Prebuffer(context.Background(), 512, 5*time.Second)
	if err != nil {
		t.Fatalf("prebuffer error: %v", err)
	}

	buffered := buf.Buffered()
	if buffered < 512 {
		t.Fatalf("buffered = %d, want >= 512", buffered)
	}
}

func TestRAMBufferClear(t *testing.T) {
	data := []byte("some data to buffer")
	source := io.NopCloser(bytes.NewReader(data))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	buf := NewRAMBuffer(source, 1024, logger)
	defer buf.Close()

	// Wait for some data to be buffered.
	time.Sleep(50 * time.Millisecond)

	buf.Clear()
	if buf.Buffered() != 0 {
		t.Fatalf("after Clear(), buffered = %d, want 0", buf.Buffered())
	}
}

func TestRAMBufferClose(t *testing.T) {
	pr, pw := io.Pipe()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	buf := NewRAMBuffer(pr, 1024, logger)

	// Write some data.
	go func() {
		pw.Write([]byte("data"))
		time.Sleep(50 * time.Millisecond)
		pw.Close()
	}()

	err := buf.Close()
	if err != nil {
		t.Fatalf("close error: %v", err)
	}

	// Reads after close should return error.
	p := make([]byte, 10)
	_, err = buf.Read(p)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestRAMBufferDefaultSize(t *testing.T) {
	source := io.NopCloser(bytes.NewReader(nil))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	buf := NewRAMBuffer(source, 0, logger)
	defer buf.Close()

	if buf.size != defaultRAMBufSize {
		t.Fatalf("default size = %d, want %d", buf.size, defaultRAMBufSize)
	}
}

func TestRAMBufferContextCancellation(t *testing.T) {
	pr, _ := io.Pipe() // never closed — blocks forever
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	buf := NewRAMBuffer(pr, 1024, logger)
	defer buf.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	err := buf.Prebuffer(ctx, 1024, 5*time.Second)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// streaming_fsm.go — StreamState, StreamJob, WindowConfig
// ---------------------------------------------------------------------------

func TestStreamStateString(t *testing.T) {
	tests := []struct {
		state StreamState
		want  string
	}{
		{StreamIdle, "idle"},
		{StreamLoading, "loading"},
		{StreamReady, "ready"},
		{StreamPlaying, "playing"},
		{StreamBuffering, "buffering"},
		{StreamSeeking, "seeking"},
		{StreamCompleted, "completed"},
		{StreamError, "error"},
		{StreamState(99), "unknown(99)"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.state.String(); got != tc.want {
				t.Fatalf("StreamState(%d).String() = %q, want %q", tc.state, got, tc.want)
			}
		})
	}
}

func TestDefaultWindowConfig(t *testing.T) {
	cfg := DefaultWindowConfig()
	if cfg.RAMBufSize != 16<<20 {
		t.Fatalf("RAMBufSize = %d, want %d", cfg.RAMBufSize, 16<<20)
	}
	if cfg.PreloadBytes != 4<<20 {
		t.Fatalf("PreloadBytes = %d, want %d", cfg.PreloadBytes, 4<<20)
	}
	if cfg.BeforeBytes != 8<<20 {
		t.Fatalf("BeforeBytes = %d, want %d", cfg.BeforeBytes, 8<<20)
	}
	if cfg.AfterBytes != 32<<20 {
		t.Fatalf("AfterBytes = %d, want %d", cfg.AfterBytes, 32<<20)
	}
}

func TestStreamJobIsRunning(t *testing.T) {
	tests := []struct {
		state StreamState
		want  bool
	}{
		{StreamIdle, false},
		{StreamLoading, true},
		{StreamReady, true},
		{StreamPlaying, true},
		{StreamBuffering, true},
		{StreamSeeking, false},
		{StreamCompleted, false},
		{StreamError, false},
	}
	for _, tc := range tests {
		t.Run(tc.state.String(), func(t *testing.T) {
			job := &StreamJob{state: tc.state}
			if got := job.IsRunning(); got != tc.want {
				t.Fatalf("IsRunning() with state %s = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}

func TestStreamJobIsCompleted(t *testing.T) {
	job := &StreamJob{state: StreamCompleted}
	if !job.IsCompleted() {
		t.Fatal("expected IsCompleted() = true")
	}
	job.state = StreamPlaying
	if job.IsCompleted() {
		t.Fatal("expected IsCompleted() = false for Playing state")
	}
}

func TestStreamJobSignalReady(t *testing.T) {
	job := &StreamJob{ready: make(chan struct{})}
	job.signalReady()

	select {
	case <-job.ready:
		// OK
	default:
		t.Fatal("ready channel should be closed")
	}

	// Double call should not panic.
	job.signalReady()
}

func TestStreamJobCheckSeekRequested(t *testing.T) {
	job := &StreamJob{}

	// No seek requested.
	_, ok := job.checkSeekRequested()
	if ok {
		t.Fatal("expected no seek request")
	}

	// Set a seek request.
	job.Seek(42.5)
	target, ok := job.checkSeekRequested()
	if !ok {
		t.Fatal("expected seek request")
	}
	if target != 42.5 {
		t.Fatalf("seek target = %f, want 42.5", target)
	}

	// Should be cleared after check.
	_, ok = job.checkSeekRequested()
	if ok {
		t.Fatal("seek should be cleared after check")
	}
}

func TestStreamJobStartPlaybackIdempotent(t *testing.T) {
	// Creating a minimal job that will fail on Loading (no manager set up).
	// The key behavior: StartPlayback should be a no-op if state != Idle.
	job := &StreamJob{
		state: StreamPlaying,
		ready: make(chan struct{}),
	}
	job.StartPlayback() // Should be a no-op.
	if job.state != StreamPlaying {
		t.Fatal("StartPlayback should not change non-Idle state")
	}
}

// ---------------------------------------------------------------------------
// streaming_priority.go — PriorityManager
// ---------------------------------------------------------------------------

type mockEngine struct {
	mu        sync.Mutex
	calls     []setPriorityCall
	stateErr  error
	state     domain.SessionState
}

type setPriorityCall struct {
	id     domain.TorrentID
	file   domain.FileRef
	rng    domain.Range
	prio   domain.Priority
}

func (e *mockEngine) SetPiecePriority(_ context.Context, id domain.TorrentID, file domain.FileRef, rng domain.Range, prio domain.Priority) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, setPriorityCall{id, file, rng, prio})
	return nil
}

func (e *mockEngine) GetSessionState(_ context.Context, id domain.TorrentID) (domain.SessionState, error) {
	return e.state, e.stateErr
}

// Stub out remaining Engine interface methods (not used by PriorityManager).
func (e *mockEngine) Open(context.Context, domain.TorrentSource) (ports.Session, error) { return nil, nil }
func (e *mockEngine) Close() error                                                       { return nil }
func (e *mockEngine) GetSession(context.Context, domain.TorrentID) (ports.Session, error) { return nil, nil }
func (e *mockEngine) ListActiveSessions(context.Context) ([]domain.TorrentID, error)     { return nil, nil }
func (e *mockEngine) ListSessions(context.Context) ([]domain.TorrentID, error)           { return nil, nil }
func (e *mockEngine) StopSession(context.Context, domain.TorrentID) error                { return nil }
func (e *mockEngine) StartSession(context.Context, domain.TorrentID) error               { return nil }
func (e *mockEngine) RemoveSession(context.Context, domain.TorrentID) error              { return nil }
func (e *mockEngine) FocusSession(context.Context, domain.TorrentID) error               { return nil }
func (e *mockEngine) UnfocusAll(context.Context) error                                   { return nil }
func (e *mockEngine) GetSessionMode(context.Context, domain.TorrentID) (domain.SessionMode, error) {
	return domain.ModeIdle, nil
}
func (e *mockEngine) SetDownloadRateLimit(context.Context, domain.TorrentID, int64) error { return nil }

func TestPriorityManagerApply(t *testing.T) {
	eng := &mockEngine{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	file := domain.FileRef{Length: 100 << 20} // 100 MB
	pm := NewPriorityManager(eng, "t1", file, logger)

	pm.Apply(context.Background(), 0, 50<<20)

	eng.mu.Lock()
	defer eng.mu.Unlock()
	if len(eng.calls) == 0 {
		t.Fatal("expected priority calls")
	}

	// Should have High, Next, Readahead bands + header/tail protection.
	var highCalls, nextCalls, readaheadCalls, normalCalls int
	for _, c := range eng.calls {
		switch c.prio {
		case domain.PriorityHigh:
			highCalls++
		case domain.PriorityNext:
			nextCalls++
		case domain.PriorityReadahead:
			readaheadCalls++
		case domain.PriorityNormal:
			normalCalls++
		}
	}
	if highCalls == 0 {
		t.Fatal("expected high priority calls")
	}
	if nextCalls == 0 {
		t.Fatal("expected next priority calls")
	}
}

func TestPriorityManagerDeprioritize(t *testing.T) {
	eng := &mockEngine{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	file := domain.FileRef{Length: 50 << 20}
	pm := NewPriorityManager(eng, "t1", file, logger)

	pm.Deprioritize(context.Background())

	eng.mu.Lock()
	defer eng.mu.Unlock()
	if len(eng.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(eng.calls))
	}
	if eng.calls[0].prio != domain.PriorityNone {
		t.Fatalf("expected PriorityNone, got %d", eng.calls[0].prio)
	}
	if eng.calls[0].rng.Length != 50<<20 {
		t.Fatalf("expected full file length, got %d", eng.calls[0].rng.Length)
	}
}

func TestPriorityManagerNilEngine(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pm := NewPriorityManager(nil, "t1", domain.FileRef{Length: 100}, logger)

	// Should not panic.
	pm.Apply(context.Background(), 0, 100)
	pm.EnhanceHigh(context.Background(), 0)
	pm.Deprioritize(context.Background())
}

func TestPriorityManagerZeroLengthFile(t *testing.T) {
	eng := &mockEngine{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pm := NewPriorityManager(eng, "t1", domain.FileRef{Length: 0}, logger)

	pm.Apply(context.Background(), 0, 100)
	eng.mu.Lock()
	defer eng.mu.Unlock()
	if len(eng.calls) != 0 {
		t.Fatalf("expected no calls for zero-length file, got %d", len(eng.calls))
	}
}

func TestPriorityManagerEnhanceHigh(t *testing.T) {
	eng := &mockEngine{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	file := domain.FileRef{Length: 100 << 20}
	pm := NewPriorityManager(eng, "t1", file, logger)

	pm.EnhanceHigh(context.Background(), 10<<20)

	eng.mu.Lock()
	defer eng.mu.Unlock()
	if len(eng.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(eng.calls))
	}
	if eng.calls[0].prio != domain.PriorityHigh {
		t.Fatalf("expected PriorityHigh, got %d", eng.calls[0].prio)
	}
	// Enhanced high band = 3 × 4MB = 12MB.
	if eng.calls[0].rng.Length != 12<<20 {
		t.Fatalf("enhanced high band = %d, want %d", eng.calls[0].rng.Length, 12<<20)
	}
}

func TestPriorityManagerBoundaryProtection(t *testing.T) {
	eng := &mockEngine{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// File big enough for boundary protection (> 16MB).
	file := domain.FileRef{Length: 100 << 20}
	pm := NewPriorityManager(eng, "t1", file, logger)

	// Apply window in the middle.
	pm.Apply(context.Background(), 30<<20, 60<<20)

	eng.mu.Lock()
	defer eng.mu.Unlock()

	// Check that first and last 8MB got PriorityNormal (protection).
	var hasHeadProtect, hasTailProtect bool
	for _, c := range eng.calls {
		if c.prio == domain.PriorityNormal && c.rng.Off == 0 && c.rng.Length == 8<<20 {
			hasHeadProtect = true
		}
		tailStart := int64(100<<20 - 8<<20)
		if c.prio == domain.PriorityNormal && c.rng.Off == tailStart && c.rng.Length == 8<<20 {
			hasTailProtect = true
		}
	}
	if !hasHeadProtect {
		t.Fatal("expected head boundary protection (first 8MB at PriorityNormal)")
	}
	if !hasTailProtect {
		t.Fatal("expected tail boundary protection (last 8MB at PriorityNormal)")
	}
}

// ---------------------------------------------------------------------------
// streaming_manager.go — StreamJobManager
// ---------------------------------------------------------------------------

func TestStreamJobManagerEncodingSettings(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newStreamJobManager(nil, nil, HLSConfig{
		Preset:       "fast",
		CRF:          28,
		AudioBitrate: "192k",
	}, logger)

	if mgr.EncodingPreset() != "fast" {
		t.Fatalf("preset = %q, want fast", mgr.EncodingPreset())
	}
	if mgr.EncodingCRF() != 28 {
		t.Fatalf("crf = %d, want 28", mgr.EncodingCRF())
	}
	if mgr.EncodingAudioBitrate() != "192k" {
		t.Fatalf("audioBitrate = %q, want 192k", mgr.EncodingAudioBitrate())
	}

	mgr.UpdateEncodingSettings("medium", 30, "256k")
	if mgr.EncodingPreset() != "medium" {
		t.Fatalf("updated preset = %q, want medium", mgr.EncodingPreset())
	}
	if mgr.EncodingCRF() != 30 {
		t.Fatalf("updated crf = %d, want 30", mgr.EncodingCRF())
	}
	if mgr.EncodingAudioBitrate() != "256k" {
		t.Fatalf("updated audioBitrate = %q, want 256k", mgr.EncodingAudioBitrate())
	}
}

func TestStreamJobManagerEncodingSettingsDefaults(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newStreamJobManager(nil, nil, HLSConfig{}, logger)

	if mgr.EncodingPreset() != "veryfast" {
		t.Fatalf("default preset = %q, want veryfast", mgr.EncodingPreset())
	}
	if mgr.EncodingCRF() != 23 {
		t.Fatalf("default crf = %d, want 23", mgr.EncodingCRF())
	}
	if mgr.EncodingAudioBitrate() != "128k" {
		t.Fatalf("default audioBitrate = %q, want 128k", mgr.EncodingAudioBitrate())
	}
	if mgr.SegmentDuration() != 2 {
		t.Fatalf("default segDur = %d, want 2", mgr.SegmentDuration())
	}
}

func TestStreamJobManagerHLSSettings(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newStreamJobManager(nil, nil, HLSConfig{
		SegmentDuration: 4,
		RAMBufSizeMB:    32,
		PrebufferMB:     8,
		WindowBeforeMB:  16,
		WindowAfterMB:   64,
	}, logger)

	if mgr.SegmentDuration() != 4 {
		t.Fatalf("segDur = %d, want 4", mgr.SegmentDuration())
	}
	if mgr.RAMBufSizeBytes() != 32<<20 {
		t.Fatalf("ramBuf = %d, want %d", mgr.RAMBufSizeBytes(), 32<<20)
	}
	if mgr.PrebufferBytes() != 8<<20 {
		t.Fatalf("prebuf = %d, want %d", mgr.PrebufferBytes(), 8<<20)
	}
	if mgr.WindowBeforeBytes() != 16<<20 {
		t.Fatalf("winBefore = %d, want %d", mgr.WindowBeforeBytes(), 16<<20)
	}
	if mgr.WindowAfterBytes() != 64<<20 {
		t.Fatalf("winAfter = %d, want %d", mgr.WindowAfterBytes(), 64<<20)
	}
}

func TestStreamJobManagerProfileHash(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newStreamJobManager(nil, nil, HLSConfig{
		Preset:          "veryfast",
		CRF:             23,
		AudioBitrate:    "128k",
		SegmentDuration: 2,
	}, logger)

	hash := mgr.profileHash()
	if len(hash) != 8 {
		t.Fatalf("hash length = %d, want 8", len(hash))
	}

	// Changing settings should change hash.
	mgr.UpdateEncodingSettings("medium", 0, "")
	hash2 := mgr.profileHash()
	if hash == hash2 {
		t.Fatal("hash should change after encoding settings update")
	}
}

func TestStreamJobManagerBuildJobDir(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newStreamJobManager(nil, nil, HLSConfig{
		BaseDir: dir,
	}, logger)

	key := hlsKey{id: "torrent123", fileIndex: 0, audioTrack: 1, subtitleTrack: 2}
	jobDir := mgr.buildJobDir(key)

	if !strings.HasPrefix(jobDir, dir) {
		t.Fatalf("job dir %q should start with base dir %q", jobDir, dir)
	}
	if !strings.Contains(jobDir, "torrent123") {
		t.Fatal("job dir should contain torrent ID")
	}
	if !strings.Contains(jobDir, "a1-s2-p") {
		t.Fatal("job dir should contain audio/subtitle/profile info")
	}
}

func TestStreamJobManagerHealthSnapshot(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newStreamJobManager(nil, nil, HLSConfig{}, logger)

	snap := mgr.healthSnapshot()
	if snap.ActiveJobs != 0 {
		t.Fatalf("activeJobs = %d, want 0", snap.ActiveJobs)
	}
	if snap.TotalJobStarts != 0 {
		t.Fatalf("totalJobStarts = %d, want 0", snap.TotalJobStarts)
	}
}

func TestStreamJobManagerShutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newStreamJobManager(nil, nil, HLSConfig{}, logger)

	// Should not panic on empty manager.
	mgr.shutdown()

	if len(mgr.jobs) != 0 {
		t.Fatal("jobs should be empty after shutdown")
	}
}

func TestStreamJobManagerEnsureJobNilStream(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newStreamJobManager(nil, nil, HLSConfig{}, logger)

	_, err := mgr.EnsureJob("t1", 0, 0, -1)
	if err == nil {
		t.Fatal("expected error for nil stream use case")
	}
}

func TestStreamJobManagerCountRunningJobs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newStreamJobManager(nil, nil, HLSConfig{}, logger)

	if mgr.countRunningJobs() != 0 {
		t.Fatal("expected 0 running jobs")
	}
}

func TestStreamJobManagerChooseSeekMode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newStreamJobManager(nil, nil, HLSConfig{}, logger)
	key := hlsKey{id: "t1", fileIndex: 0}

	// Nil job → hard seek.
	mode := mgr.chooseSeekMode(key, nil, 10.0, 2)
	if mode != SeekModeHard {
		t.Fatalf("nil job → %s, want hard", mode)
	}

	// Small distance → soft seek.
	job := &StreamJob{seekSeconds: 10.0}
	mode = mgr.chooseSeekMode(key, job, 12.0, 4)
	if mode != SeekModeSoft {
		t.Fatalf("small distance → %s, want soft", mode)
	}

	// Large distance → hard seek.
	mode = mgr.chooseSeekMode(key, job, 100.0, 4)
	if mode != SeekModeHard {
		t.Fatalf("large distance → %s, want hard", mode)
	}
}

// ---------------------------------------------------------------------------
// hls_datasource.go — MediaDataSource implementations
// ---------------------------------------------------------------------------

func TestDirectFileSourceInterface(t *testing.T) {
	ds := &directFileSource{path: "/data/movie.mp4"}

	input, pipeReader := ds.InputSpec()
	if input != "/data/movie.mp4" {
		t.Fatalf("InputSpec() = %q, want /data/movie.mp4", input)
	}
	if pipeReader != nil {
		t.Fatal("pipeReader should be nil for file source")
	}
	if ds.SupportsSeek() {
		t.Fatal("directFileSource should not support seek")
	}
	if err := ds.SeekTo(0); err != nil {
		t.Fatalf("SeekTo should be no-op, got error: %v", err)
	}
	if err := ds.Close(); err != nil {
		t.Fatalf("Close with nil reader should not error: %v", err)
	}
}

func TestDirectFileSourceCloseWithReader(t *testing.T) {
	pr, _ := io.Pipe()
	ds := &directFileSource{path: "/data/movie.mp4", reader: pr}

	err := ds.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

func TestStreamPipeSourceInterface(t *testing.T) {
	source := io.NopCloser(bytes.NewReader([]byte("data")))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ramBuf := NewRAMBuffer(source, 1024, logger)

	ds := &streamPipeSource{buf: ramBuf}

	input, pipeReader := ds.InputSpec()
	if input != "pipe:0" {
		t.Fatalf("InputSpec() = %q, want pipe:0", input)
	}
	if pipeReader == nil {
		t.Fatal("pipeReader should be non-nil for pipe source")
	}
	if ds.SupportsSeek() {
		t.Fatal("streamPipeSource should not support seek")
	}

	ds.Close()
}

func TestDataSourceFilePath(t *testing.T) {
	fileDS := &directFileSource{path: "/data/movie.mp4"}
	if p := dataSourceFilePath(fileDS); p != "/data/movie.mp4" {
		t.Fatalf("dataSourceFilePath(file) = %q, want /data/movie.mp4", p)
	}

	source := io.NopCloser(bytes.NewReader(nil))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pipeDS := &streamPipeSource{buf: NewRAMBuffer(source, 64, logger)}
	defer pipeDS.Close()
	if p := dataSourceFilePath(pipeDS); p != "" {
		t.Fatalf("dataSourceFilePath(pipe) = %q, want empty", p)
	}
}

// ---------------------------------------------------------------------------
// handlers_streaming.go — rewritePlaylistSegmentURLs and HLS handler tests
// ---------------------------------------------------------------------------

func TestRewritePlaylistSegmentURLsNoSubtitle(t *testing.T) {
	input := []byte("#EXTM3U\n#EXTINF:4.0,\nseg-00001.ts\n#EXTINF:4.0,\nseg-00002.ts\n")
	output := string(rewritePlaylistSegmentURLs(input, 1, -1))

	// With subtitleTrack=-1, only audioTrack should appear.
	if !strings.Contains(output, "seg-00001.ts?audioTrack=1") {
		t.Fatalf("expected audioTrack=1 in output: %s", output)
	}
	if strings.Contains(output, "subtitleTrack") {
		t.Fatalf("subtitleTrack should not appear when track=-1: %s", output)
	}
}

func TestRewritePlaylistSegmentURLsZeroAudio(t *testing.T) {
	input := []byte("#EXTM3U\n#EXTINF:4.0,\nseg-00001.ts\n")
	output := string(rewritePlaylistSegmentURLs(input, 0, -1))

	// audioTrack=0 should still be set (default track).
	if !strings.Contains(output, "audioTrack=0") {
		t.Fatalf("expected audioTrack=0: %s", output)
	}
}

func TestRewritePlaylistSegmentURLsComments(t *testing.T) {
	input := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:4.0,\nseg-00001.ts\n")
	output := string(rewritePlaylistSegmentURLs(input, 0, 0))

	// Comment lines should NOT have query params appended.
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "#") && strings.Contains(line, "audioTrack") {
			t.Fatalf("comment line should not have query params: %q", line)
		}
	}
}

func TestRewritePlaylistMasterVariants(t *testing.T) {
	// Master playlist with variant stream URIs.
	input := []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1500000\nv0/index.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=3000000\nv1/index.m3u8\n")
	output := string(rewritePlaylistSegmentURLs(input, 1, 2))

	if !strings.Contains(output, "v0/index.m3u8?audioTrack=1&subtitleTrack=2") {
		t.Fatalf("expected rewritten variant URL: %s", output)
	}
}

// ---------------------------------------------------------------------------
// HLS handler HTTP integration tests
// ---------------------------------------------------------------------------

// nopStreamReader is a no-op implementation of ports.StreamReader for tests.
type nopStreamReader struct{}

func (n *nopStreamReader) Read([]byte) (int, error)        { return 0, io.EOF }
func (n *nopStreamReader) Seek(int64, int) (int64, error)  { return 0, nil }
func (n *nopStreamReader) Close() error                    { return nil }
func (n *nopStreamReader) SetContext(context.Context)      {}
func (n *nopStreamReader) SetReadahead(int64)              {}
func (n *nopStreamReader) SetResponsive()                  {}

func TestHLSNotConfigured(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})
	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/hls/0/index.m3u8", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestHLSInvalidFileIndex(t *testing.T) {
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			File:   domain.FileRef{Path: "movie.mkv", Length: 1000},
			Reader: &nopStreamReader{},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))
	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/hls/abc/index.m3u8", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHLSTooFewPathSegments(t *testing.T) {
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			File:   domain.FileRef{Path: "movie.mkv", Length: 1000},
			Reader: &nopStreamReader{},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))
	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/hls/", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHLSSeekMissingTime(t *testing.T) {
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			File:   domain.FileRef{Path: "movie.mkv", Length: 1000},
			Reader: &nopStreamReader{},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))
	req := httptest.NewRequest(http.MethodPost, "/torrents/t1/hls/0/seek", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHLSSeekInvalidTime(t *testing.T) {
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			File:   domain.FileRef{Path: "movie.mkv", Length: 1000},
			Reader: &nopStreamReader{},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))
	req := httptest.NewRequest(http.MethodPost, "/torrents/t1/hls/0/seek?time=abc", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHLSSeekNegativeTime(t *testing.T) {
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			File:   domain.FileRef{Path: "movie.mkv", Length: 1000},
			Reader: &nopStreamReader{},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))
	req := httptest.NewRequest(http.MethodPost, "/torrents/t1/hls/0/seek?time=-5", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHLSSeekMethodNotAllowed(t *testing.T) {
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			File:   domain.FileRef{Path: "movie.mkv", Length: 1000},
			Reader: &nopStreamReader{},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))
	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/hls/0/seek?time=10", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

func TestHLSSegmentPathTraversal(t *testing.T) {
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			File:   domain.FileRef{Path: "movie.mkv", Length: 1000},
			Reader: &nopStreamReader{},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))

	// This tests the segment serving path — should detect path traversal.
	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/hls/0/../../etc/passwd", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	// Should not return 200.
	if w.Code == http.StatusOK {
		t.Fatal("path traversal should not succeed")
	}
}
