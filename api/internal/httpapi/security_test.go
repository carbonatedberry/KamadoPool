package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginAllowed(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		// No Origin: not browser-driven, so not a cross-site vector.
		{"no origin header", "pool.local:8080", "", true},
		{"same origin", "pool.local:8080", "http://pool.local:8080", true},
		{"same origin over https", "pool.local", "https://pool.local", true},
		{"case-insensitive host", "Pool.Local:8080", "http://pool.local:8080", true},

		// The attacks this closes.
		{"attacker site", "pool.local:8080", "https://evil.example", false},
		{"different port on same name", "pool.local:8080", "http://pool.local:9999", false},
		{"subdomain of the host", "pool.local:8080", "http://x.pool.local:8080", false},
		{"suffix trick", "pool.local:8080", "http://notpool.local:8080", false},
		{"null origin from a sandboxed frame", "pool.local:8080", "null", false},
		{"malformed origin", "pool.local:8080", "::::", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://"+tc.host+"/api/admin/reset-latency", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := originAllowed(r); got != tc.want {
				t.Errorf("originAllowed(host=%q origin=%q) = %v, want %v",
					tc.host, tc.origin, got, tc.want)
			}
		})
	}
}

// A cross-origin POST must be refused before the handler runs, so a page
// the operator happens to be visiting cannot drive the admin routes with
// their session.
func TestSameOriginRefusesCrossSite(t *testing.T) {
	s := &Server{Log: discardLogger()}
	called := false
	h := s.sameOrigin(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequest("POST", "http://pool.local:8080/api/admin/reset-latency", nil)
	r.Host = "pool.local:8080"
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h(w, r)

	if called {
		t.Error("handler ran for a cross-origin request")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestSameOriginAllowsSameSite(t *testing.T) {
	s := &Server{Log: discardLogger()}
	called := false
	h := s.sameOrigin(func(w http.ResponseWriter, r *http.Request) { called = true })

	r := httptest.NewRequest("POST", "http://pool.local:8080/api/admin/ack-best", nil)
	r.Host = "pool.local:8080"
	r.Header.Set("Origin", "http://pool.local:8080")
	h(httptest.NewRecorder(), r)

	if !called {
		t.Error("handler did not run for a same-origin request")
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
