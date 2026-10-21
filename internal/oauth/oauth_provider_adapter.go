package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dhanvin018/client-directory/internal/handler"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GoogleAdapter implements handler.OAuthProvider for Google OAuth2.
type GoogleAdapter struct {
	config *oauth2.Config
}

type googleUserInfo struct {
	Subject string `json:"sub"`
	Name    string `json:"name"`
	Email   string `json:"email"`
}

func NewGoogleAdapter(clientID, clientSecret, redirectURL string) *GoogleAdapter {
	return &GoogleAdapter{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes: []string{
				"https://www.googleapis.com/auth/userinfo.profile",
				"https://www.googleapis.com/auth/userinfo.email",
			},
			Endpoint: google.Endpoint,
		},
	}
}

func (g *GoogleAdapter) AuthCodeURL(state string) string {
	return g.config.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (g *GoogleAdapter) Exchange(ctx context.Context, code string) (handler.Identity, error) {
	token, err := g.config.Exchange(ctx, code)
	if err != nil {
		return handler.Identity{}, fmt.Errorf("oauth exchange failed: %w", err)
	}

	client := g.config.Client(ctx, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v3/userinfo", nil)
	if err != nil {
		return handler.Identity{}, fmt.Errorf("failed to create userinfo request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return handler.Identity{}, fmt.Errorf("failed to fetch userinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return handler.Identity{}, fmt.Errorf("userinfo endpoint returned status: %d", resp.StatusCode)
	}

	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return handler.Identity{}, fmt.Errorf("failed to decode userinfo: %w", err)
	}

	return handler.Identity{
		Subject: info.Subject,
		Name:    info.Name,
		Email:   info.Email,
	}, nil
}
