package apihttp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxProxiedImageBytes = int64(20 * 1024 * 1024) // 20MB

func (s *Server) handleImageProxy(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/search/image" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	if raw == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing url")
		return
	}

	target, err := url.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid url")
		return
	}
	if err := validateProxyURL(r.Context(), target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	client := newImageProxyClient(r.Context())
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid url")
		return
	}
	req.Header.Set("User-Agent", "torrent-stream-search/1.0")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Referer", target.Scheme+"://"+target.Host+"/")

	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "failed to fetch image")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Do not forward upstream body to avoid leaking HTML/JS. Keep it generic.
		writeError(w, http.StatusBadGateway, "upstream_error", fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode))
		return
	}

	if resp.ContentLength > maxProxiedImageBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "invalid_request", "image too large")
		return
	}

	limited := io.LimitReader(resp.Body, maxProxiedImageBytes)
	head := make([]byte, 512)
	n, readErr := io.ReadFull(limited, head)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		writeError(w, http.StatusBadGateway, "upstream_error", "failed to read image")
		return
	}
	head = head[:n]

	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(head)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		writeError(w, http.StatusBadGateway, "upstream_error", "not an image")
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write(head)
	_, _ = io.Copy(w, limited)
}

func newImageProxyClient(parent context.Context) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}

	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = dialer.DialContext

	return &http.Client{
		Timeout:   12 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			if req.URL == nil {
				return errors.New("redirect missing url")
			}
			if err := validateProxyURL(parent, req.URL); err != nil {
				return err
			}
			return nil
		},
	}
}

func validateProxyURL(ctx context.Context, u *url.URL) error {
	if u == nil {
		return errors.New("invalid url")
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return errors.New("unsupported url scheme")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return errors.New("invalid url host")
	}

	// Prevent SSRF to local network / docker DNS names.
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "jackett", "prowlarr", "torrent-search", "torrentstream", "mongo", "traefik":
		return errors.New("blocked url host")
	}
	if strings.HasSuffix(strings.ToLower(host), ".local") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return errors.New("blocked url host")
	}

	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return errors.New("blocked url host")
		}
		return nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil || len(addrs) == 0 {
		return errors.New("failed to resolve url host")
	}
	for _, addr := range addrs {
		if addr.IP == nil {
			continue
		}
		if isBlockedIP(addr.IP) {
			return errors.New("blocked url host")
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	return false
}

