package ffprobe

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
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
// parseProbeOutput — unit tests with mock JSON payloads
// ---------------------------------------------------------------------------

// mkPayload builds a mock ffprobe JSON payload for testing.
func mkPayload(streams []probeStream, dur, startTime string) []byte {
	p := probePayload{
		Streams: streams,
		Format:  probeFormat{Duration: dur, StartTime: startTime},
	}
	data, _ := json.Marshal(p)
	return data
}

func mkStream(codecType, codecName string, tags map[string]string, isDefault bool) probeStream {
	def := 0
	if isDefault {
		def = 1
	}
	return probeStream{
		CodecType: codecType,
		CodecName: codecName,
		Tags:      tags,
		Disposition: struct {
			Default int `json:"default"`
		}{Default: def},
	}
}

// mkVideoStream creates a video probeStream with resolution and frame rate.
func mkVideoStream(codecName string, w, h int, frameRate string, tags map[string]string, isDefault bool) probeStream {
	s := mkStream("video", codecName, tags, isDefault)
	s.Width = w
	s.Height = h
	s.RFrameRate = frameRate
	return s
}

// mkAudioStream creates an audio probeStream with channel count.
func mkAudioStream(codecName string, channels int, tags map[string]string, isDefault bool) probeStream {
	s := mkStream("audio", codecName, tags, isDefault)
	s.Channels = channels
	return s
}

func TestParseProbeOutputVideoAudioSubtitle(t *testing.T) {
	data := mkPayload([]probeStream{
		mkStream("video", "h264", map[string]string{"language": "und"}, true),
		mkStream("audio", "aac", map[string]string{"language": "eng", "title": "English"}, true),
		mkStream("audio", "ac3", map[string]string{"language": "rus", "title": "Russian"}, false),
		mkStream("subtitle", "subrip", map[string]string{"language": "eng", "title": "English"}, true),
		mkStream("subtitle", "ass", map[string]string{"language": "jpn"}, false),
	}, "7200.500", "0.000")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Duration != 7200.5 {
		t.Fatalf("duration = %f, want 7200.5", info.Duration)
	}

	// Count tracks by type.
	counts := map[string]int{}
	for _, tr := range info.Tracks {
		counts[tr.Type]++
	}
	if counts["video"] != 1 {
		t.Fatalf("expected 1 video track, got %d", counts["video"])
	}
	if counts["audio"] != 2 {
		t.Fatalf("expected 2 audio tracks, got %d", counts["audio"])
	}
	if counts["subtitle"] != 2 {
		t.Fatalf("expected 2 subtitle tracks, got %d", counts["subtitle"])
	}

	// Verify video track.
	vt := info.Tracks[0]
	if vt.Type != "video" || vt.Codec != "h264" || vt.Index != 0 || !vt.Default {
		t.Fatalf("video track mismatch: %+v", vt)
	}

	// Verify first audio track.
	at := info.Tracks[1]
	if at.Type != "audio" || at.Codec != "aac" || at.Index != 0 || at.Language != "eng" || at.Title != "English" || !at.Default {
		t.Fatalf("audio track 0 mismatch: %+v", at)
	}

	// Verify second audio track index.
	at2 := info.Tracks[2]
	if at2.Index != 1 || at2.Codec != "ac3" || at2.Language != "rus" || at2.Default {
		t.Fatalf("audio track 1 mismatch: %+v", at2)
	}

	// Verify subtitle tracks.
	st := info.Tracks[3]
	if st.Type != "subtitle" || st.Codec != "subrip" || st.Index != 0 || st.Language != "eng" || !st.Default {
		t.Fatalf("subtitle track 0 mismatch: %+v", st)
	}
	st2 := info.Tracks[4]
	if st2.Index != 1 || st2.Codec != "ass" || st2.Language != "jpn" || st2.Default {
		t.Fatalf("subtitle track 1 mismatch: %+v", st2)
	}
}

func TestParseProbeOutputH264AAC(t *testing.T) {
	data := mkPayload([]probeStream{
		mkStream("video", "h264", nil, true),
		mkStream("audio", "aac", nil, true),
	}, "120.0", "")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasH264 := false
	hasAAC := false
	for _, tr := range info.Tracks {
		if tr.Type == "video" && tr.Codec == "h264" {
			hasH264 = true
		}
		if tr.Type == "audio" && tr.Codec == "aac" {
			hasAAC = true
		}
	}
	if !hasH264 {
		t.Fatal("expected H.264 video track")
	}
	if !hasAAC {
		t.Fatal("expected AAC audio track")
	}
	if !info.DirectPlaybackCompatible {
		t.Fatal("expected DirectPlaybackCompatible=true for h264+aac")
	}
}

func TestParseProbeOutputH265NotDirectPlayable(t *testing.T) {
	data := mkPayload([]probeStream{
		mkStream("video", "hevc", nil, true),
		mkStream("audio", "aac", nil, true),
	}, "60.0", "")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, tr := range info.Tracks {
		if tr.Type == "video" && tr.Codec == "h264" {
			t.Fatal("HEVC file should not have h264 codec")
		}
	}
	if info.Tracks[0].Codec != "hevc" {
		t.Fatalf("expected hevc codec, got %q", info.Tracks[0].Codec)
	}
	if info.DirectPlaybackCompatible {
		t.Fatal("expected DirectPlaybackCompatible=false for hevc+aac")
	}
}

func TestParseProbeOutputAudioOnly(t *testing.T) {
	data := mkPayload([]probeStream{
		mkStream("audio", "flac", map[string]string{"title": "Lossless"}, true),
	}, "300.0", "")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(info.Tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(info.Tracks))
	}
	if info.Tracks[0].Type != "audio" || info.Tracks[0].Codec != "flac" {
		t.Fatalf("unexpected track: %+v", info.Tracks[0])
	}
	if info.Tracks[0].Title != "Lossless" {
		t.Fatalf("expected title Lossless, got %q", info.Tracks[0].Title)
	}
}

func TestParseProbeOutputNoTracks(t *testing.T) {
	data := mkPayload(nil, "10.0", "")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.Tracks) != 0 {
		t.Fatalf("expected 0 tracks, got %d", len(info.Tracks))
	}
	if info.Duration != 10.0 {
		t.Fatalf("expected duration 10.0, got %f", info.Duration)
	}
}

func TestParseProbeOutputEmptyStreams(t *testing.T) {
	data := mkPayload([]probeStream{}, "", "")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.Tracks) != 0 {
		t.Fatalf("expected 0 tracks, got %d", len(info.Tracks))
	}
	if info.Duration != 0 {
		t.Fatalf("expected zero duration, got %f", info.Duration)
	}
}

func TestParseProbeOutputUnknownStreamType(t *testing.T) {
	data := mkPayload([]probeStream{
		mkStream("data", "bin_data", nil, false),
		mkStream("audio", "aac", nil, true),
	}, "5.0", "")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Unknown stream types should be skipped.
	if len(info.Tracks) != 1 {
		t.Fatalf("expected 1 track (data stream skipped), got %d", len(info.Tracks))
	}
	if info.Tracks[0].Type != "audio" {
		t.Fatalf("expected audio track, got %q", info.Tracks[0].Type)
	}
}

func TestParseProbeOutputDuration(t *testing.T) {
	tests := []struct {
		name     string
		dur      string
		wantDur  float64
		start    string
		wantStart float64
	}{
		{"normal", "120.500", 120.5, "0.050", 0.05},
		{"zero duration", "0", 0, "0", 0},
		{"negative duration", "-5.0", 0, "-1.0", 0},
		{"empty duration", "", 0, "", 0},
		{"non-numeric", "N/A", 0, "N/A", 0},
		{"large duration", "86400.123", 86400.123, "1.5", 1.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := mkPayload(nil, tc.dur, tc.start)
			info, err := parseProbeOutput(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Duration != tc.wantDur {
				t.Fatalf("duration = %f, want %f", info.Duration, tc.wantDur)
			}
			if info.StartTime != tc.wantStart {
				t.Fatalf("startTime = %f, want %f", info.StartTime, tc.wantStart)
			}
		})
	}
}

func TestParseProbeOutputInvalidJSON(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty bytes", []byte{}},
		{"not json", []byte("not json at all")},
		{"truncated json", []byte(`{"streams":`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseProbeOutput(tc.data)
			if err == nil {
				t.Fatal("expected error for invalid JSON, got nil")
			}
		})
	}
}

func TestParseProbeOutputMultipleVideoTracks(t *testing.T) {
	data := mkPayload([]probeStream{
		mkStream("video", "h264", map[string]string{"title": "Main"}, true),
		mkStream("video", "mjpeg", map[string]string{"title": "Cover"}, false),
		mkStream("audio", "aac", nil, true),
	}, "60.0", "")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	videoCount := 0
	for _, tr := range info.Tracks {
		if tr.Type == "video" {
			if tr.Index != videoCount {
				t.Fatalf("video track index = %d, want %d", tr.Index, videoCount)
			}
			videoCount++
		}
	}
	if videoCount != 2 {
		t.Fatalf("expected 2 video tracks, got %d", videoCount)
	}

	// First video is h264 (main), second is mjpeg (cover art).
	if info.Tracks[0].Codec != "h264" || info.Tracks[0].Title != "Main" {
		t.Fatalf("first video track mismatch: %+v", info.Tracks[0])
	}
	if info.Tracks[1].Codec != "mjpeg" || info.Tracks[1].Title != "Cover" {
		t.Fatalf("second video track mismatch: %+v", info.Tracks[1])
	}
}

func TestParseProbeOutputTrackIndexing(t *testing.T) {
	// Verify that each track type maintains its own independent index.
	data := mkPayload([]probeStream{
		mkStream("video", "h264", nil, true),
		mkStream("audio", "aac", nil, true),
		mkStream("audio", "ac3", nil, false),
		mkStream("subtitle", "srt", nil, false),
		mkStream("video", "mjpeg", nil, false),
		mkStream("subtitle", "ass", nil, false),
	}, "90.0", "")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantOrder := []struct {
		typ   string
		index int
	}{
		{"video", 0},
		{"audio", 0},
		{"audio", 1},
		{"subtitle", 0},
		{"video", 1},
		{"subtitle", 1},
	}

	if len(info.Tracks) != len(wantOrder) {
		t.Fatalf("expected %d tracks, got %d", len(wantOrder), len(info.Tracks))
	}
	for i, want := range wantOrder {
		got := info.Tracks[i]
		if got.Type != want.typ || got.Index != want.index {
			t.Fatalf("track[%d] = {Type:%q, Index:%d}, want {Type:%q, Index:%d}", i, got.Type, got.Index, want.typ, want.index)
		}
	}
}

func TestParseProbeOutputWhitespaceTags(t *testing.T) {
	data := mkPayload([]probeStream{
		mkStream("audio", "aac", map[string]string{
			"language": "  eng  ",
			"title":    "  Main Audio  ",
		}, true),
	}, "10.0", "")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := info.Tracks[0]
	if tr.Language != "eng" {
		t.Fatalf("expected trimmed language 'eng', got %q", tr.Language)
	}
	if tr.Title != "Main Audio" {
		t.Fatalf("expected trimmed title 'Main Audio', got %q", tr.Title)
	}
}

func TestParseProbeOutputStartTime(t *testing.T) {
	data := mkPayload([]probeStream{
		mkStream("video", "h264", nil, true),
	}, "120.0", "2.500")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.StartTime != 2.5 {
		t.Fatalf("startTime = %f, want 2.5", info.StartTime)
	}
}

func TestParseProbeOutputNullJSON(t *testing.T) {
	// null is valid JSON — Go's json.Unmarshal zeroes the struct, producing empty result.
	info, err := parseProbeOutput([]byte("null"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.Tracks) != 0 {
		t.Fatalf("expected 0 tracks for null JSON, got %d", len(info.Tracks))
	}
	if info.Duration != 0 {
		t.Fatalf("expected zero duration for null JSON, got %f", info.Duration)
	}
}

func TestParseProbeOutputMinimalValid(t *testing.T) {
	// Minimal valid ffprobe JSON: just format with no streams.
	data := []byte(`{"format":{"duration":"42.0"}}`)
	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.Tracks) != 0 {
		t.Fatalf("expected 0 tracks, got %d", len(info.Tracks))
	}
	if info.Duration != 42.0 {
		t.Fatalf("duration = %f, want 42.0", info.Duration)
	}
}

// ---------------------------------------------------------------------------
// parseFrameRate tests
// ---------------------------------------------------------------------------

func TestParseFrameRate(t *testing.T) {
	tests := []struct {
		name string
		rate string
		want float64
	}{
		{"fraction 24000/1001", "24000/1001", 24000.0 / 1001.0},
		{"fraction 30/1", "30/1", 30.0},
		{"fraction 25/1", "25/1", 25.0},
		{"integer as string", "24", 24.0},
		{"float as string", "29.97", 29.97},
		{"zero over zero", "0/0", 0},
		{"empty string", "", 0},
		{"whitespace", "  ", 0},
		{"invalid", "abc", 0},
		{"zero denominator", "30/0", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFrameRate(tc.rate)
			diff := got - tc.want
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.01 {
				t.Fatalf("parseFrameRate(%q) = %f, want %f", tc.rate, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Video resolution, FPS, and audio channels tests
// ---------------------------------------------------------------------------

func TestParseProbeOutputVideoResolutionFPS(t *testing.T) {
	data := mkPayload([]probeStream{
		mkVideoStream("h264", 1920, 1080, "24000/1001", map[string]string{"language": "und"}, true),
		mkAudioStream("aac", 6, map[string]string{"language": "eng"}, true),
	}, "7200.0", "")

	info, err := parseProbeOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vt := info.Tracks[0]
	if vt.Type != "video" {
		t.Fatalf("expected video track, got %q", vt.Type)
	}
	if vt.Width != 1920 {
		t.Fatalf("width = %d, want 1920", vt.Width)
	}
	if vt.Height != 1080 {
		t.Fatalf("height = %d, want 1080", vt.Height)
	}
	wantFPS := 24000.0 / 1001.0
	diff := vt.FPS - wantFPS
	if diff < 0 {
		diff = -diff
	}
	if diff > 0.01 {
		t.Fatalf("fps = %f, want ~%f", vt.FPS, wantFPS)
	}

	at := info.Tracks[1]
	if at.Type != "audio" {
		t.Fatalf("expected audio track, got %q", at.Type)
	}
	if at.Channels != 6 {
		t.Fatalf("channels = %d, want 6", at.Channels)
	}
}

func TestParseProbeOutputAudioChannels(t *testing.T) {
	tests := []struct {
		name     string
		channels int
	}{
		{"mono", 1},
		{"stereo", 2},
		{"5.1 surround", 6},
		{"7.1 surround", 8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := mkPayload([]probeStream{
				mkAudioStream("aac", tc.channels, nil, true),
			}, "10.0", "")

			info, err := parseProbeOutput(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Tracks[0].Channels != tc.channels {
				t.Fatalf("channels = %d, want %d", info.Tracks[0].Channels, tc.channels)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DirectPlaybackCompatible tests
// ---------------------------------------------------------------------------

func TestParseProbeOutputDirectPlaybackCompatible(t *testing.T) {
	tests := []struct {
		name    string
		streams []probeStream
		want    bool
	}{
		{
			name: "h264 + aac = compatible",
			streams: []probeStream{
				mkVideoStream("h264", 1920, 1080, "24/1", nil, true),
				mkAudioStream("aac", 2, nil, true),
			},
			want: true,
		},
		{
			name: "hevc + aac = incompatible",
			streams: []probeStream{
				mkVideoStream("hevc", 1920, 1080, "24/1", nil, true),
				mkAudioStream("aac", 2, nil, true),
			},
			want: false,
		},
		{
			name: "h264 + ac3 = incompatible",
			streams: []probeStream{
				mkVideoStream("h264", 1920, 1080, "24/1", nil, true),
				mkAudioStream("ac3", 6, nil, true),
			},
			want: false,
		},
		{
			name: "hevc + ac3 = incompatible",
			streams: []probeStream{
				mkVideoStream("hevc", 3840, 2160, "30/1", nil, true),
				mkAudioStream("ac3", 6, nil, true),
			},
			want: false,
		},
		{
			name: "audio only = incompatible",
			streams: []probeStream{
				mkAudioStream("aac", 2, nil, true),
			},
			want: false,
		},
		{
			name: "video only = incompatible",
			streams: []probeStream{
				mkVideoStream("h264", 1920, 1080, "24/1", nil, true),
			},
			want: false,
		},
		{
			name:    "no tracks = incompatible",
			streams: nil,
			want:    false,
		},
		{
			name: "h264 + multiple audio with one aac = compatible",
			streams: []probeStream{
				mkVideoStream("h264", 1920, 1080, "24/1", nil, true),
				mkAudioStream("ac3", 6, nil, true),
				mkAudioStream("aac", 2, nil, false),
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := mkPayload(tc.streams, "60.0", "")
			info, err := parseProbeOutput(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.DirectPlaybackCompatible != tc.want {
				t.Fatalf("DirectPlaybackCompatible = %v, want %v", info.DirectPlaybackCompatible, tc.want)
			}
		})
	}
}

func TestProbeNonExistentBinary(t *testing.T) {
	p := New("/nonexistent/path/to/ffprobe_does_not_exist")
	_, err := p.Probe(context.Background(), "/some/file.mkv")
	if err == nil {
		t.Fatal("expected error for non-existent binary, got nil")
	}
	if !strings.Contains(err.Error(), "ffprobe failed") {
		t.Fatalf("expected 'ffprobe failed' error, got: %v", err)
	}
}

func TestMaxProbeTimeoutConst(t *testing.T) {
	if maxProbeTimeout != 30*time.Second {
		t.Fatalf("maxProbeTimeout = %v, want 30s", maxProbeTimeout)
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

	foundVideo := false
	foundAudio := false
	for _, track := range info.Tracks {
		switch track.Type {
		case "video":
			foundVideo = true
			if track.Codec != "h264" {
				t.Fatalf("expected video codec h264, got %q", track.Codec)
			}
			if track.Width != 64 || track.Height != 64 {
				t.Fatalf("expected 64x64 resolution, got %dx%d", track.Width, track.Height)
			}
			if track.FPS <= 0 {
				t.Fatalf("expected positive FPS, got %f", track.FPS)
			}
		case "audio":
			foundAudio = true
			if track.Codec != "aac" {
				t.Fatalf("expected audio codec aac, got %q", track.Codec)
			}
			if track.Language != "eng" {
				t.Fatalf("expected audio language eng, got %q", track.Language)
			}
			if track.Channels <= 0 {
				t.Fatalf("expected positive channels, got %d", track.Channels)
			}
		}
	}
	if !foundVideo {
		t.Fatal("expected at least one video track")
	}
	if !foundAudio {
		t.Fatal("expected at least one audio track")
	}

	// H.264 + AAC = direct playback compatible.
	if !info.DirectPlaybackCompatible {
		t.Fatal("expected DirectPlaybackCompatible=true for h264+aac file")
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
