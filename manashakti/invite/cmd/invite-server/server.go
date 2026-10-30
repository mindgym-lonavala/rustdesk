package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mindgym-lonavala/rustdesk/manashakti/invite/internal/rdapi"
	"github.com/mindgym-lonavala/rustdesk/manashakti/invite/internal/store"
)

const (
	sessionCookie = "ms_invite_session"
	sessionTTL    = 8 * time.Hour
	inviteTTL     = 24 * time.Hour
	inviteTag     = "Invite"

	FilePortable = "ManashaktiSupport.exe"
	FileMSI      = "ManashaktiSupport.msi"
	FileHelper   = "invite-helper.exe"
)

var (
	reToken    = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	rePeerID   = regexp.MustCompile(`^\d{6,12}$`)
	rePassword = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)
	reTag      = regexp.MustCompile(`^[A-Za-z0-9 _-]{1,24}$`)
)

type API interface {
	Login(ctx context.Context, username, password string) (string, string, error)
	BookGUID(ctx context.Context, token, name string) (string, error)
	UpsertPeer(ctx context.Context, token, guid string, p rdapi.Peer) error
	OidcBegin(ctx context.Context, op, id, uuid string) (string, string, error)
	OidcQuery(ctx context.Context, code, id, uuid string) (string, string, error)
}

type session struct {
	user, name, apiToken string
	expires              time.Time
}

type Server struct {
	Store     *store.Store
	API       API
	PublicURL string // https://support.manashakti.org:8443
	Book      string // address book name
	FilesDir  string
	// GoogleOp is the rustdesk-api OAuth provider name for "Continue with Google";
	// empty disables it. Password sign-in is always office-only.
	GoogleOp string

	mu       sync.Mutex
	sessions map[string]session
	pending  map[string]pendingSignIn
	hits     map[string][]time.Time
	hashes   map[string]fileHash
}

type pendingSignIn struct {
	code, id, uuid, url string
	expires             time.Time
}

const (
	pendingCookie = "ms_invite_signin"
	pendingTTL    = 5 * time.Minute
)

type fileHash struct {
	mod  time.Time
	size int64
	sum  string
}

func (s *Server) Handler() http.Handler {
	s.sessions = map[string]session{}
	s.pending = map[string]pendingSignIn{}
	s.hits = map[string][]time.Time{}
	s.hashes = map[string]fileHash{}

	mux := http.NewServeMux()
	// The page and session actions work from anywhere; a session comes either from a
	// password (office network only) or from Google sign-in via rustdesk-api.
	mux.HandleFunc("GET /invites", s.limited(60, s.techPage))
	mux.HandleFunc("GET /invites/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/invites", http.StatusMovedPermanently)
	})
	mux.HandleFunc("POST /invites/login", s.officeOnly(s.sameOrigin(s.limited(10, s.techLogin))))
	mux.HandleFunc("POST /invites/google", s.sameOrigin(s.limited(10, s.googleBegin)))
	mux.HandleFunc("GET /invites/google/poll", s.limited(60, s.googlePoll))
	mux.HandleFunc("POST /invites/logout", s.sameOrigin(s.techLogout))
	mux.HandleFunc("POST /invites/create", s.sameOrigin(s.techCreate))
	mux.HandleFunc("POST /invites/revoke", s.sameOrigin(s.techRevoke))

	mux.HandleFunc("GET /i/{token}", s.limited(30, s.landing))
	mux.HandleFunc("GET /i/{token}/helper/{mode}", s.limited(10, s.helperDownload))
	mux.HandleFunc("GET /api/invite/{token}", s.limited(30, s.inviteInfo))
	mux.HandleFunc("GET /api/invite/{token}/file/{kind}", s.limited(10, s.fileDownload))
	mux.HandleFunc("POST /api/invite/{token}/enroll", s.limited(10, s.enroll))
	return securityHeaders(mux)
}

// ---- middleware ----------------------------------------------------------------------

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		// same-origin, not no-referrer: with no-referrer browsers send "Origin: null" on form
		// POSTs, which sameOrigin would reject. Invite URLs still never leak cross-site.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src 'self'; form-action 'self'")
		h.ServeHTTP(w, r)
	})
}

// clientIP trusts X-Forwarded-For only from the local reverse proxy (Caddy).
func clientIP(r *http.Request) net.IP {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if p := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); p != nil {
				return p
			}
		}
	}
	return ip
}

func inOffice(r *http.Request) bool {
	ip := clientIP(r)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}

func (s *Server) officeOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !inOffice(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (s *Server) sameOrigin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && o != originOf(s.PublicURL) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func originOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return p.Scheme + "://" + p.Host
}

// limited allows perMinute requests per client IP on a route group.
func (s *Server) limited(perMinute int, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := fmt.Sprintf("%d|%s", perMinute, clientIP(r))
		now := time.Now()
		s.mu.Lock()
		var kept []time.Time
		for _, t := range s.hits[key] {
			if now.Sub(t) < time.Minute {
				kept = append(kept, t)
			}
		}
		over := len(kept) >= perMinute
		if !over {
			kept = append(kept, now)
		}
		s.hits[key] = kept
		s.mu.Unlock()
		if over {
			http.Error(w, "Too many requests, try again in a minute.", http.StatusTooManyRequests)
			return
		}
		h(w, r)
	}
}

// ---- technician pages ----------------------------------------------------------------

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) currentSession(r *http.Request) (session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	se, ok := s.sessions[c.Value]
	if !ok || time.Now().After(se.expires) {
		delete(s.sessions, c.Value)
		return session{}, false
	}
	return se, true
}

type techView struct {
	User, Name, Error, NewLink, NewName string
	Office, Google                      bool
	GoogleURL                           string
	Book                                string
	Invites                             []inviteRow
}

type inviteRow struct {
	Key, Name, Status, Expires string
	AllowSetup, Active         bool
}

func (s *Server) techPage(w http.ResponseWriter, r *http.Request) {
	s.renderTech(w, r, techView{})
}

func (s *Server) renderTech(w http.ResponseWriter, r *http.Request, v techView) {
	se, ok := s.currentSession(r)
	v.Book = s.Book
	v.Office, v.Google = inOffice(r), s.GoogleOp != ""
	if ok {
		v.User, v.Name = se.user, se.name
		now := time.Now()
		for _, inv := range s.Store.ListBy(se.user) {
			row := inviteRow{Key: inv.Key, Name: inv.Name, AllowSetup: inv.AllowSetup, Expires: inv.ExpiresAt.Local().Format("02 Jan 15:04")}
			switch {
			case inv.Revoked:
				row.Status = "Revoked"
			case inv.SetupAt != nil:
				row.Status = "Set up (ID " + inv.SetupPeer + ")"
			case now.After(inv.ExpiresAt):
				row.Status = "Expired"
			case inv.HelpPeerID != "":
				row.Status, row.Active = "Waiting for you (ID "+inv.HelpPeerID+")", true
			default:
				row.Status, row.Active = "Not opened yet", true
			}
			v.Invites = append(v.Invites, row)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := techTmpl.Execute(w, v); err != nil {
		log.Printf("render tech: %v", err)
	}
}

func (s *Server) techLogin(w http.ResponseWriter, r *http.Request) {
	user := strings.TrimSpace(r.FormValue("username"))
	pass := r.FormValue("password")
	if user == "" || pass == "" {
		s.renderTech(w, r, techView{Error: "Enter username and password."})
		return
	}
	token, name, err := s.API.Login(r.Context(), user, pass)
	if err != nil {
		msg := "Sign-in failed. Check username and password."
		if !errors.Is(err, rdapi.ErrBadLogin) {
			log.Printf("login %q: %v", user, err)
			msg = "Sign-in failed: account server not reachable."
		}
		s.renderTech(w, r, techView{Error: msg})
		return
	}
	if _, err := s.API.BookGUID(r.Context(), token, s.Book); err != nil {
		s.renderTech(w, r, techView{Error: fmt.Sprintf("Signed in, but: %v. Create it in the web admin first.", err)})
		return
	}
	id := randomID()
	s.mu.Lock()
	s.sessions[id] = session{user: user, name: name, apiToken: token, expires: time.Now().Add(sessionTTL)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/invites", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	http.Redirect(w, r, "/invites", http.StatusSeeOther)
}

func (s *Server) techLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/invites", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/invites", http.StatusSeeOther)
}

func parseTags(s string) ([]string, error) {
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if !reTag.MatchString(t) {
			return nil, fmt.Errorf("tag %q: use letters, digits, space, - or _ (max 24)", t)
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Server) techCreate(w http.ResponseWriter, r *http.Request) {
	se, ok := s.currentSession(r)
	if !ok {
		http.Redirect(w, r, "/invites", http.StatusSeeOther)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 60 || strings.ContainsAny(name, "<>\"\r\n") {
		s.renderTech(w, r, techView{Error: "Enter a name (max 60 characters)."})
		return
	}
	tags, err := parseTags(r.FormValue("tags"))
	if err != nil {
		s.renderTech(w, r, techView{Error: err.Error()})
		return
	}
	allowSetup := r.FormValue("allow_setup") == "on"
	token, _, err := s.Store.Create(name, se.user, se.apiToken, allowSetup, tags, inviteTTL)
	if err != nil {
		log.Printf("create invite: %v", err)
		s.renderTech(w, r, techView{Error: "Could not save the invite."})
		return
	}
	log.Printf("invite created by %s for %q (setup=%v)", se.user, name, allowSetup)
	s.renderTech(w, r, techView{NewLink: s.PublicURL + "/i/" + token, NewName: name})
}

func (s *Server) techRevoke(w http.ResponseWriter, r *http.Request) {
	se, ok := s.currentSession(r)
	if !ok {
		http.Redirect(w, r, "/invites", http.StatusSeeOther)
		return
	}
	if err := s.Store.Revoke(r.FormValue("key"), se.user); err != nil {
		s.renderTech(w, r, techView{Error: "Invite not found."})
		return
	}
	http.Redirect(w, r, "/invites", http.StatusSeeOther)
}

// ---- public: landing page and helper --------------------------------------------------

func (s *Server) lookup(w http.ResponseWriter, r *http.Request) (string, store.Invite, bool) {
	token := r.PathValue("token")
	if !reToken.MatchString(token) {
		http.Error(w, "This invite link is not valid.", http.StatusNotFound)
		return "", store.Invite{}, false
	}
	inv, err := s.Store.Get(token)
	if err != nil {
		msg := "This invite link is not valid."
		switch {
		case errors.Is(err, store.ErrExpired):
			msg = "This invite link has expired. Ask for a new one."
		case errors.Is(err, store.ErrRevoked):
			msg = "This invite link was cancelled."
		}
		http.Error(w, msg, http.StatusGone)
		return "", store.Invite{}, false
	}
	return token, inv, true
}

func (s *Server) landing(w http.ResponseWriter, r *http.Request) {
	token, inv, ok := s.lookup(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err := landingTmpl.Execute(w, map[string]any{
		"Name": inv.Name, "By": inv.CreatedBy, "Token": token,
		"AllowSetup": inv.AllowSetup && inv.SetupAt == nil,
		"Expires":    inv.ExpiresAt.Local().Format("02 Jan 2006, 15:04"),
	})
	if err != nil {
		log.Printf("render landing: %v", err)
	}
}

func (s *Server) helperDownload(w http.ResponseWriter, r *http.Request) {
	token, inv, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var label string
	switch r.PathValue("mode") {
	case "help":
		label = "Help"
	case "setup":
		if !inv.AllowSetup || inv.SetupAt != nil {
			http.Error(w, "Permanent access is not offered by this invite.", http.StatusForbidden)
			return
		}
		label = "Setup"
	default:
		http.NotFound(w, r)
		return
	}
	// The helper reads the mode and invite token from its own file name.
	s.serveFile(w, r, FileHelper, fmt.Sprintf("ManashaktiSupport-%s-%s.exe", label, token))
}

func (s *Server) fileSHA256(name string) (string, error) {
	p := filepath.Join(s.FilesDir, name)
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	h, ok := s.hashes[name]
	s.mu.Unlock()
	if ok && h.mod.Equal(st.ModTime()) && h.size == st.Size() {
		return h.sum, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	hexsum := hex.EncodeToString(sum.Sum(nil))
	s.mu.Lock()
	s.hashes[name] = fileHash{mod: st.ModTime(), size: st.Size(), sum: hexsum}
	s.mu.Unlock()
	return hexsum, nil
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, name, downloadAs string) {
	f, err := os.Open(filepath.Join(s.FilesDir, name))
	if err != nil {
		log.Printf("serve %s: %v", name, err)
		http.Error(w, "File not available on the server.", http.StatusServiceUnavailable)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "File not available on the server.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, downloadAs))
	http.ServeContent(w, r, downloadAs, st.ModTime(), f)
}

// ---- public: helper API ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) inviteInfo(w http.ResponseWriter, r *http.Request) {
	_, inv, ok := s.lookup(w, r)
	if !ok {
		return
	}
	files := map[string]string{}
	for kind, name := range map[string]string{"portable": FilePortable, "msi": FileMSI} {
		if sum, err := s.fileSHA256(name); err == nil {
			files[kind] = sum
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": inv.Name, "technician": inv.CreatedBy,
		"allow_setup": inv.AllowSetup && inv.SetupAt == nil,
		"expires_at":  inv.ExpiresAt, "sha256": files,
	})
}

func (s *Server) fileDownload(w http.ResponseWriter, r *http.Request) {
	_, inv, ok := s.lookup(w, r)
	if !ok {
		return
	}
	switch r.PathValue("kind") {
	case "portable":
		s.serveFile(w, r, FilePortable, FilePortable)
	case "msi":
		if !inv.AllowSetup || inv.SetupAt != nil {
			http.Error(w, "Not offered by this invite.", http.StatusForbidden)
			return
		}
		s.serveFile(w, r, FileMSI, FileMSI)
	default:
		http.NotFound(w, r)
	}
}

type enrollRequest struct {
	Mode     string `json:"mode"`
	ID       string `json:"id"`
	Password string `json:"password"`
	Hostname string `json:"hostname"`
	Username string `json:"username"`
}

func cleanLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>"'\`, r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	token, inv, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var req enrollRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if !rePeerID.MatchString(req.ID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device ID"})
		return
	}
	host, user := cleanLabel(req.Hostname), cleanLabel(req.Username)

	peer := rdapi.Peer{ID: req.ID, Hostname: host, Username: user, Platform: "Windows"}
	switch req.Mode {
	case "help":
		peer.Alias = "Invite: " + inv.Name
		peer.Tags = []string{inviteTag}
	case "setup":
		if !inv.AllowSetup {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "permanent access not offered by this invite"})
			return
		}
		if !rePassword.MatchString(req.Password) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid password"})
			return
		}
		// Claim the invite before writing the address book, so it cannot be used twice.
		now := time.Now()
		err := s.Store.Update(token, func(i *store.Invite) error {
			if i.SetupAt != nil {
				return store.ErrUsed
			}
			i.SetupAt, i.SetupPeer = &now, req.ID
			return nil
		})
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "this invite was already used for permanent access"})
			return
		}
		peer.Alias, peer.Password, peer.Tags = inv.Name, req.Password, inv.Tags
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid mode"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	err := s.writePeer(ctx, inv.APIToken, peer)
	if err != nil {
		log.Printf("enroll %s for invite %q: %v", req.Mode, inv.Name, err)
		if req.Mode == "setup" {
			_ = s.Store.Update(token, func(i *store.Invite) error { i.SetupAt, i.SetupPeer = nil, ""; return nil })
		}
		msg := "could not add this PC to the technician's list"
		var ae *rdapi.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			msg = "the technician's session has expired; ask for a new invite"
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": msg})
		return
	}
	if req.Mode == "help" {
		_ = s.Store.Update(token, func(i *store.Invite) error { i.HelpPeerID = req.ID; return nil })
	}
	log.Printf("enrolled %s mode=%s id=%s host=%q invite=%q by=%s", clientIP(r), req.Mode, req.ID, host, inv.Name, inv.CreatedBy)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) writePeer(ctx context.Context, apiToken string, p rdapi.Peer) error {
	guid, err := s.API.BookGUID(ctx, apiToken, s.Book)
	if err != nil {
		return err
	}
	return s.API.UpsertPeer(ctx, apiToken, guid, p)
}

// ---- Google sign-in (through rustdesk-api OIDC) --------------------------------------------

// googleBegin starts a sign-in and shows a page that opens Google in a new tab and polls
// until rustdesk-api reports the result. Only Google accounts bound to a rustdesk-api user
// (auto-register off) can complete it.
func (s *Server) googleBegin(w http.ResponseWriter, r *http.Request) {
	if s.GoogleOp == "" {
		http.NotFound(w, r)
		return
	}
	id, uuid := randomID()[:16], randomID()
	code, authURL, err := s.API.OidcBegin(r.Context(), s.GoogleOp, id, uuid)
	if err != nil {
		log.Printf("google sign-in start: %v", err)
		s.renderTech(w, r, techView{Error: "Google sign-in is not available right now."})
		return
	}
	if u, perr := url.Parse(authURL); perr != nil || u.Scheme != "https" {
		log.Printf("google sign-in start: unexpected URL %q", authURL)
		s.renderTech(w, r, techView{Error: "Google sign-in is not available right now."})
		return
	}
	pid := randomID()
	s.mu.Lock()
	for k, p := range s.pending {
		if time.Now().After(p.expires) {
			delete(s.pending, k)
		}
	}
	s.pending[pid] = pendingSignIn{code: code, id: id, uuid: uuid, url: authURL, expires: time.Now().Add(pendingTTL)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: pendingCookie, Value: pid, Path: "/invites", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(pendingTTL.Seconds())})
	s.renderTech(w, r, techView{GoogleURL: authURL})
}

func (s *Server) googlePoll(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(pendingCookie)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": "Sign-in expired. Start again."})
		return
	}
	s.mu.Lock()
	p, ok := s.pending[c.Value]
	s.mu.Unlock()
	if !ok || time.Now().After(p.expires) {
		writeJSON(w, http.StatusOK, map[string]any{"error": "Sign-in expired. Start again."})
		return
	}
	token, user, err := s.API.OidcQuery(r.Context(), p.code, p.id, p.uuid)
	if errors.Is(err, rdapi.ErrOidcPending) {
		writeJSON(w, http.StatusOK, map[string]any{"done": false})
		return
	}
	if err != nil || user == "" {
		log.Printf("google sign-in result: %v", err)
		s.mu.Lock()
		delete(s.pending, c.Value)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"error": "Google sign-in failed. Is this Google account linked to your technician account?"})
		return
	}
	if _, err := s.API.BookGUID(r.Context(), token, s.Book); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": fmt.Sprintf("Signed in, but: %v.", err)})
		return
	}
	sid := randomID()
	s.mu.Lock()
	delete(s.pending, c.Value)
	s.sessions[sid] = session{user: user, name: user, apiToken: token, expires: time.Now().Add(sessionTTL)}
	s.mu.Unlock()
	log.Printf("google sign-in: %s from %s", user, clientIP(r))
	http.SetCookie(w, &http.Cookie{Name: pendingCookie, Value: "", Path: "/invites", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sid, Path: "/invites", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	writeJSON(w, http.StatusOK, map[string]any{"done": true})
}
