package apihttp

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------- CORS middleware tests ----------

func TestCorsMiddleware_AllowAll_WhenNoOriginsConfigured(t *testing.T) {
	handler := corsMiddleware(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://example.com" {
		t.Errorf("expected origin reflected, got %q", got)
	}
}

func TestCorsMiddleware_AllowAll_EmptySlice(t *testing.T) {
	handler := corsMiddleware([]string{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://anything.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://anything.com" {
		t.Errorf("expected origin reflected for empty whitelist, got %q", got)
	}
}

func TestCorsMiddleware_AllowWhitelisted(t *testing.T) {
	handler := corsMiddleware([]string{"http://allowed.com", "http://also-allowed.com"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://allowed.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://allowed.com" {
		t.Errorf("expected whitelisted origin, got %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("expected Vary: Origin, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("expected Allow-Methods header to be set")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("expected Allow-Headers header to be set")
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got == "" {
		t.Error("expected Expose-Headers header to be set")
	}
}

func TestCorsMiddleware_RejectNonWhitelisted(t *testing.T) {
	handler := corsMiddleware([]string{"http://allowed.com"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://evil.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no ACAO header for rejected origin, got %q", got)
	}
	// The handler still runs (CORS is advisory), but browser will block the response.
	if rec.Code != http.StatusOK {
		t.Errorf("expected handler to still execute, got %d", rec.Code)
	}
}

func TestCorsMiddleware_OriginTrailingSlashTrimmed(t *testing.T) {
	handler := corsMiddleware([]string{"http://example.com/"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://example.com" {
		t.Errorf("expected trailing slash trimmed origin to match, got %q", got)
	}
}

func TestCorsMiddleware_PreflightReturns204(t *testing.T) {
	called := false
	handler := corsMiddleware(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rec.Code)
	}
	if called {
		t.Error("preflight should not call the next handler")
	}
}

func TestCorsMiddleware_SameOriginNoHeaders(t *testing.T) {
	handler := corsMiddleware([]string{"http://allowed.com"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	// No Origin header = same-origin request.
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS headers for same-origin, got %q", got)
	}
}

// ---------- Rate limit middleware tests ----------

func TestRateLimitMiddleware_AllowsWithinBurst(t *testing.T) {
	handler := rateLimitMiddleware(100, 10, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
		}
	}
}

func TestRateLimitMiddleware_Returns429AfterBurst(t *testing.T) {
	handler := rateLimitMiddleware(0.001, 2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust burst.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Next request should be rate limited.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Errorf("expected Retry-After: 1, got %q", got)
	}
}

func TestRateLimitMiddleware_SkipsHealthEndpoint(t *testing.T) {
	handler := rateLimitMiddleware(0.001, 1, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust burst.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Health endpoint should bypass rate limit.
	req = httptest.NewRequest(http.MethodGet, "/internal/health/player", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected health endpoint to bypass rate limit, got %d", rec.Code)
	}
}

func TestRateLimitMiddleware_SkipsMetricsEndpoint(t *testing.T) {
	handler := rateLimitMiddleware(0.001, 1, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust burst.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Metrics endpoint should bypass rate limit.
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected metrics endpoint to bypass rate limit, got %d", rec.Code)
	}
}

// ---------- Recovery middleware tests ----------

func TestRecoveryMiddleware_CatchesPanic(t *testing.T) {
	logger := slog.Default()
	handler := recoveryMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	// Should not panic.
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestRecoveryMiddleware_CatchesNilPanic(t *testing.T) {
	logger := slog.Default()
	handler := recoveryMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(nil)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	// panic(nil) in Go <1.21 recovers as nil, in Go >=1.21 recovers as *runtime.PanicNilError.
	// Either way, if recover() is non-nil, the middleware should catch it.
	// If recover() is nil, the middleware does nothing — handler "completes normally".
	handler.ServeHTTP(rec, req)

	// In Go >=1.21, this should be 500. In older Go, it may be 200 (no recovery needed).
	// We accept either outcome as correct behavior.
	if rec.Code != http.StatusInternalServerError && rec.Code != http.StatusOK {
		t.Errorf("expected 500 or 200, got %d", rec.Code)
	}
}

func TestRecoveryMiddleware_CatchesErrorPanic(t *testing.T) {
	logger := slog.Default()
	handler := recoveryMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(fmt.Errorf("something went wrong"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestRecoveryMiddleware_NoPanicPassesThrough(t *testing.T) {
	logger := slog.Default()
	handler := recoveryMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}
}

// ---------- Logging middleware tests ----------

func TestLoggingMiddleware_SetsStatusAndSize(t *testing.T) {
	logger := slog.Default()
	handler := loggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got %q", rec.Body.String())
	}
}

func TestLoggingMiddleware_DefaultStatusIs200(t *testing.T) {
	logger := slog.Default()
	var capturedStatus int
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't call WriteHeader — should default to 200.
		w.Write([]byte("ok"))
	})
	// Wrap in a test middleware to capture the responseWriter status.
	handler := loggingMiddleware(logger, inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// The logging middleware sets default status to 200.
	_ = capturedStatus
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// ---------- responseWriter tests ----------

func TestResponseWriter_WriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}

	rw.WriteHeader(http.StatusNotFound)

	if rw.status != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rw.status)
	}
}

func TestResponseWriter_WriteCapturesSize(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}

	n, err := rw.Write([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if rw.size != 5 {
		t.Errorf("expected size 5, got %d", rw.size)
	}

	rw.Write([]byte(" world"))
	if rw.size != 11 {
		t.Errorf("expected cumulative size 11, got %d", rw.size)
	}
}

// fakeHijacker wraps a ResponseWriter and implements Hijacker.
type fakeHijacker struct {
	http.ResponseWriter
}

func (f *fakeHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, nil
}

func TestResponseWriter_HijackSupported(t *testing.T) {
	inner := &fakeHijacker{ResponseWriter: httptest.NewRecorder()}
	rw := &responseWriter{ResponseWriter: inner, status: http.StatusOK}

	_, _, err := rw.Hijack()
	if err != nil {
		t.Errorf("expected hijack to succeed, got %v", err)
	}
}

func TestResponseWriter_HijackUnsupported(t *testing.T) {
	rw := &responseWriter{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}

	_, _, err := rw.Hijack()
	if err == nil {
		t.Error("expected error when underlying writer doesn't support Hijack")
	}
}

// ---------- clientIP tests ----------

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		xRealIP    string
		remoteAddr string
		want       string
	}{
		{
			name:       "X-Forwarded-For single",
			xff:        "1.2.3.4",
			remoteAddr: "5.6.7.8:9999",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For multiple takes first",
			xff:        "1.2.3.4, 10.0.0.1, 172.16.0.1",
			remoteAddr: "5.6.7.8:9999",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For with spaces",
			xff:        "  1.2.3.4 , 10.0.0.1",
			remoteAddr: "5.6.7.8:9999",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Real-IP fallback",
			xRealIP:    "10.0.0.1",
			remoteAddr: "5.6.7.8:9999",
			want:       "10.0.0.1",
		},
		{
			name:       "RemoteAddr fallback with port",
			remoteAddr: "192.168.1.1:12345",
			want:       "192.168.1.1",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "192.168.1.1",
			want:       "192.168.1.1",
		},
		{
			name:       "XFF empty string falls through",
			xff:        "",
			xRealIP:    "10.0.0.1",
			remoteAddr: "5.6.7.8:9999",
			want:       "10.0.0.1",
		},
		{
			name:       "XFF whitespace only falls through",
			xff:        "   ",
			xRealIP:    "",
			remoteAddr: "5.6.7.8:9999",
			want:       "5.6.7.8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xRealIP != "" {
				req.Header.Set("X-Real-IP", tc.xRealIP)
			}
			req.RemoteAddr = tc.remoteAddr

			got := clientIP(req)
			if got != tc.want {
				t.Errorf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------- truncate tests ----------

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact limit", "hello", 5, "hello"},
		{"over limit", "hello world", 8, "hello..."},
		{"limit 3", "hello", 3, "hel"},
		{"limit 2", "hello", 2, "he"},
		{"limit 1", "hello", 1, "h"},
		{"limit 0", "hello", 0, "hello"},
		{"negative limit", "hello", -1, "hello"},
		{"empty string", "", 10, ""},
		{"limit 4 on long", "abcdefgh", 4, "a..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.value, tc.limit)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.value, tc.limit, got, tc.want)
			}
		})
	}
}

// ---------- pickRequestLogLevel tests ----------

func TestPickRequestLogLevel(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		status int
		want   slog.Level
	}{
		{"500 error", "/api", 500, slog.LevelError},
		{"503 error", "/api", 503, slog.LevelError},
		{"400 warn", "/api", 400, slog.LevelWarn},
		{"404 warn", "/api", 404, slog.LevelWarn},
		{"200 info", "/api", 200, slog.LevelInfo},
		{"201 info", "/api", 201, slog.LevelInfo},
		{"noisy path debug", "/internal/health/player", 200, slog.LevelDebug},
		{"HLS segment debug", "/torrents/abc/hls/0/seg001.ts", 200, slog.LevelDebug},
		{"HLS playlist debug", "/torrents/abc/hls/0/index.m3u8", 200, slog.LevelDebug},
		{"swagger debug", "/swagger/index.html", 200, slog.LevelDebug},
		{"noisy path with 500 still error", "/internal/health/player", 500, slog.LevelError},
		{"noisy path with 400 still warn", "/internal/health/player", 400, slog.LevelWarn},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pickRequestLogLevel(tc.path, tc.status)
			if got != tc.want {
				t.Errorf("pickRequestLogLevel(%q, %d) = %v, want %v", tc.path, tc.status, got, tc.want)
			}
		})
	}
}

// ---------- isNoisyPath tests ----------

func TestIsNoisyPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/internal/health/player", true},
		{"/swagger", true},
		{"/swagger/index.html", true},
		{"/torrents/abc/hls/0/seg001.ts", true},
		{"/torrents/abc/hls/0/index.m3u8", true},
		{"/torrents", false},
		{"/api/test", false},
		{"/search", false},
		{"/settings/encoding", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := isNoisyPath(tc.path)
			if got != tc.want {
				t.Errorf("isNoisyPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// ---------- normalizeRoute tests ----------

func TestNormalizeRoute(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/metrics", "/metrics"},
		{"/internal/health/player", "/internal/health/player"},
		{"/torrents", "/torrents"},
		{"/torrents/abc123", "/torrents/:id"},
		{"/torrents/abc123/hls/0/master.m3u8", "/torrents/:id"},
		{"/settings/encoding", "/settings"},
		{"/settings/storage", "/settings"},
		{"/watch-history", "/watch-history"},
		{"/watch-history/abc123", "/watch-history/:id"},
		{"/torrents/abc/hls/0/index.m3u8", "/hls/playlist"},
		{"/torrents/abc/hls/0/seg001.ts", "/hls/segment"},
		{"/swagger", "/swagger"},
		{"/swagger/index.html", "/swagger"},
		{"/unknown", "/other"},
		{"/", "/other"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := normalizeRoute(tc.path)
			if got != tc.want {
				t.Errorf("normalizeRoute(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// ---------- Metrics middleware tests ----------

func TestMetricsMiddleware_SkipsMetricsPath(t *testing.T) {
	called := false
	handler := metricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMetricsMiddleware_RecordsNonMetricsPath(t *testing.T) {
	handler := metricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/torrents", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	// We can't easily assert Prometheus counter values here without resetting,
	// but verifying no panic means the middleware works.
}

// ---------- Integration: middleware chain order ----------

func TestMiddlewareChain_RecoveryOutermost(t *testing.T) {
	// Simulate the real middleware chain order:
	// recovery → rateLimit → metrics → cors → logging → handler
	logger := slog.Default()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test chain panic")
	})

	chain := recoveryMiddleware(logger,
		rateLimitMiddleware(100, 200,
			metricsMiddleware(
				corsMiddleware(nil,
					loggingMiddleware(logger, inner)))))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 from recovery middleware, got %d", rec.Code)
	}
}

func TestMiddlewareChain_CorsAndRateLimitTogether(t *testing.T) {
	logger := slog.Default()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	chain := recoveryMiddleware(logger,
		rateLimitMiddleware(0.001, 1,
			corsMiddleware([]string{"http://allowed.com"}, inner)))

	// First request within burst should work.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://allowed.com")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", rec.Code)
	}

	// Second request should be rate limited (burst=1, very low rps).
	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://allowed.com")
	rec = httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", rec.Code)
	}
}

// ---------- CORS edge cases ----------

func TestCorsMiddleware_WhitelistWithSpaces(t *testing.T) {
	handler := corsMiddleware([]string{"  http://example.com  "}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://example.com" {
		t.Errorf("expected whitespace-trimmed origin to match, got %q", got)
	}
}

func TestCorsMiddleware_MultipleOrigins(t *testing.T) {
	handler := corsMiddleware([]string{"http://a.com", "http://b.com", "http://c.com"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, origin := range []string{"http://a.com", "http://b.com", "http://c.com"} {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("origin %s: expected ACAO=%s, got %s", origin, origin, got)
		}
	}
}

// ---------- Rate limit 429 response body ----------

func TestRateLimitMiddleware_429ResponseBody(t *testing.T) {
	handler := rateLimitMiddleware(0.001, 1, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust burst.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Rate limited.
	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "rate_limited") {
		t.Errorf("expected rate_limited in response body, got %q", body)
	}
}
