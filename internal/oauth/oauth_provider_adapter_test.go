package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

const (
	testClientID     = "test-client.apps.googleusercontent.com"
	testClientSecret = "test-secret"
	testRedirectURL  = "http://localhost:8080/auth/google/callback"
)

// fakeGoogle serves the two endpoints Exchange calls: the token endpoint
// (oauth2.googleapis.com/token) and the userinfo endpoint.
type fakeGoogle struct {
	tokenStatus    int
	userinfoStatus int
	userinfo       string
}

func (g *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/token":
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// oauth2 sends client credentials either as basic auth or as form fields.
		id, secret, ok := r.BasicAuth()
		if !ok {
			id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		if id != testClientID || secret != testClientSecret {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		if r.PostForm.Get("grant_type") != "authorization_code" ||
			r.PostForm.Get("code") != "good-code" ||
			r.PostForm.Get("redirect_uri") != testRedirectURL {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		if g.tokenStatus != 0 {
			w.WriteHeader(g.tokenStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"access-123","token_type":"Bearer","expires_in":3600}`))

	case r.Method == http.MethodGet && r.URL.Path == "/oauth2/v3/userinfo":
		if r.Header.Get("Authorization") != "Bearer access-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if g.userinfoStatus != 0 {
			w.WriteHeader(g.userinfoStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(g.userinfo))

	default:
		http.NotFound(w, r)
	}
}

// redirectTransport sends every request to the fake server, whatever host the
// adapter asked for, so the real Google URLs in the adapter stay untouched.
type redirectTransport struct{ target *url.URL }

func (t redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host, r.Host = t.target.Scheme, t.target.Host, t.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

// withFakeGoogle returns a context whose oauth2 HTTP client talks to g.
func withFakeGoogle(t *testing.T, g *fakeGoogle) context.Context {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: redirectTransport{target}}
	return context.WithValue(context.Background(), oauth2.HTTPClient, client)
}

func TestAuthCodeURL(t *testing.T) {
	g := NewGoogleAdapter(testClientID, testClientSecret, testRedirectURL)
	u, err := url.Parse(g.AuthCodeURL("state-xyz"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "accounts.google.com" {
		t.Errorf("host = %q, want accounts.google.com", u.Host)
	}
	q := u.Query()
	want := map[string]string{
		"client_id":     testClientID,
		"redirect_uri":  testRedirectURL,
		"state":         "state-xyz",
		"response_type": "code",
		"access_type":   "offline",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	scope := q.Get("scope")
	for _, s := range []string{"userinfo.profile", "userinfo.email"} {
		if !strings.Contains(scope, s) {
			t.Errorf("scope %q is missing %s", scope, s)
		}
	}
	if strings.Contains(u.String(), testClientSecret) {
		t.Error("client secret leaked into the browser redirect URL")
	}
}

func TestExchange(t *testing.T) {
	ctx := withFakeGoogle(t, &fakeGoogle{
		userinfo: `{"sub":"1234567890","name":"Ada Lovelace","email":"ada@example.com"}`,
	})
	g := NewGoogleAdapter(testClientID, testClientSecret, testRedirectURL)

	id, err := g.Exchange(ctx, "good-code")
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "1234567890" || id.Name != "Ada Lovelace" || id.Email != "ada@example.com" {
		t.Errorf("identity = %+v", id)
	}
}

func TestExchangeErrors(t *testing.T) {
	tests := []struct {
		name     string
		google   fakeGoogle
		secret   string
		code     string
		wantText string
	}{
		{"wrong client secret", fakeGoogle{}, "wrong", "good-code", "oauth exchange failed"},
		{"bad code", fakeGoogle{}, testClientSecret, "bad-code", "oauth exchange failed"},
		{"token endpoint down", fakeGoogle{tokenStatus: http.StatusServiceUnavailable}, testClientSecret, "good-code", "oauth exchange failed"},
		{"userinfo rejected", fakeGoogle{userinfoStatus: http.StatusForbidden}, testClientSecret, "good-code", "status: 403"},
		{"userinfo not JSON", fakeGoogle{userinfo: "<html>"}, testClientSecret, "good-code", "decode userinfo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := withFakeGoogle(t, &tt.google)
			g := NewGoogleAdapter(testClientID, tt.secret, testRedirectURL)

			_, err := g.Exchange(ctx, tt.code)
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantText)
			}
		})
	}
}

// TestGoogleCredentialsLive checks the configured client ID and secret against
// the real Google token endpoint. It sends a deliberately invalid code: Google
// answers "invalid_grant" when the client credentials are valid and
// "invalid_client" when they are not, so no user sign-in is needed.
//
// Opt-in because it needs the network and real credentials:
//
//	GOOGLE_OAUTH_LIVE_TEST=1 go test ./internal/oauth -run Live -v
//
// Credentials come from GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET, else from
// oauth.google in config.json ($CONFIG_FILE, default the repo root).
func TestGoogleCredentialsLive(t *testing.T) {
	if os.Getenv("GOOGLE_OAUTH_LIVE_TEST") != "1" {
		t.Skip("set GOOGLE_OAUTH_LIVE_TEST=1 to check credentials against Google")
	}
	id, secret, redirect := liveCredentials(t)
	g := NewGoogleAdapter(id, secret, redirect)

	_, err := g.Exchange(context.Background(), "deliberately-invalid-code")
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		t.Fatalf("expected an OAuth error response from Google, got %v", err)
	}
	switch re.ErrorCode {
	case "invalid_grant":
		t.Logf("Google accepted client %s (the code was rejected, as intended)", id)
	case "invalid_client", "unauthorized_client":
		t.Fatalf("Google rejected the client credentials: %s %s", re.ErrorCode, re.ErrorDescription)
	default:
		t.Fatalf("unexpected response from Google: %s %s", re.ErrorCode, re.ErrorDescription)
	}
}

func liveCredentials(t *testing.T) (id, secret, redirect string) {
	t.Helper()
	path := os.Getenv("CONFIG_FILE")
	if path == "" {
		path = filepath.Join("..", "..", "config.json")
	}
	var cfg struct {
		OAuth struct {
			Google struct {
				ClientID     string `json:"clientId"`
				ClientSecret string `json:"clientSecret"`
				RedirectURL  string `json:"redirectUrl"`
			} `json:"google"`
		} `json:"oauth"`
	}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
	}
	id = firstNonEmpty(os.Getenv("GOOGLE_CLIENT_ID"), cfg.OAuth.Google.ClientID)
	secret = firstNonEmpty(os.Getenv("GOOGLE_CLIENT_SECRET"), cfg.OAuth.Google.ClientSecret)
	redirect = firstNonEmpty(os.Getenv("OAUTH_REDIRECT_URL"), cfg.OAuth.Google.RedirectURL)
	if id == "" || secret == "" {
		t.Skipf("no Google credentials in env or %s", path)
	}
	return id, secret, redirect
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
