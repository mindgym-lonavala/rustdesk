package main

import (
	"context"
	"errors"
	"html"
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
	mu         sync.Mutex
	peers      map[string]rdapi.Peer
	fail       error
	oidcUser   string // set once the "Google" sign-in has completed
	oidcCode   string
	oidcIDUUID string
}

func (f *fakeAPI) OidcBegin(_ context.Context, op, id, uuid string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.oidcCode, f.oidcIDUUID = "state123", id+"|"+uuid
	return f.oidcCode, "https://accounts.google.com/o/oauth2/auth?state=state123", nil
}

func (f *fakeAPI) OidcQuery(_ context.Context, code, id, uuid string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if code != f.oidcCode || id+"|"+uuid != f.oidcIDUUID {
		return "", "", &rdapi.APIError{Status: 400, Message: "OauthExpired"}
	}
	if f.oidcUser == "" {
		return "", "", rdapi.ErrOidcPending
	}
	return "tok-tech1", f.oidcUser, nil
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
	for _, n := range []string{FileHelper, FilePortable, FileMSI, FileLinux} {
		os.WriteFile(filepath.Join(files, n), []byte("binary:"+n), 0o600)
	}
	api := &fakeAPI{peers: map[string]rdapi.Peer{}}
	s := &Server{Store: st, API: api, PublicURL: public, Book: "Manashakti PCs", FilesDir: files, GoogleOp: "Google"}
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
	pub := req(h, "GET", "/invites", "203.0.113.9", nil, nil, "")
	if pub.Code != 200 || strings.Contains(pub.Body.String(), `action="/invites/login"`) || !strings.Contains(pub.Body.String(), "Continue with Google") {
		t.Fatalf("public page should offer Google only: %d", pub.Code)
	}
	if w := req(h, "POST", "/invites/login", "203.0.113.9", url.Values{"username": {"tech1"}, "password": {"right"}}, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("password sign-in from outside the office got %d", w.Code)
	}
	if w := req(h, "POST", "/invites/create", "203.0.113.9", url.Values{"name": {"X"}}, nil, ""); w.Code != http.StatusSeeOther {
		t.Fatalf("create without session from outside: %d", w.Code)
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

func TestGoogleSignInFromOutsideTheOffice(t *testing.T) {
	_, api, h := newTestServer(t)
	const ip = "203.0.113.9"
	start := req(h, "POST", "/invites/google", ip, url.Values{}, nil, "")
	if !strings.Contains(start.Body.String(), "https://accounts.google.com/o/oauth2/auth?state=state123") {
		t.Fatalf("no Google link: %d %s", start.Code, start.Body)
	}
	var pending *http.Cookie
	for _, c := range start.Result().Cookies() {
		if c.Name == pendingCookie {
			pending = c
		}
	}
	if pending == nil {
		t.Fatal("no pending sign-in cookie")
	}
	if w := req(h, "GET", "/invites/google/poll", ip, nil, pending, ""); !strings.Contains(w.Body.String(), `"done":false`) {
		t.Fatalf("poll before sign-in: %s", w.Body)
	}
	if w := req(h, "GET", "/invites/google/poll", ip, nil, nil, ""); !strings.Contains(w.Body.String(), "expired") {
		t.Fatalf("poll without cookie: %s", w.Body)
	}

	api.mu.Lock()
	api.oidcUser = "tech1"
	api.mu.Unlock()
	done := req(h, "GET", "/invites/google/poll", ip, nil, pending, "")
	if !strings.Contains(done.Body.String(), `"done":true`) {
		t.Fatalf("poll after sign-in: %s", done.Body)
	}
	var session *http.Cookie
	for _, c := range done.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no session after Google sign-in")
	}
	token := createInviteFrom(t, h, session, ip, "Home PC")
	if w := req(h, "GET", "/i/"+token, "198.51.100.7", nil, nil, ""); w.Code != 200 {
		t.Fatalf("invite created via Google does not open: %d", w.Code)
	}
	if w := req(h, "GET", "/invites/google/poll", ip, nil, pending, ""); !strings.Contains(w.Body.String(), "expired") {
		t.Fatal("pending sign-in reusable after completion")
	}
}

func createInviteFrom(t *testing.T, h http.Handler, c *http.Cookie, ip, name string) string {
	t.Helper()
	w := req(h, "POST", "/invites/create", ip, url.Values{"name": {name}}, c, "")
	m := regexp.MustCompile(regexp.QuoteMeta(public) + `/i/([A-Za-z0-9_-]{43})`).FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("no link: %d %s", w.Code, w.Body)
	}
	return m[1]
}

func TestTrailingSlashRedirects(t *testing.T) {
	_, _, h := newTestServer(t)
	w := req(h, "GET", "/invites/", "203.0.113.9", nil, nil, "")
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/invites" {
		t.Fatalf("got %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestEnrollLinuxPlatform(t *testing.T) {
	_, api, h := newTestServer(t)
	c := login(t, h)
	token := createInvite(t, h, c, "Lab Linux", true)
	body := `{"mode":"setup","id":"123456780","password":"Abcdefghijkmnpqrstuvwx23","hostname":"lab1","platform":"Linux"}`
	if w := req(h, "POST", "/api/invite/"+token+"/enroll", "198.51.100.7", nil, nil, body); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if p := api.peers["123456780"]; p.Platform != "Linux" {
		t.Fatalf("platform %q", p.Platform)
	}
}

func TestHelpOnlyTechnician(t *testing.T) {
	s, api, h := newTestServer(t)
	s.SetupUsers = map[string]bool{"boss": true} // tech1 is not allowed to offer unattended access
	c := login(t, h)
	page := req(h, "GET", "/invites", "192.168.50.5", nil, c, "")
	if strings.Contains(page.Body.String(), `name="allow_setup"`) || !strings.Contains(page.Body.String(), "one-time help invites only") {
		t.Fatal("setup option shown to a help-only technician")
	}
	token := createInvite(t, h, c, "Visitor PC", true) // allow_setup=on is posted anyway
	if land := req(h, "GET", "/i/"+token, "198.51.100.7", nil, nil, ""); strings.Contains(land.Body.String(), "/helper/setup") {
		t.Fatal("landing page offers permanent access")
	}
	if w := req(h, "GET", "/i/"+token+"/helper/setup", "198.51.100.7", nil, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("setup helper served: %d", w.Code)
	}
	body := `{"mode":"setup","id":"222333444","password":"Abcdefghijkmnpqrstuvwx23"}`
	if w := req(h, "POST", "/api/invite/"+token+"/enroll", "198.51.100.7", nil, nil, body); w.Code != http.StatusForbidden {
		t.Fatalf("setup enrol accepted: %d %s", w.Code, w.Body)
	}
	if w := req(h, "POST", "/api/invite/"+token+"/enroll", "198.51.100.7", nil, nil, `{"mode":"help","id":"222333444"}`); w.Code != 200 {
		t.Fatalf("help enrol: %d %s", w.Code, w.Body)
	}
	if p := api.peers["222333444"]; p.Password != "" || p.Alias != "Invite: Visitor PC" {
		t.Fatalf("peer %+v", p)
	}
}

func TestLinuxCommandAndScript(t *testing.T) {
	s, _, h := newTestServer(t)
	c := login(t, h)
	created := req(h, "POST", "/invites/create", "192.168.50.5", url.Values{"name": {"Lab"}, "allow_setup": {"on"}}, c, "")
	m := regexp.MustCompile(regexp.QuoteMeta(public) + `/i/([A-Za-z0-9_-]{43})`).FindStringSubmatch(created.Body.String())
	if m == nil {
		t.Fatal("no link")
	}
	token := m[1]
	want := s.linuxCommand(token)
	if !strings.Contains(created.Body.String(), "Copy Linux command") || !strings.Contains(html.UnescapeString(created.Body.String()), want) {
		t.Fatal("Linux command missing on the technician page")
	}
	land := req(h, "GET", "/i/"+token, "198.51.100.7", nil, nil, "")
	if !strings.Contains(html.UnescapeString(land.Body.String()), want) {
		t.Fatal("Linux command missing on the landing page")
	}
	dl := req(h, "GET", "/i/"+token+"/linux", "198.51.100.7", nil, nil, "")
	if dl.Code != 200 || dl.Body.String() != "binary:"+FileLinux {
		t.Fatalf("script download: %d", dl.Code)
	}
	help := createInvite(t, h, c, "Visitor", false)
	if w := req(h, "GET", "/i/"+help+"/linux", "198.51.100.7", nil, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("script served for help-only invite: %d", w.Code)
	}
	if strings.Contains(req(h, "POST", "/invites/create", "192.168.50.5", url.Values{"name": {"V2"}}, c, "").Body.String(), "Copy Linux command") {
		t.Fatal("Linux command shown for a help-only invite")
	}
}

type fakeMail struct {
	to, subject, body string
	fail              error
}

func (m *fakeMail) Send(to, subject, body string) error {
	m.to, m.subject, m.body = to, subject, body
	return m.fail
}

func TestEmailInviteByComputerType(t *testing.T) {
	s, _, h := newTestServer(t)
	m := &fakeMail{}
	s.Mail = m
	c := login(t, h)
	send := func(f url.Values) string {
		f.Set("name", "Asha PC")
		return req(h, "POST", "/invites/create", "192.168.50.5", f, c, "").Body.String()
	}
	page := send(url.Values{"email": {"asha@example.org"}, "pc": {"windows"}, "allow_setup": {"on"}})
	if !strings.Contains(page, "Emailed to asha@example.org") || m.to != "asha@example.org" {
		t.Fatalf("not sent: %q", m.to)
	}
	if !strings.Contains(m.body, "ON A WINDOWS PC") || strings.Contains(m.body, "LINUX") || !strings.Contains(m.body, public+"/i/") {
		t.Fatalf("windows body:\n%s", m.body)
	}
	send(url.Values{"email": {"lab@example.org"}, "pc": {"linux"}, "allow_setup": {"on"}})
	if strings.Contains(m.body, "WINDOWS") || !strings.Contains(m.body, "sudo bash install-linux.sh") {
		t.Fatalf("linux body:\n%s", m.body)
	}
	send(url.Values{"email": {"x@example.org"}, "pc": {"unknown"}, "allow_setup": {"on"}})
	if !strings.Contains(m.body, "WINDOWS") || !strings.Contains(m.body, "LINUX") {
		t.Fatal("unknown should include both")
	}
	if p := send(url.Values{"email": {"a@b.org\r\nBcc: evil@x.org"}}); !strings.Contains(p, "not valid") {
		t.Fatal("bad address accepted")
	}
	if p := send(url.Values{"email": {"l@example.org"}, "pc": {"linux"}}); !strings.Contains(p, "Linux PCs need") {
		t.Fatal("linux help-only accepted")
	}
	m.fail = errors.New("smtp down")
	if p := send(url.Values{"email": {"y@example.org"}}); !strings.Contains(p, "could not be sent") || !strings.Contains(p, `id="link"`) {
		t.Fatal("send failure should still show the link")
	}
	s.Mail = nil
	if p := req(h, "GET", "/invites", "192.168.50.5", nil, c, "").Body.String(); strings.Contains(p, `name="email"`) {
		t.Fatal("email field shown without SMTP")
	}
}

func TestTechnicianDownloadsNeedSession(t *testing.T) {
	_, _, h := newTestServer(t)
	if w := req(h, "GET", "/invites/download/msi", "203.0.113.9", nil, nil, ""); w.Code != http.StatusSeeOther {
		t.Fatalf("download without session: %d", w.Code)
	}
	c := login(t, h)
	if !strings.Contains(req(h, "GET", "/invites", "192.168.50.5", nil, c, "").Body.String(), "/invites/download/msi") {
		t.Fatal("download link missing")
	}
	w := req(h, "GET", "/invites/download/msi", "192.168.50.5", nil, c, "")
	if w.Code != 200 || w.Body.String() != "binary:"+FileMSI || !strings.Contains(w.Header().Get("Content-Disposition"), FileMSI) {
		t.Fatalf("msi: %d %q", w.Code, w.Header().Get("Content-Disposition"))
	}
	if w := req(h, "GET", "/invites/download/portable", "192.168.50.5", nil, c, ""); w.Body.String() != "binary:"+FilePortable {
		t.Fatal("portable")
	}
}
