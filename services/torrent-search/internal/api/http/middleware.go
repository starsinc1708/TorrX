package apihttp

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
	"torrentstream/searchservice/internal/metrics"
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

func (rw *responseWriter) Flush() {
	flusher, ok := rw.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		level := pickRequestLogLevel(r.URL.Path, rw.status)
		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Int("bytes", rw.size),
			slog.Int64("durationMs", time.Since(start).Milliseconds()),
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
			if recovered := recover(); recovered != nil {
				logger.Error("panic recovered",
					slog.Any("error", recovered),
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
	case path == "/health" || path == "/metrics":
		return path
	case path == "/search" || path == "/search/stream" || path == "/search/suggest" || path == "/search/image":
		return path
	case strings.HasPrefix(path, "/search/providers"):
		return "/search/providers"
	case strings.HasPrefix(path, "/search/settings"):
		return "/search/settings"
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
	case path == "/health":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			return strings.TrimSpace(parts[0])
		}
	}
	if xRealIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); xRealIP != "" {
		return xRealIP
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
		if r.URL.Path == "/health" || r.URL.Path == "/metrics" {
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
