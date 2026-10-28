package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/mindgym-lonavala/rustdesk/manashakti/invite/internal/rdapi"
	"github.com/mindgym-lonavala/rustdesk/manashakti/invite/internal/store"
)

type fakeAPI struct {
	mu    sync.Mutex
	peers map[string]rdapi.Peer
	fail  error
}

func (f *fakeAPI) Login(_ context.Context, u, p string) (string, string, error) {
	if u == "tech1" && p == "right" {
		return "tok-tech1", "Tech One", nil
	}
	return "", "", rdapi.ErrBadLogin
}

func (f *fakeAPI) BookGUID(_ context.Context, token, name string) (string, error) {
	if token != "tok-tech1" || name != "Manashakti PCs" {
		return "", &rdapi.APIError{Status: 401, Message: "unauthorized"}
	}
	return "1-2-1", nil
}

func (f *fakeAPI) UpsertPeer(_ context.Context, token, guid string, p rdapi.Peer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.peers[p.ID] = p
	return nil
}

const public = "https://support.example.org:8443"

func newTestServer(t *testing.T) (*Server, *fakeAPI, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "data", "invites.json"))
	if err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(dir, "files")
	os.MkdirAll(files, 0o700)
	for _, n := range []string{FileHelper, FilePortable, FileMSI} {
		os.WriteFile(filepath.Join(files, n), []byte("binary:"+n), 0o600)
	}
	api := &fakeAPI{peers: map[string]rdapi.Peer{}}
	s := &Server{Store: st, API: api, PublicURL: public, Book: "Manashakti PCs", FilesDir: files}
	return s, api, s.Handler()
}

// req sends a request as if it came through Caddy from clientIP.
func req(h http.Handler, method, path, clientIP string, form url.Values, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if form != nil {
		r = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", public)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", clientIP)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func login(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	w := req(h, "POST", "/invites/login", "192.168.50.5", url.Values{"username": {"tech1"}, "password": {"right"}}, nil, "")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func createInvite(t *testing.T, h http.Handler, c *http.Cookie, name string, setup bool) string {
	t.Helper()
	f := url.Values{"name": {name}, "tags": {"Office"}}
	if setup {
		f.Set("allow_setup", "on")
	}
	w := req(h, "POST", "/invites/create", "192.168.50.5", f, c, "")
	m := regexp.MustCompile(regexp.QuoteMeta(public) + `/i/([A-Za-z0-9_-]{43})`).FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("no link in page: %d %s", w.Code, w.Body)
	}
	return m[1]
}

func TestTechnicianPagesAreOfficeOnlyAndNeedLogin(t *testing.T) {
	_, _, h := newTestServer(t)
	if rp := req(h, "GET", "/invites", "192.168.50.5", nil, nil, "").Header().Get("Referrer-Policy"); rp == "no-referrer" {
		t.Fatal("no-referrer makes browsers send Origin: null on the sign-in form")
	}
	if w := req(h, "GET", "/invites", "203.0.113.9", nil, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("public IP got %d", w.Code)
	}
	w := req(h, "POST", "/invites/login", "192.168.50.5", url.Values{"username": {"tech1"}, "password": {"wrong"}}, nil, "")
	if !strings.Contains(w.Body.String(), "Sign-in failed") {
		t.Fatalf("bad password: %d %s", w.Code, w.Body)
	}
	// create without a session redirects back to sign-in
	w = req(h, "POST", "/invites/create", "192.168.50.5", url.Values{"name": {"X"}}, nil, "")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create without session: %d", w.Code)
	}
	// cross-site POST rejected
	r := httptest.NewRequest("POST", "/invites/login", strings.NewReader("username=tech1&password=right"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.example")
	r.RemoteAddr = "192.168.50.5:1"
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login got %d", rw.Code)
	}
}

func TestHelpInviteFlow(t *testing.T) {
	_, api, h := newTestServer(t)
	c := login(t, h)
	token := createInvite(t, h, c, "Asha", false)

	page := req(h, "GET", "/i/"+token, "198.51.100.7", nil, nil, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Get help now") || strings.Contains(page.Body.String(), "/helper/setup") {
		t.Fatalf("landing: %d", page.Code)
	}
	dl := req(h, "GET", "/i/"+token+"/helper/help", "198.51.100.7", nil, nil, "")
	if cd := dl.Header().Get("Content-Disposition"); !strings.Contains(cd, "ManashaktiSupport-Help-"+token+".exe") {
		t.Fatalf("helper filename: %q", cd)
	}
	if w := req(h, "GET", "/i/"+token+"/helper/setup", "198.51.100.7", nil, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("setup helper offered for help-only invite: %d", w.Code)
	}
	if w := req(h, "GET", "/api/invite/"+token+"/file/msi", "198.51.100.7", nil, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("msi served for help-only invite: %d", w.Code)
	}

	w := req(h, "POST", "/api/invite/"+token+"/enroll", "198.51.100.7", nil, nil, `{"mode":"help","id":"347494540","hostname":"ASHA-PC","username":"asha"}`)
	if w.Code != 200 {
		t.Fatalf("enroll help: %d %s", w.Code, w.Body)
	}
	p := api.peers["347494540"]
	if p.Alias != "Invite: Asha" || p.Password != "" || len(p.Tags) != 1 || p.Tags[0] != "Invite" {
		t.Fatalf("peer: %+v", p)
	}
	tech := req(h, "GET", "/invites", "192.168.50.5", nil, c, "")
	if !strings.Contains(tech.Body.String(), "Waiting for you (ID 347494540)") {
		t.Fatal("status not shown to technician")
	}
}

func TestSetupInviteIsSingleUseAndValidated(t *testing.T) {
	_, api, h := newTestServer(t)
	c := login(t, h)
	token := createInvite(t, h, c, "Reception PC", true)

	if w := req(h, "GET", "/api/invite/"+token+"/file/msi", "198.51.100.7", nil, nil, ""); w.Code != 200 || w.Body.String() != "binary:"+FileMSI {
		t.Fatalf("msi: %d", w.Code)
	}
	bad := req(h, "POST", "/api/invite/"+token+"/enroll", "198.51.100.7", nil, nil, `{"mode":"setup","id":"211867013","password":"short"}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("weak password accepted: %d", bad.Code)
	}
	body := `{"mode":"setup","id":"211867013","password":"Abcdefghijkmnpqrstuvwx23","hostname":"RECEPTION"}`
	if w := req(h, "POST", "/api/invite/"+token+"/enroll", "198.51.100.7", nil, nil, body); w.Code != 200 {
		t.Fatalf("enroll setup: %d %s", w.Code, w.Body)
	}
	p := api.peers["211867013"]
	if p.Alias != "Reception PC" || p.Password != "Abcdefghijkmnpqrstuvwx23" || len(p.Tags) != 1 || p.Tags[0] != "Office" {
		t.Fatalf("peer: %+v", p)
	}
	if w := req(h, "POST", "/api/invite/"+token+"/enroll", "198.51.100.7", nil, nil, body); w.Code != http.StatusConflict {
		t.Fatalf("second setup: %d", w.Code)
	}
	if page := req(h, "GET", "/i/"+token, "198.51.100.7", nil, nil, ""); strings.Contains(page.Body.String(), "/helper/setup") {
		t.Fatal("setup still offered after use")
	}
}

func TestSetupClaimReleasedWhenAddressBookFails(t *testing.T) {
	_, api, h := newTestServer(t)
	c := login(t, h)
	token := createInvite(t, h, c, "PC", true)
	api.fail = &rdapi.APIError{Status: 401, Message: "expired"}
	body := `{"mode":"setup","id":"123456789","password":"Abcdefghijkmnpqrstuvwx23"}`
	w := req(h, "POST", "/api/invite/"+token+"/enroll", "198.51.100.7", nil, nil, body)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "session has expired") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	api.fail = nil
	if w := req(h, "POST", "/api/invite/"+token+"/enroll", "198.51.100.7", nil, nil, body); w.Code != 200 {
		t.Fatalf("retry after failure: %d %s", w.Code, w.Body)
	}
}

func TestInvalidAndRevokedTokens(t *testing.T) {
	s, _, h := newTestServer(t)
	if w := req(h, "GET", "/i/not-a-token", "198.51.100.7", nil, nil, ""); w.Code != http.StatusNotFound {
		t.Fatalf("bad token: %d", w.Code)
	}
	c := login(t, h)
	token := createInvite(t, h, c, "PC", true)
	key := s.Store.ListBy("tech1")[0].Key
	req(h, "POST", "/invites/revoke", "192.168.50.5", url.Values{"key": {key}}, c, "")
	if w := req(h, "GET", "/i/"+token, "198.51.100.7", nil, nil, ""); w.Code != http.StatusGone {
		t.Fatalf("revoked invite: %d", w.Code)
	}
}
