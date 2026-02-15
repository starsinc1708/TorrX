package rutracker

import (
	"net/url"
	"testing"
)

func TestParseTopics(t *testing.T) {
	payload := `
<a href="/forum/viewtopic.php?t=12345">First Topic</a>
<a href="https://rutracker.org/forum/viewtopic.php?t=67890">Second Topic</a>
<a href='viewtopic.php?t=99999&amp;start=0'>Third Topic</a>`

	topics := parseTopics(payload)
	if len(topics) != 3 {
		t.Fatalf("expected 3 topics, got %d", len(topics))
	}
	if topics[0].ID != "12345" {
		t.Fatalf("unexpected first topic id: %s", topics[0].ID)
	}
	if topics[2].ID != "99999" {
		t.Fatalf("unexpected third topic id: %s", topics[2].ID)
	}
}

func TestIsLoginPage(t *testing.T) {
	loginURL, _ := url.Parse("https://rutracker.org/forum/login.php?redirect=tracker.php")
	if !isLoginPage(loginURL, "<html></html>") {
		t.Fatalf("expected login page by url")
	}

	trackerURL, _ := url.Parse("https://rutracker.org/forum/tracker.php?nm=test")
	if !isLoginPage(trackerURL, `<form action="login.php"><input name="login_username"></form>`) {
		t.Fatalf("expected login page by html payload")
	}

	if isLoginPage(trackerURL, "<html><body>tracker list</body></html>") {
		t.Fatalf("did not expect login page")
	}
}
