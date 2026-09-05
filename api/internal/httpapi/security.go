package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)

// Browser-driven cross-site attacks are the realistic threat here. The
// dashboard is reached with a browser session, so any page the operator
// happens to visit can make their browser issue requests to the pool
// with whatever credentials that session carries. Two shapes matter:
//
//   - Cross-Site WebSocket Hijacking. The WebSocket handshake is not
//     subject to the same-origin policy, so without a check any site
//     could open /api/ws and read the whole snapshot: payout addresses,
//     per-worker hashrates, block history.
//   - CSRF against the state-changing POSTs. The admin endpoints take no
//     body, so a plain <form> submission reaches them with no preflight
//     to stop it, which is enough to zero the latency counters or kick
//     off a rescan of a multi-million-line log.
//
// Both are closed by checking Origin, which browsers attach to every
// WebSocket handshake and to cross-site POSTs, and which page script
// cannot forge.

// originAllowed reports whether a request may proceed. A request with no
// Origin header is not browser-driven (curl, the StartOS actions, a
// health probe) and is allowed: an attacker able to make arbitrary
// direct requests already has network access and does not need the
// operator's browser to carry them.
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// sameOrigin wraps a handler so cross-origin browser requests are
// refused. Applied to every state-changing route and to the WebSocket.
func (s *Server) sameOrigin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r) {
			s.Log.Warn("refused cross-origin request",
				"path", r.URL.Path, "origin", r.Header.Get("Origin"), "host", r.Host)
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": "cross-origin request refused",
			})
			return
		}
		h(w, r)
	}
}

// securityHeaders sets the response headers that cost nothing and close
// off framing and MIME-sniffing. No CSP: the dashboard is a bundled
// Svelte app with inline styles, and a policy loose enough to run it
// would not be worth the claim of having one.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		head := w.Header()
		head.Set("X-Content-Type-Options", "nosniff")
		// The dashboard has nothing to gain from being framed, and
		// framing it invites clickjacking of the admin controls.
		head.Set("X-Frame-Options", "DENY")
		head.Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}
