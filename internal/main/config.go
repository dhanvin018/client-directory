package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

type Config struct {
	Port              string
	DatabaseURL       string
	MaxOpenConns      int
	MaxIdleConns      int
	ConnMaxLifetime   time.Duration
	JWTSecret         string
	JWTDuration       time.Duration // access token lifetime
	SessionDuration   time.Duration // refresh token lifetime, extended on every refresh
	CookieSecure      bool
	GoogleClientID    string
	GoogleSecret      string
	OAuthRedirectURL  string
	PostLoginRedirect string
	AllowedOrigins    []string
	InviteLinkBase    string
	InviteAppLinkBase string
}

// fileConfig mirrors config.json. The "migrations" section is read by
// scripts/migrate.sh, not by the server.
type fileConfig struct {
	Server struct {
		Port           string   `json:"port"`
		AllowedOrigins []string `json:"allowedOrigins"`
	} `json:"server"`
	Database struct {
		URL             string `json:"url"`
		MaxOpenConns    int    `json:"maxOpenConns"`
		MaxIdleConns    int    `json:"maxIdleConns"`
		ConnMaxLifetime string `json:"connMaxLifetime"`
	} `json:"database"`
	Migrations json.RawMessage `json:"migrations"`
	Auth       struct {
		JWTSecret         string `json:"jwtSecret"`
		JWTDuration       string `json:"jwtDuration"`
		SessionDuration   string `json:"sessionDuration"`
		CookieSecure      bool   `json:"cookieSecure"`
		PostLoginRedirect string `json:"postLoginRedirect"`
	} `json:"auth"`
	Invites struct {
		LinkBaseURL    string `json:"linkBaseUrl"`
		AppLinkBaseURL string `json:"appLinkBaseUrl"`
	} `json:"invites"`
	OAuth struct {
		Google struct {
			ClientID     string `json:"clientId"`
			ClientSecret string `json:"clientSecret"`
			RedirectURL  string `json:"redirectUrl"`
		} `json:"google"`
	} `json:"oauth"`
}

func defaultFileConfig() fileConfig {
	var fc fileConfig
	fc.Server.Port = "8080"
	fc.Server.AllowedOrigins = []string{"http://localhost:3000"}
	fc.Database.URL = "postgres://postgres:postgres@localhost:5432/client_directory?sslmode=disable"
	fc.Database.MaxOpenConns = 25
	fc.Database.MaxIdleConns = 5
	fc.Database.ConnMaxLifetime = "5m"
	fc.Auth.JWTSecret = "default-dev-secret-change-in-production"
	fc.Auth.JWTDuration = "15m"
	fc.Auth.SessionDuration = "720h"
	fc.Auth.PostLoginRedirect = "http://localhost:3000/"
	fc.OAuth.Google.RedirectURL = "http://localhost:8080/auth/google/callback"
	fc.Invites.LinkBaseURL = "http://localhost:3000/invite/"
	fc.Invites.AppLinkBaseURL = "collabdoc://invite/"
	return fc
}

// loadConfig builds the config from, in increasing precedence: built-in
// defaults, the JSON file at $CONFIG_FILE (default ./config.json), and
// environment variables. A missing file is not an error.
func loadConfig() (Config, error) {
	fc := defaultFileConfig()

	path := getEnv("CONFIG_FILE", "config.json")
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Defaults and environment only.
	case err != nil:
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	default:
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields() // catch typos in key names
		if err := dec.Decode(&fc); err != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", path, err)
		}
	}

	cfg := Config{
		Port:              getEnv("PORT", fc.Server.Port),
		DatabaseURL:       getEnv("DATABASE_URL", fc.Database.URL),
		MaxOpenConns:      fc.Database.MaxOpenConns,
		MaxIdleConns:      fc.Database.MaxIdleConns,
		JWTSecret:         getEnv("JWT_SECRET", fc.Auth.JWTSecret),
		CookieSecure:      fc.Auth.CookieSecure,
		GoogleClientID:    getEnv("GOOGLE_CLIENT_ID", fc.OAuth.Google.ClientID),
		GoogleSecret:      getEnv("GOOGLE_CLIENT_SECRET", fc.OAuth.Google.ClientSecret),
		OAuthRedirectURL:  getEnv("OAUTH_REDIRECT_URL", fc.OAuth.Google.RedirectURL),
		PostLoginRedirect: getEnv("POST_LOGIN_REDIRECT", fc.Auth.PostLoginRedirect),
		AllowedOrigins:    fc.Server.AllowedOrigins,
		InviteLinkBase:    getEnv("INVITE_LINK_BASE_URL", fc.Invites.LinkBaseURL),
		InviteAppLinkBase: getEnv("INVITE_APP_LINK_BASE_URL", fc.Invites.AppLinkBaseURL),
	}
	if v := getEnv("ALLOWED_ORIGINS", ""); v != "" {
		cfg.AllowedOrigins = strings.Split(v, ",")
	}
	if cfg.ConnMaxLifetime, err = time.ParseDuration(fc.Database.ConnMaxLifetime); err != nil {
		return Config{}, fmt.Errorf("database.connMaxLifetime: %w", err)
	}
	if cfg.JWTDuration, err = time.ParseDuration(fc.Auth.JWTDuration); err != nil {
		return Config{}, fmt.Errorf("auth.jwtDuration: %w", err)
	}
	if cfg.SessionDuration, err = time.ParseDuration(fc.Auth.SessionDuration); err != nil {
		return Config{}, fmt.Errorf("auth.sessionDuration: %w", err)
	}
	// A logged-out access token works until it expires, so it must be short;
	// the session (refresh token) is what keeps the user signed in.
	if cfg.JWTDuration <= 0 || cfg.SessionDuration <= cfg.JWTDuration {
		return Config{}, errors.New("auth.jwtDuration must be positive and shorter than auth.sessionDuration")
	}
	// An empty key would sign sessions that anyone can forge. config.example.json
	// ships it blank on purpose, so a missing secret fails here instead.
	if cfg.JWTSecret == "" {
		return Config{}, errors.New("auth.jwtSecret is empty: set it in the config file or JWT_SECRET")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}
