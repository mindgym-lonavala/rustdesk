// invite-server issues one-time support invite links and enrols PCs that accept them
// into a rustdesk-api address book. It listens on localhost behind Caddy.
package main

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/mindgym-lonavala/rustdesk/manashakti/invite/internal/rdapi"
	"github.com/mindgym-lonavala/rustdesk/manashakti/invite/internal/store"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:21120", "listen address (keep on localhost; Caddy fronts it)")
	api := flag.String("api", "http://127.0.0.1:21114", "rustdesk-api base URL")
	public := flag.String("public-url", "https://support.manashakti.org:8443", "public base URL")
	book := flag.String("book", "Manashakti PCs", "address book to add PCs to")
	dir := flag.String("dir", ".", "base directory (data\\ and files\\ live here)")
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
