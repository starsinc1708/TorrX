package bittorrentindex

import "testing"

func TestParseAPIItems(t *testing.T) {
	payload := []byte(`[
		{"name":"Ubuntu ISO","info_hash":"ABCDEF1234567890ABCDEF1234567890ABCDEF12","size":"2147483648","seeders":"1200","leechers":"80","added":"1700000000"}
	]`)

	items, err := parseAPIItems(payload)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected items count: %d", len(items))
	}
}

func TestToResultBuildsMagnet(t *testing.T) {
	provider := NewProvider(Config{})
	result, ok := provider.toResult(apiItem{
		Name:     "Ubuntu ISO",
		InfoHash: "ABCDEF1234567890ABCDEF1234567890ABCDEF12",
		Size:     "2147483648",
		Seeders:  "1200",
		Leechers: "80",
		Added:    "1700000000",
	})
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.InfoHash == "" || result.Magnet == "" {
		t.Fatalf("expected infoHash and magnet, got %#v", result)
	}
	if result.Seeders != 1200 {
		t.Fatalf("unexpected seeders: %d", result.Seeders)
	}
}
