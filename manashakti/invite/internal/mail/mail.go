// Package mail sends plain-text invite emails over SMTP with STARTTLS (e.g. Google Workspace,
// smtp.gmail.com:587 with an app password). net/smtp refuses PLAIN auth without TLS.
package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type SMTP struct {
	Host, Port, User, Pass, From string
}

// ValidAddress returns the bare address if s is a single valid email address.
func ValidAddress(s string) (string, error) {
	if len(s) > 200 || strings.ContainsAny(s, "\r\n,;") {
		return "", errors.New("invalid email address")
	}
	a, err := mail.ParseAddress(s)
	if err != nil {
		return "", errors.New("invalid email address")
	}
	return a.Address, nil
}

// Build returns an RFC 5322 message; exported for tests.
func Build(from, to, subject, body string, now time.Time) ([]byte, error) {
	if strings.ContainsAny(from+to+subject, "\r\n") {
		return nil, errors.New("header contains a line break")
	}
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	domain := from[strings.LastIndex(from, "@")+1:]
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", mime.QEncoding.Encode("utf-8", "Manashakti IT Support")+" <"+from+">")
	fmt.Fprintf(&b, "To: <%s>\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), domain)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	w := quotedprintable.NewWriter(&b)
	if _, err := w.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (m *SMTP) Send(to, subject, body string) error {
	msg, err := Build(m.From, to, subject, body, time.Now())
	if err != nil {
		return err
	}
	auth := smtp.PlainAuth("", m.User, m.Pass, m.Host)
	return smtp.SendMail(net.JoinHostPort(m.Host, m.Port), auth, m.From, []string{to}, msg)
}
