package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
)

type PeerService struct{ base }

func (s *PeerService) Get(ctx context.Context, id uuid.UUID) (*model.Peer, error) {
	return s.store.Peers().Get(ctx, id)
}

// UpdateUserName changes the caller's own display name.
func (s *PeerService) UpdateUserName(ctx context.Context, actor uuid.UUID, userName string) (*model.Peer, error) {
	name, err := cleanText("userName", userName, maxUserNameLen)
	if err != nil {
		return nil, err
	}
	return s.store.Peers().UpdateUserName(ctx, actor, name)
}

// UpdateProfileInput changes only the fields that are set.
type UpdateProfileInput struct {
	UserName *string
	Handle   *string
}

// UpdateProfile changes the caller's display name and/or handle together.
// A handle someone else has is ErrConflict.
func (s *PeerService) UpdateProfile(ctx context.Context, actor uuid.UUID, in UpdateProfileInput) (*model.Peer, error) {
	if in.UserName == nil && in.Handle == nil {
		return nil, invalid("nothing to update")
	}
	var name, handle string
	var err error
	if in.UserName != nil {
		if name, err = cleanText("userName", *in.UserName, maxUserNameLen); err != nil {
			return nil, err
		}
	}
	if in.Handle != nil {
		if handle, err = cleanHandle(*in.Handle); err != nil {
			return nil, err
		}
	}

	var peer *model.Peer
	err = s.store.WithTx(ctx, func(r Repos) error {
		if in.UserName != nil {
			if peer, err = r.Peers().UpdateUserName(ctx, actor, name); err != nil {
				return err
			}
		}
		if in.Handle != nil {
			peer, err = r.Peers().UpdateHandle(ctx, actor, handle)
			if errors.Is(err, ErrConflict) {
				return conflict("handle %q is taken", handle)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return peer, nil
}

// FindOrCreateByAuth is called by the OAuth callback. The first sign-in with a
// provider identity creates the peer (sign up) with a handle generated from
// userName; later sign-ins return the same peer. userName is only used on
// creation; the handler supplies a fallback when the provider gives no name.
func (s *PeerService) FindOrCreateByAuth(ctx context.Context, provider, subject, userName string) (peer *model.Peer, created bool, err error) {
	if provider == "" || subject == "" {
		return nil, false, invalid("auth provider and subject are required")
	}

	existing, err := s.store.Peers().GetByAuth(ctx, provider, subject)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}

	name, err := cleanText("userName", userName, maxUserNameLen)
	if err != nil {
		return nil, false, err
	}
	base := handleBase(name)
	for attempt := 0; attempt < maxHandleAttempts; attempt++ {
		p, err := s.store.Peers().Create(ctx, NewPeer{
			PeerID:       s.newID(),
			Handle:       handleCandidate(base, attempt),
			UserName:     name,
			AuthProvider: provider,
			AuthSubject:  subject,
		})
		if err == nil {
			return p, true, nil
		}
		if !errors.Is(err, ErrConflict) {
			return nil, false, err
		}
		// Either a concurrent first sign-in won the race (use its row) or
		// the handle is taken (try another).
		if winner, getErr := s.store.Peers().GetByAuth(ctx, provider, subject); getErr == nil {
			return winner, false, nil
		}
	}
	return nil, false, fmt.Errorf("could not find a free handle for %q after %d attempts", base, maxHandleAttempts)
}

// ---- Handles ----

const (
	minHandleLen      = 2
	maxHandleLen      = 32
	handleBaseLen     = 24 // leaves room for "_NNNN"
	maxHandleAttempts = 8
)

var (
	handlePattern  = regexp.MustCompile(`^[a-z0-9._]{2,32}$`)
	handleInvalid  = regexp.MustCompile(`[^a-z0-9._]+`)
	handleRepeated = regexp.MustCompile(`[._]{2,}`)
)

// cleanHandle normalizes a user-chosen handle (trim, drop a leading "@",
// lowercase) and checks its format.
func cleanHandle(s string) (string, error) {
	h := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
	if !handlePattern.MatchString(h) {
		return "", invalid("handle must be %d-%d characters of a-z, 0-9, '.' or '_'", minHandleLen, maxHandleLen)
	}
	return h, nil
}

// handleBase turns a display name into a handle: "Ada Lovelace" -> "ada.lovelace".
// Names with nothing usable (e.g. only non-Latin letters) become "user".
func handleBase(name string) string {
	h := strings.ToLower(strings.TrimSpace(name))
	h = strings.Join(strings.Fields(h), ".")
	h = handleInvalid.ReplaceAllString(h, "")
	h = handleRepeated.ReplaceAllStringFunc(h, func(m string) string { return m[:1] })
	h = strings.Trim(h, "._")
	if len(h) > handleBaseLen {
		h = strings.TrimRight(h[:handleBaseLen], "._")
	}
	if len(h) < minHandleLen {
		h = "user"
	}
	return h
}

// handleCandidate is the base itself first, then the base with a random
// 4-digit suffix.
func handleCandidate(base string, attempt int) string {
	if attempt == 0 && base != "user" {
		return base
	}
	return fmt.Sprintf("%s_%04d", base, rand.IntN(10000))
}

// cleanSearch normalizes a search query: trimmed, lowercased, without a
// leading "@", and long enough that search cannot list every user.
func cleanSearch(q string, max int) (string, error) {
	q = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(q), "@"))
	if n := len([]rune(q)); n < minSearchLen || n > max {
		return "", invalid("search query must be %d-%d characters", minSearchLen, max)
	}
	return q, nil
}

const (
	minSearchLen = 2
	searchLimit  = 10
)
