package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type CORSConfig struct {
	// AllowedOrigins are exact origins such as "http://localhost:5173".
	// "*" allows any origin but cannot be combined with AllowCredentials.
	AllowedOrigins []string

	AllowedMethods []string // default: GET, POST, PUT, PATCH, DELETE, OPTIONS
	AllowedHeaders []string // default: Authorization, Content-Type, X-Request-ID

	AllowCredentials bool
	MaxAge           time.Duration // how long browsers may cache a preflight
}

var (
	defaultCORSMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	defaultCORSHeaders = []string{"Authorization", "Content-Type", "X-Request-ID"}
)

// CORS answers preflight requests and adds CORS headers for allowed origins.
//
// Install it as the innermost Global middleware: preflights carry no
// credentials and match no route, so they must be answered before
// authentication and before the mux sees them.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(cfg.AllowedOrigins))
	allowAll := false
	for _, o := range cfg.AllowedOrigins {
		if o == "*" {
			allowAll = true
			continue
		}
		allowed[o] = struct{}{}
	}
	if allowAll && cfg.AllowCredentials {
		panic("middleware: CORS cannot combine a wildcard origin with credentials")
	}

	methods := strings.Join(orDefault(cfg.AllowedMethods, defaultCORSMethods), ", ")
	headers := strings.Join(orDefault(cfg.AllowedHeaders, defaultCORSHeaders), ", ")
	maxAge := ""
	if cfg.MaxAge > 0 {
		maxAge = strconv.Itoa(int(cfg.MaxAge.Seconds()))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r) // not a cross-origin browser request
				return
			}

			w.Header().Add("Vary", "Origin")
			_, ok := allowed[origin]
			if !ok && !allowAll {
				if isPreflight(r) {
					writeError(w, http.StatusForbidden, "origin not allowed")
					return
				}
				next.ServeHTTP(w, r) // served without CORS headers; the browser blocks the read
				return
			}

			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			if cfg.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			if isPreflight(r) {
				w.Header().Add("Vary", "Access-Control-Request-Method")
				w.Header().Add("Vary", "Access-Control-Request-Headers")
				w.Header().Set("Access-Control-Allow-Methods", methods)
				w.Header().Set("Access-Control-Allow-Headers", headers)
				if maxAge != "" {
					w.Header().Set("Access-Control-Max-Age", maxAge)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

func orDefault(v, def []string) []string {
	if len(v) == 0 {
		return def
	}
	return v
}
