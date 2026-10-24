package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dhanvin018/client-directory/internal/auth"
	"github.com/dhanvin018/client-directory/internal/handler"
	"github.com/dhanvin018/client-directory/internal/middleware"
	"github.com/dhanvin018/client-directory/internal/oauth"
	"github.com/dhanvin018/client-directory/internal/repository"
	"github.com/dhanvin018/client-directory/internal/router"
	"github.com/dhanvin018/client-directory/internal/service"

	_ "github.com/lib/pq"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := loadConfig()
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// 1. Database Connection
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		logger.Error("Failed to initialize database connection driver", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		logger.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	logger.Info("Successfully connected to PostgreSQL database")

	// 2. Initialize Repositories and Services
	store := repository.NewStore(db)
	services := service.New(store, service.WithSessionTTL(cfg.SessionDuration))

	// 3. Access tokens: short-lived JWTs, checked without the database.
	// Sessions (refresh tokens, logout) live in Postgres via services.Sessions.
	accessTokens := auth.NewAccessTokens(cfg.JWTSecret, cfg.JWTDuration)

	// 4. Initialize OAuth Providers
	providers := make(map[string]handler.OAuthProvider)
	if cfg.GoogleClientID != "" && cfg.GoogleSecret != "" {
		providers["google"] = oauth.NewGoogleAdapter(cfg.GoogleClientID, cfg.GoogleSecret, cfg.OAuthRedirectURL)
		logger.Info("Registered Google OAuth provider")
	} else {
		logger.Warn("Google OAuth credentials missing; Google sign-in disabled")
	}

	// 5. Initialize Handlers
	handlers := router.Handlers{
		Auth: handler.NewAuthHandler(handler.AuthConfig{
			Providers:         providers,
			Tokens:            accessTokens,
			Sessions:          services.Sessions,
			Peers:             services.Peers,
			Desktop:           services.Desktop,
			CookieName:        "session",
			CookieSecure:      cfg.CookieSecure,
			PostLoginRedirect: cfg.PostLoginRedirect,
		}),
		Peers:     handler.NewPeerHandler(services.Peers, logger),
		Rooms:     handler.NewRoomHandler(services.Rooms, logger),
		Documents: handler.NewDocumentHandler(services.Documents, logger),
		Members:   handler.NewMemberHandler(services.Members, logger),
		Events:    handler.NewEventHandler(services.Events, logger),
		Invites:   handler.NewInviteHandler(services.Invites, cfg.InviteLinkBase, cfg.InviteAppLinkBase, logger),
	}

	// 6. Build Router and Middleware Chain
	rootHandler := router.New(router.Config{
		Handlers: handlers,
		Authenticate: middleware.Authenticate(middleware.AuthConfig{
			Verifier:   accessTokens,
			CookieName: "session",
		}),
		Global: []router.Middleware{
			middleware.RequestID(),
			middleware.Logging(logger),
			middleware.Recover(logger),
			middleware.CORS(middleware.CORSConfig{
				AllowedOrigins:   cfg.AllowedOrigins,
				AllowCredentials: true,
			}),
		},
	})

	// 7. Start HTTP Server with Graceful Shutdown
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      rootHandler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("Starting server", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown on signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Server stopped gracefully")
}
