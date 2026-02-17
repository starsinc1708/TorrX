package ffprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"torrentstream/internal/domain"
)

type Prober struct {
	binary string
}

func New(binary string) *Prober {
	bin := strings.TrimSpace(binary)
	if bin == "" {
		bin = "ffprobe"
	}
	return &Prober{binary: bin}
}

func (p *Prober) Probe(ctx context.Context, filePath string) (domain.MediaInfo, error) {
	path := strings.TrimSpace(filePath)
	if path == "" {
		return domain.MediaInfo{}, errors.New("file path is required")
	}

	return p.runProbe(ctx, []string{
		"-v", "quiet",
		"-probesize", "100M",
		"-analyzeduration", "100M",
		"-print_format", "json",
		"-show_streams",
		"-show_format",
		path,
	}, nil)
}

func (p *Prober) ProbeReader(ctx context.Context, reader io.Reader) (domain.MediaInfo, error) {
	if reader == nil {
		return domain.MediaInfo{}, errors.New("reader is required")
	}
	return p.runProbe(ctx, []string{
		"-v", "quiet",
		"-probesize", "100M",
		"-analyzeduration", "100M",
		"-print_format", "json",
		"-show_streams",
		"-show_format",
		"-i", "pipe:0",
	}, reader)
}

const maxProbeTimeout = 30 * time.Second

func (p *Prober) runProbe(ctx context.Context, args []string, stdin io.Reader) (domain.MediaInfo, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, maxProbeTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, p.binary, args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdin = stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	var payload struct {
		Streams []struct {
			CodecType   string            `json:"codec_type"`
			CodecName   string            `json:"codec_name"`
			Tags        map[string]string `json:"tags"`
			Disposition struct {
				Default int `json:"default"`
			} `json:"disposition"`
		} `json:"streams"`
		Format struct {
			Duration  string `json:"duration"`
			StartTime string `json:"start_time"`
		} `json:"format"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		if runErr != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				return domain.MediaInfo{}, fmt.Errorf("ffprobe failed: %w", runErr)
			}
			return domain.MediaInfo{}, fmt.Errorf("ffprobe failed: %w: %s", runErr, msg)
		}
		return domain.MediaInfo{}, fmt.Errorf("ffprobe output parse failed: %w", err)
	}

	tracks := make([]domain.MediaTrack, 0)
	audioIndex := 0
	subtitleIndex := 0

	for _, stream := range payload.Streams {
		switch stream.CodecType {
		case "audio":
			tracks = append(tracks, domain.MediaTrack{
				Index:    audioIndex,
				Type:     "audio",
				Codec:    stream.CodecName,
				Language: strings.TrimSpace(getTag(stream.Tags, "language")),
				Title:    strings.TrimSpace(getTag(stream.Tags, "title")),
				Default:  stream.Disposition.Default == 1,
			})
			audioIndex++
		case "subtitle":
			tracks = append(tracks, domain.MediaTrack{
				Index:    subtitleIndex,
				Type:     "subtitle",
				Codec:    stream.CodecName,
				Language: strings.TrimSpace(getTag(stream.Tags, "language")),
				Title:    strings.TrimSpace(getTag(stream.Tags, "title")),
				Default:  stream.Disposition.Default == 1,
			})
			subtitleIndex++
		}
	}

	// ffprobe can exit with non-zero for partially downloaded files, but still
	// return usable stream metadata in stdout. Keep metadata if we have it.
	if runErr != nil && len(tracks) == 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return domain.MediaInfo{}, fmt.Errorf("ffprobe failed: %w", runErr)
		}
		return domain.MediaInfo{}, fmt.Errorf("ffprobe failed: %w: %s", runErr, msg)
	}

	var duration float64
	if payload.Format.Duration != "" {
		if d, err := strconv.ParseFloat(payload.Format.Duration, 64); err == nil && d > 0 {
			duration = d
		}
	}

	var startTime float64
	if payload.Format.StartTime != "" {
		if st, err := strconv.ParseFloat(payload.Format.StartTime, 64); err == nil && st > 0 {
			startTime = st
		}
	}

	return domain.MediaInfo{Tracks: tracks, Duration: duration, StartTime: startTime}, nil
}

func getTag(tags map[string]string, key string) string {
	if len(tags) == 0 {
		return ""
	}
	if value, ok := tags[key]; ok {
		return value
	}
	upper := strings.ToUpper(key)
	if value, ok := tags[upper]; ok {
		return value
	}
	lower := strings.ToLower(key)
	if value, ok := tags[lower]; ok {
		return value
	}
	return ""
}
