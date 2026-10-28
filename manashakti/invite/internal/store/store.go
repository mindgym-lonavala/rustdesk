// Package store keeps invites in a JSON file. Only a SHA-256 of each invite token is
// stored, so the file alone cannot be used to redeem invites.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("invite not found")
	ErrExpired  = errors.New("invite expired")
	ErrRevoked  = errors.New("invite revoked")
	ErrUsed     = errors.New("invite already used")
)

type Invite struct {
	Key        string    `json:"key"` // sha256(token), hex
	Name       string    `json:"name"`
	CreatedBy  string    `json:"created_by"`
	AllowSetup bool      `json:"allow_setup"`
	Tags       []string  `json:"tags"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Revoked    bool      `json:"revoked"`
	// rustdesk-api token of the technician who created the invite; used to write
	// the address book entry when the invite is redeemed. Never sent to clients.
	APIToken   string     `json:"api_token"`
	HelpPeerID string     `json:"help_peer_id,omitempty"`
	SetupAt    *time.Time `json:"setup_at,omitempty"`
	SetupPeer  string     `json:"setup_peer_id,omitempty"`
}

// Usable reports why an invite cannot be used, or nil.
func (inv *Invite) Usable(now time.Time) error {
	switch {
	case inv.Revoked:
		return ErrRevoked
	case now.After(inv.ExpiresAt):
		return ErrExpired
	}
	return nil
}

type Store struct {
	mu      sync.Mutex
	path    string
	invites map[string]*Invite
	now     func() time.Time
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, invites: map[string]*Invite{}, now: time.Now}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Invite
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	for _, inv := range list {
		s.invites[inv.Key] = inv
	}
	return s, nil
}

func KeyOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Create stores a new invite and returns its token. The token is not kept.
func (s *Store) Create(name, createdBy, apiToken string, allowSetup bool, tags []string, ttl time.Duration) (string, *Invite, error) {
	token, err := newToken()
	if err != nil {
		return "", nil, err
	}
	now := s.now()
	inv := &Invite{
		Key:        KeyOf(token),
		Name:       name,
		CreatedBy:  createdBy,
		AllowSetup: allowSetup,
		Tags:       tags,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
		APIToken:   apiToken,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invites[inv.Key] = inv
	if err := s.saveLocked(); err != nil {
		delete(s.invites, inv.Key)
		return "", nil, err
	}
	return token, inv, nil
}

// Get returns a copy of a usable invite.
func (s *Store) Get(token string) (Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invites[KeyOf(token)]
	if !ok {
		return Invite{}, ErrNotFound
	}
	if err := inv.Usable(s.now()); err != nil {
		return Invite{}, err
	}
	return *inv, nil
}

// Update applies fn to a usable invite and saves. fn may return an error to abort.
func (s *Store) Update(token string, fn func(*Invite) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invites[KeyOf(token)]
	if !ok {
		return ErrNotFound
	}
	if err := inv.Usable(s.now()); err != nil {
		return err
	}
	before := *inv
	if err := fn(inv); err != nil {
		*inv = before
		return err
	}
	if err := s.saveLocked(); err != nil {
		*inv = before
		return err
	}
	return nil
}

func (s *Store) Revoke(key, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invites[key]
	if !ok || inv.CreatedBy != by {
		return ErrNotFound
	}
	inv.Revoked = true
	return s.saveLocked()
}

// ListBy returns invites created by a user, newest first, dropping ones older than 30 days.
func (s *Store) ListBy(by string) []Invite {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-30 * 24 * time.Hour)
	var out []Invite
	pruned := false
	for k, inv := range s.invites {
		if inv.ExpiresAt.Before(cutoff) {
			delete(s.invites, k)
			pruned = true
			continue
		}
		if inv.CreatedBy == by {
			out = append(out, *inv)
		}
	}
	if pruned {
		_ = s.saveLocked()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *Store) saveLocked() error {
	list := make([]*Invite, 0, len(s.invites))
	for _, inv := range s.invites {
		list = append(list, inv)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
