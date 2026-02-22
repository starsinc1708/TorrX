package qbt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// statusMap converts torrent-engine status to qBittorrent state string.
var statusMap = map[string]string{
	"active":    "downloading",
	"completed": "uploading",
	"stopped":   "pausedDL",
	"pending":   "checkingDL",
	"error":     "error",
}

type engineTorrent struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Progress   float64   `json:"progress"`
	DoneBytes  int64     `json:"doneBytes"`
	TotalBytes int64     `json:"totalBytes"`
	CreatedAt  time.Time `json:"createdAt"`
}

type engineListResp struct {
	Items []engineTorrent `json:"items"`
	Count int             `json:"count"`
}

// qbtTorrent is the qBittorrent API v2 torrent info shape.
type qbtTorrent struct {
	Hash         string  `json:"hash"`
	Name         string  `json:"name"`
	State        string  `json:"state"`
	Progress     float64 `json:"progress"`
	Size         int64   `json:"size"`
	Downloaded   int64   `json:"downloaded"`
	DlSpeed      int64   `json:"dlspeed"`
	SavePath     string  `json:"save_path"`
	Category     string  `json:"category"`
	AddedOn      int64   `json:"added_on"`
	CompletionOn int64   `json:"completion_on"`
}

// Handler implements the qBittorrent WebAPI v2 subset needed by Sonarr/Radarr.
type Handler struct {
	engineURL string
	client    *http.Client
	mux       *http.ServeMux
}

func NewHandler(engineURL string) *Handler {
	h := &Handler{
		engineURL: strings.TrimRight(engineURL, "/"),
		client:    &http.Client{Timeout: 10 * time.Second},
	}
	h.mux = http.NewServeMux()
	h.mux.HandleFunc("/api/v2/auth/login", h.handleLogin)
	h.mux.HandleFunc("/api/v2/app/version", h.handleAppVersion)
	h.mux.HandleFunc("/api/v2/app/webapiVersion", h.handleWebapiVersion)
	h.mux.HandleFunc("/api/v2/torrents/info", h.handleTorrentsInfo)
	h.mux.HandleFunc("/api/v2/torrents/add", h.handleTorrentsAdd)
	h.mux.HandleFunc("/api/v2/torrents/delete", h.handleTorrentsDelete)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Always succeed — localhost-only, no real auth needed.
	http.SetCookie(w, &http.Cookie{Name: "SID", Value: "localtoken", Path: "/"})
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "Ok.")
}

func (h *Handler) handleAppVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "4.6.0")
}

func (h *Handler) handleWebapiVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "2.8.3")
}

func (h *Handler) handleTorrentsInfo(w http.ResponseWriter, r *http.Request) {
	resp, err := h.client.Get(h.engineURL + "/torrents")
	if err != nil {
		log.Printf("qbt: GET /torrents error: %v", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		log.Printf("qbt: GET /torrents returned %d", resp.StatusCode)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	var list engineListResp
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		http.Error(w, "decode error", http.StatusInternalServerError)
		return
	}

	out := make([]qbtTorrent, 0, len(list.Items))
	for _, t := range list.Items {
		state, ok := statusMap[t.Status]
		if !ok {
			state = "unknown"
		}
		qt := qbtTorrent{
			Hash:       t.ID,
			Name:       t.Name,
			State:      state,
			Progress:   t.Progress,
			Size:       t.TotalBytes,
			Downloaded: t.DoneBytes,
			DlSpeed:    0,
			SavePath:   "/data/",
			Category:   "",
			AddedOn:    t.CreatedAt.Unix(),
		}
		// CompletionOn approximates from CreatedAt; engine does not expose a completion timestamp.
		if t.Status == "completed" {
			qt.CompletionOn = t.CreatedAt.Unix()
		}
		out = append(out, qt)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		log.Printf("qbt: encode torrents: %v", err)
	}
}

func (h *Handler) handleTorrentsAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		r.ParseForm()
	}
	magnet := r.FormValue("urls")
	name := r.FormValue("rename")

	type addBody struct {
		MagnetURI string `json:"magnetURI"`
		Name      string `json:"name"`
	}
	b, err := json.Marshal(addBody{MagnetURI: magnet, Name: name})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp, err := h.client.Post(h.engineURL+"/torrents", "application/json",
		bytes.NewReader(b))
	if err != nil {
		log.Printf("qbt: POST /torrents error: %v", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode >= 300 {
		log.Printf("qbt: POST /torrents returned %d", resp.StatusCode)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "Ok.")
}

func (h *Handler) handleTorrentsDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	hash := r.FormValue("hashes")
	deleteFiles := r.FormValue("deleteFiles") == "true"

	url := fmt.Sprintf("%s/torrents/%s?deleteFiles=%v", h.engineURL, hash, deleteFiles)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodDelete, url, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("qbt: DELETE /torrents/%s error: %v", hash, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	w.WriteHeader(http.StatusOK)
}
