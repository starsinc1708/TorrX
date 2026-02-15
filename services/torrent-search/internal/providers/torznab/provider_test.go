package torznab

import (
	"context"
	"strings"
	"testing"

	"torrentstream/searchservice/internal/domain"
)

func TestParseTorznabResponseReadsNamespacedAttrs(t *testing.T) {
	payload := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <item>
      <title>Dark.S01E01.1080p.WEB-DL.RUS</title>
      <guid>magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&amp;dn=Dark</guid>
      <pubDate>Fri, 13 Feb 2026 12:00:00 +0000</pubDate>
      <torznab:attr name="seeders" value="123"/>
      <torznab:attr name="peers" value="150"/>
      <torznab:attr name="size" value="1073741824"/>
      <torznab:attr name="infohash" value="0123456789ABCDEF0123456789ABCDEF01234567"/>
      <torznab:attr name="tracker" value="rutracker.org"/>
      <torznab:attr name="indexer" value="rutracker"/>
    </item>
  </channel>
</rss>`)

	items, err := parseTorznabResponse(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if len(items[0].Attrs) == 0 {
		t.Fatalf("expected torznab:attr elements to be parsed")
	}
}

func TestItemToResultBuildsCoreFields(t *testing.T) {
	payload := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <item>
      <title>Dark.S01E01.1080p.WEB-DL.RUS</title>
      <guid>magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&amp;dn=Dark</guid>
      <pubDate>Fri, 13 Feb 2026 12:00:00 +0000</pubDate>
      <torznab:attr name="seeders" value="123"/>
      <torznab:attr name="peers" value="150"/>
      <torznab:attr name="size" value="1073741824"/>
      <torznab:attr name="infohash" value="0123456789ABCDEF0123456789ABCDEF01234567"/>
      <torznab:attr name="tracker" value="rutracker.org"/>
      <torznab:attr name="indexer" value="rutracker"/>
    </item>
  </channel>
</rss>`)

	items, err := parseTorznabResponse(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	provider := NewProvider(Config{
		Name:   "jackett",
		Label:  "Jackett (Torznab)",
		Kind:   "indexer",
		APIKey: "dummy",
	})

	result, ok := provider.itemToResult(context.Background(), items[0], "jackett.local")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if result.Name == "" {
		t.Fatalf("expected name")
	}
	if result.InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("unexpected infohash: %q", result.InfoHash)
	}
	if !strings.HasPrefix(strings.ToLower(result.Magnet), "magnet:?") {
		t.Fatalf("expected magnet, got %q", result.Magnet)
	}
	if result.Seeders != 123 {
		t.Fatalf("expected seeders=123, got %d", result.Seeders)
	}
	if result.Leechers != 27 {
		t.Fatalf("expected leechers=27, got %d", result.Leechers)
	}
	if result.SizeBytes != 1073741824 {
		t.Fatalf("expected sizeBytes=1073741824, got %d", result.SizeBytes)
	}
	if result.Source != "rutracker" {
		t.Fatalf("expected source=rutracker, got %q", result.Source)
	}
	if result.Tracker != "rutracker.org" {
		t.Fatalf("expected tracker=rutracker.org, got %q", result.Tracker)
	}
	if result.PublishedAt == nil {
		t.Fatalf("expected publishedAt")
	}
}

func TestItemToResultBuildsMagnetWhenOnlyInfoHashPresent(t *testing.T) {
	item := torznabItem{
		Title: "Dark.S01E01.1080p.WEB-DL.RUS",
		Attrs: []torznabAttr{
			{Name: "infohash", Value: "0123456789ABCDEF0123456789ABCDEF01234567"},
		},
	}
	provider := NewProvider(Config{
		Name:     "jackett",
		Label:    "Jackett (Torznab)",
		Kind:     "indexer",
		APIKey:   "dummy",
		Trackers: []string{"udp://tracker.opentrackr.org:1337/announce"},
	})

	result, ok := provider.itemToResult(context.Background(), item, "jackett.local")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if result.InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("unexpected infohash: %q", result.InfoHash)
	}
	if !strings.HasPrefix(strings.ToLower(result.Magnet), "magnet:?") {
		t.Fatalf("expected magnet, got %q", result.Magnet)
	}
}

func TestItemToResultUsesOriginalCommentsURLForPageURL(t *testing.T) {
	item := torznabItem{
		Title:    "Dark.S01E01.1080p.WEB-DL.RUS",
		Link:     "http://jackett.local/api/v2.0/indexers/all/results/torznab/api",
		Comments: "https://rutracker.org/forum/viewtopic.php?t=67890",
		Attrs: []torznabAttr{
			{Name: "infohash", Value: "0123456789ABCDEF0123456789ABCDEF01234567"},
		},
	}
	provider := NewProvider(Config{
		Name:   "jackett",
		Label:  "Jackett (Torznab)",
		Kind:   "indexer",
		APIKey: "dummy",
	})

	result, ok := provider.itemToResult(context.Background(), item, "jackett.local")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if result.PageURL != "https://rutracker.org/forum/viewtopic.php?t=67890" {
		t.Fatalf("expected original page url, got %q", result.PageURL)
	}
}

func TestItemToResultSkipsAggregatorPageURL(t *testing.T) {
	item := torznabItem{
		Title: "Dark.S01E01.1080p.WEB-DL.RUS",
		Link:  "http://jackett.local/api/v2.0/indexers/all/results/torznab/api",
		Attrs: []torznabAttr{
			{Name: "infohash", Value: "0123456789ABCDEF0123456789ABCDEF01234567"},
			{Name: "comments", Value: "http://prowlarr.local/1/api?t=search"},
		},
	}
	provider := NewProvider(Config{
		Name:   "jackett",
		Label:  "Jackett (Torznab)",
		Kind:   "indexer",
		APIKey: "dummy",
	})

	result, ok := provider.itemToResult(context.Background(), item, "jackett.local")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if result.PageURL != "" {
		t.Fatalf("expected empty page url for aggregator-only links, got %q", result.PageURL)
	}
}

func TestInfoEnabledDependsOnConfig(t *testing.T) {
	provider := NewProvider(Config{Name: "jackett", Label: "Jackett"})
	if provider.Info().Enabled {
		t.Fatalf("expected enabled=false without endpoint/apiKey")
	}

	provider = NewProvider(Config{Name: "jackett", Label: "Jackett", Endpoint: "http://example", APIKey: "x"})
	if !provider.Info().Enabled {
		t.Fatalf("expected enabled=true")
	}

	provider = NewProvider(Config{Name: "jackett", Label: "Jackett", Endpoint: "http://example/api?t=search&apikey=inline"})
	if !provider.Info().Enabled {
		t.Fatalf("expected enabled=true with apikey in endpoint")
	}
}

func TestExpandedQueryProfileDoesNotAffectProviderInfo(t *testing.T) {
	// Sanity check for imports and build: domain is used in torznab provider, no-op here.
	_ = domain.DefaultSearchRankingProfile()
}
