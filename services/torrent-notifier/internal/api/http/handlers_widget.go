package apihttp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type engineItem struct {
	Status string `json:"status"`
}

type engineList struct {
	Items []engineItem `json:"items"`
	Count int          `json:"count"`
}

type widgetResponse struct {
	Active    int `json:"active"`
	Completed int `json:"completed"`
	Stopped   int `json:"stopped"`
	Total     int `json:"total"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"ok"}`)
}

func (s *Server) handleWidget(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: 5 * time.Second}
	url := strings.TrimRight(s.engineURL, "/") + "/torrents"
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("widget: GET /torrents error: %v", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		log.Printf("widget: GET /torrents returned %d", resp.StatusCode)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	var list engineList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		log.Printf("widget: decode error: %v", err)
		http.Error(w, "decode error", http.StatusInternalServerError)
		return
	}

	out := widgetResponse{Total: list.Count}
	for _, item := range list.Items {
		switch item.Status {
		case "active":
			out.Active++
		case "completed":
			out.Completed++
		case "stopped":
			out.Stopped++
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		log.Printf("widget: encode error: %v", err)
	}
}
