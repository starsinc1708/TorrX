package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.opensubtitles.com/api/v1"

type SubtitleResult struct {
	FileID        int     `json:"fileID"`
	Language      string  `json:"language"`
	Release       string  `json:"release"`
	Rating        float64 `json:"rating"`
	DownloadCount int     `json:"downloadCount"`
	FileName      string  `json:"fileName"`
}

type SearchParams struct {
	MovieHash string
	Query     string
	Languages []string
}

type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

type Option func(*Client)

func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = url }
}

func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// --- search types ---

type searchResponse struct {
	Data []searchResult `json:"data"`
}

type searchResult struct {
	ID         string           `json:"id"`
	Attributes searchAttributes `json:"attributes"`
}

type searchAttributes struct {
	Language      string       `json:"language"`
	Release       string       `json:"release"`
	Ratings       float64      `json:"ratings"`
	DownloadCount int          `json:"download_count"`
	Files         []searchFile `json:"files"`
}

type searchFile struct {
	FileID   int    `json:"file_id"`
	FileName string `json:"file_name"`
}

// Search queries the OpenSubtitles API for subtitles matching the given parameters.
// Results are returned as a flat list: one SubtitleResult per file across all matched entries.
func (c *Client) Search(ctx context.Context, params SearchParams) ([]SubtitleResult, error) {
	q := url.Values{}
	if params.MovieHash != "" {
		q.Set("moviehash", params.MovieHash)
	}
	if params.Query != "" {
		q.Set("query", params.Query)
	}
	if len(params.Languages) > 0 {
		q.Set("languages", strings.Join(params.Languages, ","))
	}

	reqURL := c.baseURL + "/subtitles?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("opensubtitles search: status %d: %s", resp.StatusCode, body)
	}

	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("opensubtitles search: decode: %w", err)
	}

	var results []SubtitleResult
	for _, item := range sr.Data {
		for _, file := range item.Attributes.Files {
			results = append(results, SubtitleResult{
				FileID:        file.FileID,
				Language:      item.Attributes.Language,
				Release:       item.Attributes.Release,
				Rating:        item.Attributes.Ratings,
				DownloadCount: item.Attributes.DownloadCount,
				FileName:      file.FileName,
			})
		}
	}
	return results, nil
}

// --- download types ---

type downloadRequest struct {
	FileID int `json:"file_id"`
}

type downloadResponse struct {
	Link string `json:"link"`
}

// DownloadLink requests a temporary download URL for the subtitle file with the given ID.
func (c *Client) DownloadLink(ctx context.Context, fileID int) (string, error) {
	body, err := json.Marshal(downloadRequest{FileID: fileID})
	if err != nil {
		return "", err
	}

	reqURL := c.baseURL + "/download"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("opensubtitles download: status %d: %s", resp.StatusCode, respBody)
	}

	var dr downloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return "", fmt.Errorf("opensubtitles download: decode: %w", err)
	}
	return dr.Link, nil
}
