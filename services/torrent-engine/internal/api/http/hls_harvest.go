package apihttp

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// harvestSegmentsToCache parses the m3u8 playlist(s) in dir, computes time
// offsets from seekSeconds + cumulative EXTINF durations, and copies each
// segment file into the cache. For multi-variant jobs, each variant directory
// is harvested independently.
func (m *hlsManager) harvestSegmentsToCache(key hlsKey, dir string, seekSeconds float64) {
	if m.cache == nil {
		return
	}

	type variantInfo struct {
		dir      string
		playlist string
		variant  string
	}
	var variantsToParse []variantInfo

	// Check for multi-variant (master.m3u8 exists).
	masterPlaylist := filepath.Join(dir, "master.m3u8")
	if _, err := os.Stat(masterPlaylist); err == nil {
		for i := 0; ; i++ {
			vDir := filepath.Join(dir, fmt.Sprintf("v%d", i))
			vPlaylist := filepath.Join(vDir, "index.m3u8")
			if _, err := os.Stat(vPlaylist); err != nil {
				break
			}
			variantsToParse = append(variantsToParse, variantInfo{
				dir: vDir, playlist: vPlaylist, variant: fmt.Sprintf("v%d", i),
			})
		}
	}
	if len(variantsToParse) == 0 {
		variantsToParse = append(variantsToParse, variantInfo{
			dir: dir, playlist: filepath.Join(dir, "index.m3u8"), variant: "",
		})
	}

	for _, vi := range variantsToParse {
		segments, err := parseM3U8Segments(vi.playlist)
		if err != nil {
			continue
		}
		cacheV := m.cacheVariant(vi.variant)
		cumTime := seekSeconds
		for _, seg := range segments {
			startTime := cumTime
			endTime := cumTime + seg.Duration
			srcPath := filepath.Join(vi.dir, seg.Filename)
			if _, err := os.Stat(srcPath); err != nil {
				cumTime = endTime
				continue
			}
			if err := m.cache.Store(
				string(key.id), key.fileIndex, key.audioTrack, key.subtitleTrack,
				cacheV, startTime, endTime, srcPath,
			); err != nil {
				m.logger.Warn("hls cache store failed",
					slog.String("segment", seg.Filename),
					slog.String("variant", vi.variant),
					slog.String("error", err.Error()),
				)
			} else if m.memBuf != nil {
				if raw, readErr := os.ReadFile(srcPath); readErr == nil {
					cachePath := m.cache.SegmentPath(
						string(key.id), key.fileIndex, key.audioTrack, key.subtitleTrack,
						cacheV, startTime, endTime,
					)
					m.memBuf.Put(cachePath, raw)
				}
			}
			cumTime = endTime
		}
	}
}

// cacheSegmentsLive runs alongside an active ffmpeg job, periodically
// parsing the growing m3u8 playlist(s) and caching new segments as they
// appear. For multi-variant jobs, each variant playlist is tracked
// independently.
func (m *hlsManager) cacheSegmentsLive(job *hlsJob, key hlsKey) {
	if m.cache == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	type variantState struct {
		dir      string
		playlist string
		variant  string
		cached   int
	}

	var variants []*variantState
	initialized := false

	for {
		select {
		case <-job.ctx.Done():
			return
		case <-ticker.C:
			// Lazy-initialize variant list after ffmpeg has started and
			// potentially set job.multiVariant.
			if !initialized {
				if job.multiVariant {
					for i := range job.variants {
						vDir := filepath.Join(job.dir, fmt.Sprintf("v%d", i))
						variants = append(variants, &variantState{
							dir:      vDir,
							playlist: filepath.Join(vDir, "index.m3u8"),
							variant:  fmt.Sprintf("v%d", i),
						})
					}
				} else {
					variants = append(variants, &variantState{
						dir:      job.dir,
						playlist: job.playlist,
						variant:  "",
					})
				}
				initialized = true
			}

			for _, vs := range variants {
				segments, err := parseM3U8Segments(vs.playlist)
				if err != nil || len(segments) <= vs.cached {
					continue
				}
				cacheV := m.cacheVariant(vs.variant)
				cumTime := job.seekSeconds
				for i := 0; i < vs.cached && i < len(segments); i++ {
					cumTime += segments[i].Duration
				}
				for i := vs.cached; i < len(segments); i++ {
					seg := segments[i]
					startTime := cumTime
					endTime := cumTime + seg.Duration
					srcPath := filepath.Join(vs.dir, seg.Filename)
					if _, statErr := os.Stat(srcPath); statErr == nil {
						_ = m.cache.Store(
							string(key.id), key.fileIndex, key.audioTrack, key.subtitleTrack,
							cacheV, startTime, endTime, srcPath,
						)
						if m.memBuf != nil {
							if raw, readErr := os.ReadFile(srcPath); readErr == nil {
								m.memBuf.Put(srcPath, raw)
							}
						}
					}
					cumTime = endTime
				}
				vs.cached = len(segments)
			}
		}
	}
}
