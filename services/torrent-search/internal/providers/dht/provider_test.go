package dht

import "testing"

func TestExtractMagnets(t *testing.T) {
	htmlPayload := `
<html><body>
<a href="magnet:?xt=urn:btih:ABCDEF1234567890ABCDEF1234567890ABCDEF12&amp;dn=Ubuntu+ISO&amp;xl=2147483648">link</a>
</body></html>`

	magnets := extractMagnets(htmlPayload)
	if len(magnets) != 1 {
		t.Fatalf("expected 1 magnet, got %d", len(magnets))
	}
	if magnets[0] == "" {
		t.Fatal("empty magnet")
	}
}

func TestMagnetToResult(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:ABCDEF1234567890ABCDEF1234567890ABCDEF12&dn=Ubuntu+ISO&xl=2147483648"
	result, ok := magnetToResult(magnet)
	if !ok {
		t.Fatal("expected parsed result")
	}
	if result.InfoHash == "" || result.Name == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.SizeBytes != 2147483648 {
		t.Fatalf("unexpected size: %d", result.SizeBytes)
	}
}
