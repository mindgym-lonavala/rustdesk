// invite-server issues one-time support invite links and enrols PCs that accept them
// into a rustdesk-api address book. It listens on localhost behind Caddy.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mindgym-lonavala/rustdesk/manashakti/invite/internal/mail"
	"github.com/mindgym-lonavala/rustdesk/manashakti/invite/internal/rdapi"
	"github.com/mindgym-lonavala/rustdesk/manashakti/invite/internal/store"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:21120", "listen address (keep on localhost; Caddy fronts it)")
	api := flag.String("api", "http://127.0.0.1:21114", "rustdesk-api base URL")
	public := flag.String("public-url", "https://support.manashakti.org:8443", "public base URL")
	book := flag.String("book", "Manashakti PCs", "address book to add PCs to")
	dir := flag.String("dir", ".", "base directory (data\\ and files\\ live here)")
	smtpHost := flag.String("smtp-host", "", "SMTP server for emailing invites, e.g. smtp.gmail.com (empty disables email)")
	smtpPort := flag.String("smtp-port", "587", "SMTP port (STARTTLS)")
	smtpUser := flag.String("smtp-user", "", "SMTP username, e.g. developers@manashakti.org")
	smtpFrom := flag.String("smtp-from", "", "From address (default: smtp-user)")
	setupUsers := flag.String("setup-users", "", "comma-separated technician usernames allowed to offer unattended access; empty = all")
	googleOp := flag.String("google-op", "google", "rustdesk-api OAuth provider name (the IdP column in the web admin, case-sensitive); empty disables")
	flag.Parse()

	st, err := store.Open(filepath.Join(*dir, "data", "invites.json"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	s := &Server{
		Store:     st,
		API:       rdapi.New(*api),
		PublicURL: strings.TrimRight(*public, "/"),
		Book:      *book,
		FilesDir:  filepath.Join(*dir, "files"),
		GoogleOp:  *googleOp,
	}
	if *smtpHost != "" {
		// The password stays out of the service command line: <dir>\data\smtp-password.txt (Administrators only).
		pw, err := os.ReadFile(filepath.Join(*dir, "data", "smtp-password.txt"))
		if err != nil {
			log.Fatalf("smtp-host is set but data\\smtp-password.txt cannot be read: %v", err)
		}
		from := *smtpFrom
		if from == "" {
			from = *smtpUser
		}
		s.Mail = &mail.SMTP{Host: *smtpHost, Port: *smtpPort, User: *smtpUser, Pass: strings.TrimSpace(string(pw)), From: from}
		log.Printf("emailing invites via %s:%s as %s", *smtpHost, *smtpPort, from)
	}
	for _, u := range strings.Split(*setupUsers, ",") {
		if u = strings.TrimSpace(u); u != "" {
			if s.SetupUsers == nil {
				s.SetupUsers = map[string]bool{}
			}
			s.SetupUsers[u] = true
		}
	}
	srv := &http.Server{
		Addr:              *listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("invite-server listening on %s (public %s, book %q)", *listen, s.PublicURL, s.Book)
	log.Fatal(srv.ListenAndServe())
}
