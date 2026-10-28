package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateGetPersistAndExpire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invites.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	token, _, err := s.Create("Asha", "tech1", "apitok", true, []string{"Office"}, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 43 {
		t.Fatalf("token length %d", len(token))
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), token) {
		t.Fatal("token stored in plain text")
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s2.now = s.now
	inv, err := s2.Get(token)
	if err != nil || inv.Name != "Asha" || !inv.AllowSetup {
		t.Fatalf("reload: %+v %v", inv, err)
	}

	now = now.Add(25 * time.Hour)
	if _, err := s2.Get(token); !errors.Is(err, ErrExpired) {
		t.Fatalf("want expired, got %v", err)
	}
	if _, err := s2.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestUpdateRollbackAndRevoke(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "i.json"))
	token, inv, _ := s.Create("PC", "tech1", "t", true, nil, time.Hour)

	boom := errors.New("boom")
	err := s.Update(token, func(i *Invite) error { i.HelpPeerID = "123"; return boom })
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	got, _ := s.Get(token)
	if got.HelpPeerID != "" {
		t.Fatal("failed update not rolled back")
	}

	if err := s.Revoke(inv.Key, "someone-else"); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked another user's invite")
	}
	if err := s.Revoke(inv.Key, "tech1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(token); !errors.Is(err, ErrRevoked) {
		t.Fatalf("want revoked, got %v", err)
	}
}
