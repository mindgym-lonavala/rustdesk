// Package rdapi is a minimal client for the rustdesk-api endpoints the invite service needs:
// login and adding/updating peers in a shared address book.
package rdapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	Base string // e.g. http://127.0.0.1:21114
	HTTP *http.Client
}

func New(base string) *Client {
	return &Client{Base: base, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

var ErrBadLogin = errors.New("username or password incorrect")

type Peer struct {
	ID       string   `json:"id"`
	Alias    string   `json:"alias"`
	Password string   `json:"password,omitempty"`
	Tags     []string `json:"tags"`
	Hostname string   `json:"hostname,omitempty"`
	Username string   `json:"username,omitempty"`
	Platform string   `json:"platform,omitempty"`
}

func (c *Client) do(ctx context.Context, method, path, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("rustdesk-api %d: %s", e.Status, e.Message) }

// Login returns an access token and the user's display name.
func (c *Client) Login(ctx context.Context, username, password string) (token, name string, err error) {
	var res struct {
		AccessToken string `json:"access_token"`
		User        struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	err = c.do(ctx, http.MethodPost, "/api/login", "", map[string]any{
		"username": username, "password": password, "id": "", "uuid": "", "autoLogin": false, "type": "account",
	}, &res)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusBadRequest {
		return "", "", ErrBadLogin
	}
	if err != nil {
		return "", "", err
	}
	if res.AccessToken == "" {
		return "", "", errors.New("rustdesk-api returned no token")
	}
	if res.User.Name == "" {
		res.User.Name = username
	}
	return res.AccessToken, res.User.Name, nil
}

// BookGUID finds a writable address book by name.
func (c *Client) BookGUID(ctx context.Context, token, name string) (string, error) {
	var res struct {
		Data []struct {
			GUID string `json:"guid"`
			Name string `json:"name"`
			Rule int    `json:"rule"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/ab/shared/profiles?current=1&pageSize=100", token, map[string]any{}, &res); err != nil {
		return "", err
	}
	for _, b := range res.Data {
		if b.Name == name {
			if b.Rule < 2 {
				return "", fmt.Errorf("no write access to address book %q", name)
			}
			return b.GUID, nil
		}
	}
	return "", fmt.Errorf("address book %q not found", name)
}

// UpsertPeer adds the peer to the book, or updates alias/password/tags if it is already there.
func (c *Client) UpsertPeer(ctx context.Context, token, guid string, p Peer) error {
	if p.Tags == nil {
		p.Tags = []string{}
	}
	var res struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	q := "/api/ab/peers?current=1&pageSize=1000&ab=" + url.QueryEscape(guid)
	if err := c.do(ctx, http.MethodPost, q, token, map[string]any{}, &res); err != nil {
		return err
	}
	for _, e := range res.Data {
		if e.ID == p.ID {
			upd := map[string]any{"id": p.ID, "alias": p.Alias, "tags": p.Tags}
			if p.Password != "" {
				upd["password"] = p.Password
			}
			return c.do(ctx, http.MethodPut, "/api/ab/peer/update/"+url.PathEscape(guid), token, upd, nil)
		}
	}
	return c.do(ctx, http.MethodPost, "/api/ab/peer/add/"+url.PathEscape(guid), token, p, nil)
}

// OidcBegin starts a rustdesk-api OIDC sign-in for provider op (as configured in the web admin,
// e.g. "Google"). The returned URL is opened by the user; code is polled with OidcQuery.
func (c *Client) OidcBegin(ctx context.Context, op, id, uuid string) (code, authURL string, err error) {
	var res struct {
		Code string `json:"code"`
		URL  string `json:"url"`
	}
	err = c.do(ctx, http.MethodPost, "/api/oidc/auth", "", map[string]any{
		"op": op, "id": id, "uuid": uuid,
		"deviceInfo": map[string]string{"name": "invite-server", "os": "web", "type": "browser"},
	}, &res)
	if err != nil {
		return "", "", err
	}
	if res.Code == "" || res.URL == "" {
		return "", "", errors.New("rustdesk-api returned no sign-in URL")
	}
	return res.Code, res.URL, nil
}

// ErrOidcPending means the user has not finished signing in yet.
var ErrOidcPending = errors.New("sign-in not finished yet")

// OidcQuery returns the access token and username once the user has signed in.
func (c *Client) OidcQuery(ctx context.Context, code, id, uuid string) (token, name string, err error) {
	var res struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		User        struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	q := "/api/oidc/auth-query?code=" + url.QueryEscape(code) + "&id=" + url.QueryEscape(id) + "&uuid=" + url.QueryEscape(uuid)
	if err := c.do(ctx, http.MethodGet, q, "", nil, &res); err != nil {
		return "", "", err
	}
	if res.AccessToken == "" {
		return "", "", ErrOidcPending
	}
	return res.AccessToken, res.User.Name, nil
}
