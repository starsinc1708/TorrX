package opensubtitles

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Search(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/subtitles" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Api-Key") != "test-key" {
			t.Fatalf("missing api key header")
		}
		q := r.URL.Query()
		if q.Get("moviehash") != "abc123" {
			t.Fatalf("unexpected moviehash: %s", q.Get("moviehash"))
		}
		if q.Get("languages") != "en,ru" {
			t.Fatalf("unexpected languages: %s", q.Get("languages"))
		}
		resp := searchResponse{
			Data: []searchResult{
				{
					ID: "1",
					Attributes: searchAttributes{
						Language:      "en",
						Release:       "Movie.2024.1080p",
						Ratings:       8.5,
						DownloadCount: 1000,
						Files:         []searchFile{{FileID: 101, FileName: "movie.srt"}},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	results, err := client.Search(context.Background(), SearchParams{
		MovieHash: "abc123",
		Languages: []string{"en", "ru"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Language != "en" || results[0].FileID != 101 {
		t.Fatalf("unexpected result: %+v", results[0])
	}
}

func TestClient_SearchByQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("query") != "Pirates Caribbean" {
			t.Fatalf("unexpected query: %s", q.Get("query"))
		}
		resp := searchResponse{Data: []searchResult{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	results, err := client.Search(context.Background(), SearchParams{
		Query:     "Pirates Caribbean",
		Languages: []string{"en"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestClient_Download(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/download" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		resp := downloadResponse{Link: "https://dl.opensubtitles.com/file/123"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	link, err := client.DownloadLink(context.Background(), 101)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if link != "https://dl.opensubtitles.com/file/123" {
		t.Fatalf("unexpected link: %s", link)
	}
}
