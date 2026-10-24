package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateEnv clears every variable loadConfig reads, so the developer's shell
// cannot change the result. getEnv treats empty as unset.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CONFIG_FILE", "PORT", "DATABASE_URL", "JWT_SECRET", "GOOGLE_CLIENT_ID",
		"GOOGLE_CLIENT_SECRET", "OAUTH_REDIRECT_URL", "POST_LOGIN_REDIRECT", "ALLOWED_ORIGINS",
		"INVITE_LINK_BASE_URL", "INVITE_APP_LINK_BASE_URL",
	} {
		t.Setenv(k, "")
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	return path
}

const sampleConfig = `{
  "server":   {"port": "9090", "allowedOrigins": ["http://a.test", "http://b.test"]},
  "database": {"url": "postgres://file", "maxOpenConns": 7, "maxIdleConns": 2, "connMaxLifetime": "90s"},
  "migrations": {"schemaDir": "db", "waitSeconds": 5, "skip": false},
  "auth":     {"jwtSecret": "file-secret", "jwtDuration": "2h", "cookieSecure": true, "postLoginRedirect": "http://app.test/"},
  "invites":  {"linkBaseUrl": "http://app.test/invite/", "appLinkBaseUrl": "app.test://invite/"},
  "oauth":    {"google": {"clientId": "file-id", "clientSecret": "file-oauth-secret", "redirectUrl": "http://api.test/cb"}}
}`

func TestLoadConfigFromFile(t *testing.T) {
	isolateEnv(t)
	writeConfig(t, sampleConfig)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"Port", cfg.Port, "9090"},
		{"DatabaseURL", cfg.DatabaseURL, "postgres://file"},
		{"MaxOpenConns", cfg.MaxOpenConns, 7},
		{"MaxIdleConns", cfg.MaxIdleConns, 2},
		{"ConnMaxLifetime", cfg.ConnMaxLifetime, 90 * time.Second},
		{"JWTSecret", cfg.JWTSecret, "file-secret"},
		{"JWTDuration", cfg.JWTDuration, 2 * time.Hour},
		{"CookieSecure", cfg.CookieSecure, true},
		{"PostLoginRedirect", cfg.PostLoginRedirect, "http://app.test/"},
		{"GoogleClientID", cfg.GoogleClientID, "file-id"},
		{"GoogleSecret", cfg.GoogleSecret, "file-oauth-secret"},
		{"OAuthRedirectURL", cfg.OAuthRedirectURL, "http://api.test/cb"},
		{"AllowedOrigins", strings.Join(cfg.AllowedOrigins, ","), "http://a.test,http://b.test"},
		{"InviteLinkBase", cfg.InviteLinkBase, "http://app.test/invite/"},
		{"InviteAppLinkBase", cfg.InviteAppLinkBase, "app.test://invite/"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoadConfigEnvOverridesFile(t *testing.T) {
	isolateEnv(t)
	writeConfig(t, sampleConfig)
	t.Setenv("GOOGLE_CLIENT_ID", "env-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "env-oauth-secret")
	t.Setenv("DATABASE_URL", "postgres://env")
	t.Setenv("ALLOWED_ORIGINS", "http://c.test")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GoogleClientID != "env-id" || cfg.GoogleSecret != "env-oauth-secret" {
		t.Errorf("google credentials = %q/%q, want the env values", cfg.GoogleClientID, cfg.GoogleSecret)
	}
	if cfg.DatabaseURL != "postgres://env" {
		t.Errorf("DatabaseURL = %q, want the env value", cfg.DatabaseURL)
	}
	if strings.Join(cfg.AllowedOrigins, ",") != "http://c.test" {
		t.Errorf("AllowedOrigins = %v", cfg.AllowedOrigins)
	}
	// Keys without an env override still come from the file.
	if cfg.OAuthRedirectURL != "http://api.test/cb" {
		t.Errorf("OAuthRedirectURL = %q, want the file value", cfg.OAuthRedirectURL)
	}
}

func TestLoadConfigMissingFileUsesDefaults(t *testing.T) {
	isolateEnv(t)
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "absent.json"))

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "8080" || cfg.JWTDuration != 15*time.Minute || cfg.SessionDuration != 30*24*time.Hour || cfg.GoogleClientID != "" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadConfigRejectsBadFiles(t *testing.T) {
	tests := []struct{ name, body, wantText string }{
		// encoding/json matches keys case-insensitively, so only a different
		// spelling (not "clientID") is an unknown field.
		{"misspelled key", `{"oauth":{"google":{"client_id":"x"}}}`, `unknown field "client_id"`},
		{"bad duration", `{"auth":{"jwtDuration":"forever"}}`, "auth.jwtDuration"},
		{"access outlives session", `{"auth":{"jwtDuration":"48h","sessionDuration":"24h"}}`, "shorter than auth.sessionDuration"},
		{"not JSON", `{`, "parsing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateEnv(t)
			writeConfig(t, tt.body)
			_, err := loadConfig()
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantText)
			}
		})
	}
}

// TestRepoConfigIsValid loads the real config.json at the repo root, so a typo
// there fails the build instead of the deployment.
func TestRepoConfigIsValid(t *testing.T) {
	isolateEnv(t)
	path := filepath.Join("..", "..", "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no %s (it is git-ignored): %v", path, err)
	}
	t.Setenv("CONFIG_FILE", path)
	if _, err := loadConfig(); err != nil {
		t.Fatal(err)
	}
}

// The committed template must load once its secrets are supplied, and must
// not load without them.
func TestExampleConfig(t *testing.T) {
	isolateEnv(t)
	t.Setenv("CONFIG_FILE", filepath.Join("..", "..", "config.example.json"))

	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "jwtSecret") {
		t.Fatalf("err = %v, want the missing jwtSecret to be rejected", err)
	}

	t.Setenv("JWT_SECRET", "secret-from-the-environment")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JWTSecret != "secret-from-the-environment" || cfg.GoogleClientID != "" || cfg.GoogleSecret != "" {
		t.Errorf("example config must carry no secrets of its own: %+v", cfg)
	}
}
