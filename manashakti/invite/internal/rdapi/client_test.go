package rdapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeRustdeskAPI mimics the rustdesk-api v2.7 responses used by Client.
func fakeRustdeskAPI(t *testing.T, existing []string, calls *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.Method+" "+r.URL.Path)
		if r.URL.Path != "/api/login" && r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"Unauthorized"}`))
			return
		}
		switch r.URL.Path {
		case "/api/login":
			var f map[string]any
			json.NewDecoder(r.Body).Decode(&f)
			if f["password"] != "pw" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"Username or password error."}`))
				return
			}
			w.Write([]byte(`{"access_token":"good","type":"access_token","user":{"name":"tech1"}}`))
		case "/api/ab/shared/profiles":
			w.Write([]byte(`{"total":0,"data":[{"guid":"1-2-0","name":"My address book","rule":3},{"guid":"1-2-1","name":"Manashakti PCs","rule":3},{"guid":"1-3-4","name":"ReadOnly","rule":1}]}`))
		case "/api/ab/peers":
			if r.URL.Query().Get("ab") != "1-2-1" {
				t.Errorf("peers for wrong book %q", r.URL.Query().Get("ab"))
			}
			var data []map[string]string
			for _, id := range existing {
				data = append(data, map[string]string{"id": id})
			}
			json.NewEncoder(w).Encode(map[string]any{"total": len(data), "data": data})
		case "/api/ab/peer/add/1-2-1", "/api/ab/peer/update/1-2-1":
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			if _, ok := p["tags"].([]any); !ok {
				t.Errorf("tags not an array: %v", p["tags"])
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestLoginAndBooks(t *testing.T) {
	var calls []string
	srv := fakeRustdeskAPI(t, nil, &calls)
	defer srv.Close()
	c := New(srv.URL)
	ctx := context.Background()

	if _, _, err := c.Login(ctx, "tech1", "bad"); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("want ErrBadLogin, got %v", err)
	}
	tok, name, err := c.Login(ctx, "tech1", "pw")
	if err != nil || tok != "good" || name != "tech1" {
		t.Fatalf("login: %q %q %v", tok, name, err)
	}
	if g, err := c.BookGUID(ctx, tok, "Manashakti PCs"); err != nil || g != "1-2-1" {
		t.Fatalf("book: %q %v", g, err)
	}
	if _, err := c.BookGUID(ctx, tok, "ReadOnly"); err == nil {
		t.Fatal("read-only book accepted")
	}
	if _, err := c.BookGUID(ctx, tok, "Missing"); err == nil {
		t.Fatal("missing book accepted")
	}
	var ae *APIError
	if _, err := c.BookGUID(ctx, "expired", "Manashakti PCs"); !errors.As(err, &ae) || ae.Status != 401 {
		t.Fatalf("want 401 APIError, got %v", err)
	}
}

func TestUpsertAddsOrUpdates(t *testing.T) {
	for _, tc := range []struct {
		existing []string
		want     string
	}{
		{nil, "POST /api/ab/peer/add/1-2-1"},
		{[]string{"123456789"}, "PUT /api/ab/peer/update/1-2-1"},
	} {
		var calls []string
		srv := fakeRustdeskAPI(t, tc.existing, &calls)
		err := New(srv.URL).UpsertPeer(context.Background(), "good", "1-2-1", Peer{ID: "123456789", Alias: "PC"})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if last := calls[len(calls)-1]; last != tc.want {
			t.Fatalf("existing=%v: last call %q, want %q", tc.existing, last, tc.want)
		}
	}
}

func TestOidcBeginAndQuery(t *testing.T) {
	authed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oidc/auth":
			var f map[string]any
			json.NewDecoder(r.Body).Decode(&f)
			if f["op"] != "Google" {
				t.Errorf("op %v", f["op"])
			}
			w.Write([]byte(`{"code":"st","url":"https://accounts.google.com/x"}`))
		case "/api/oidc/auth-query":
			if r.URL.Query().Get("code") != "st" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"OauthExpired"}`))
				return
			}
			if !authed { // rustdesk-api v2.7 answers 200 while the user is still signing in
				w.Write([]byte(`{"message":"Authorization in progress, please login and bind","error":"No authed oidc is found"}`))
				return
			}
			w.Write([]byte(`{"access_token":"tok","type":"access_token","user":{"name":"tech1"}}`))
		}
	}))
	defer srv.Close()
	c, ctx := New(srv.URL), context.Background()
	code, u, err := c.OidcBegin(ctx, "Google", "id", "uuid")
	if err != nil || code != "st" || u != "https://accounts.google.com/x" {
		t.Fatalf("begin: %q %q %v", code, u, err)
	}
	if _, _, err := c.OidcQuery(ctx, "st", "id", "uuid"); !errors.Is(err, ErrOidcPending) {
		t.Fatalf("want pending, got %v", err)
	}
	authed = true
	if tok, name, err := c.OidcQuery(ctx, "st", "id", "uuid"); err != nil || tok != "tok" || name != "tech1" {
		t.Fatalf("query: %q %q %v", tok, name, err)
	}
	if _, _, err := c.OidcQuery(ctx, "other", "id", "uuid"); err == nil || errors.Is(err, ErrOidcPending) {
		t.Fatalf("expired code: %v", err)
	}
}
