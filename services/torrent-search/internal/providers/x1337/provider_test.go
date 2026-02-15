package x1337

import (
	"net/url"
	"testing"
)

func TestParseSearchEntries(t *testing.T) {
	payload := `
<table>
  <tr><td><a href="/torrent/123/test-torrent/">Test Torrent</a></td></tr>
  <tr><td><a href="/torrent/456/another/">Another One</a></td></tr>
</table>`

	entries := parseSearchEntries(payload)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "Test Torrent" {
		t.Fatalf("unexpected first title: %s", entries[0].Name)
	}
}

func TestParseDetailHTML(t *testing.T) {
	payload := `
<div>Seeders</div><span>421</span>
<div>Leechers</div><span>17</span>
<div>Total size</div><span>1.5 GB</span>
<div class="torrent-description">WEB-DL x265 release</div>
<img src="/images/poster.jpg" />
<a href="magnet:?xt=urn:btih:ABCDEF1234567890ABCDEF1234567890ABCDEF12&dn=Test">magnet</a>`

	baseURL, _ := url.Parse("https://1337x.to")
	magnet, seeders, leechers, sizeBytes, enrichment := parseDetailHTML(payload, baseURL)
	if magnet == "" {
		t.Fatal("expected magnet")
	}
	if seeders != 421 || leechers != 17 {
		t.Fatalf("unexpected peers: s=%d l=%d", seeders, leechers)
	}
	if sizeBytes <= 0 {
		t.Fatalf("unexpected size: %d", sizeBytes)
	}
	if enrichment.Description == "" {
		t.Fatal("expected description")
	}
	if enrichment.Poster == "" {
		t.Fatal("expected poster")
	}
}

func TestParseEndpoints(t *testing.T) {
	endpoints := parseEndpoints("https://a.example, https://b.example, https://a.example")
	if len(endpoints) != 2 {
		t.Fatalf("expected deduped endpoints, got %d", len(endpoints))
	}
}
