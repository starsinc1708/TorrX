package apihttp

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
	"torrentstream/internal/metrics"
)

type responseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.size += n
	return n, err
}

// Hijack implements http.Hijacker so that WebSocket upgrades work through the
// middleware chain (gorilla/websocket requires the ResponseWriter to support it).
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Range")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Accept-Ranges, Content-Length")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		level := pickRequestLogLevel(r.URL.Path, rw.status)
		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Int("bytes", rw.size),
			slog.Int64("durationMs", duration.Milliseconds()),
			slog.String("clientIP", clientIP(r)),
		}
		if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
			attrs = append(attrs, slog.String("query", truncate(rawQuery, 180)))
		}
		if userAgent := strings.TrimSpace(r.UserAgent()); userAgent != "" {
			attrs = append(attrs, slog.String("userAgent", truncate(userAgent, 120)))
		}
		logger.LogAttrs(r.Context(), level, "http request", attrs...)
	})
}

func recoveryMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.Error("panic recovered",
					slog.Any("error", err),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("clientIP", clientIP(r)),
					slog.String("stack", string(debug.Stack())),
				)
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		duration := time.Since(start)
		route := normalizeRoute(r.URL.Path)
		metrics.HTTPRequestsTotal.WithLabelValues(r.Method, route, strconv.Itoa(rw.status)).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(r.Method, route).Observe(duration.Seconds())
	})
}

func normalizeRoute(path string) string {
	switch {
	case path == "/metrics" || path == "/internal/health/player":
		return path
	case path == "/torrents":
		return "/torrents"
	case strings.HasPrefix(path, "/torrents/"):
		return "/torrents/:id"
	case strings.HasPrefix(path, "/settings/"):
		return "/settings"
	case path == "/watch-history":
		return "/watch-history"
	case strings.HasPrefix(path, "/watch-history/"):
		return "/watch-history/:id"
	case strings.Contains(path, "/hls/") && strings.HasSuffix(path, ".m3u8"):
		return "/hls/playlist"
	case strings.Contains(path, "/hls/") && strings.HasSuffix(path, ".ts"):
		return "/hls/segment"
	case strings.HasPrefix(path, "/swagger"):
		return "/swagger"
	default:
		return "/other"
	}
}

func pickRequestLogLevel(path string, status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	case isNoisyPath(path):
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

func isNoisyPath(path string) bool {
	if path == "/internal/health/player" || strings.HasPrefix(path, "/swagger") {
		return true
	}
	if strings.Contains(path, "/hls/") && (strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".m3u8")) {
		return true
	}
	return false
}

func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			return strings.TrimSpace(parts[0])
		}
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

// rateLimitMiddleware applies a global token-bucket rate limiter.
// Requests that exceed the limit receive HTTP 429.
func rateLimitMiddleware(rps float64, burst int, next http.Handler) http.Handler {
	limiter := rate.NewLimiter(rate.Limit(rps), burst)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for health checks and metrics.
		if r.URL.Path == "/internal/health/player" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		if !limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}
