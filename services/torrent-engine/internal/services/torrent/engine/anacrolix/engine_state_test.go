package anacrolix

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent"

	"torrentstream/internal/domain"
)

func TestSampleSpeedFirstCallZero(t *testing.T) {
	engine := &Engine{speeds: make(map[domain.TorrentID]speedSample)}
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)

	download, upload := engine.sampleSpeed("t1", statsWithCounts(100, 50), now)
	if download != 0 || upload != 0 {
		t.Fatalf("expected 0 speeds, got %d/%d", download, upload)
	}
}

func TestSampleSpeedDelta(t *testing.T) {
	engine := &Engine{speeds: make(map[domain.TorrentID]speedSample)}
	start := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	_, _ = engine.sampleSpeed("t1", statsWithCounts(100, 50), start)

	next := start.Add(2 * time.Second)
	download, upload := engine.sampleSpeed("t1", statsWithCounts(1100, 450), next)
	if download != 500 {
		t.Fatalf("download = %d", download)
	}
	if upload != 200 {
		t.Fatalf("upload = %d", upload)
	}
}

func TestSampleSpeedNonPositiveDelta(t *testing.T) {
	engine := &Engine{speeds: make(map[domain.TorrentID]speedSample)}
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	_, _ = engine.sampleSpeed("t1", statsWithCounts(100, 50), now)

	download, upload := engine.sampleSpeed("t1", statsWithCounts(200, 100), now)
	if download != 0 || upload != 0 {
		t.Fatalf("expected 0 speeds, got %d/%d", download, upload)
	}
}

func statsWithCounts(read, written int64) torrent.TorrentStats {
	var stats torrent.TorrentStats
	stats.BytesReadUsefulData.Add(read)
	stats.BytesWrittenData.Add(written)
	return stats
}
