// Package homer is a client for the homer-app (Homer 7) REST API, /api/v3.
package homer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/napadailo/voip-tui/i18n"
)

// Client talks to a single homer-app instance.
type Client struct {
	BaseURL     string // e.g. https://homer.example.com
	APIPrefix   string // value of main_settings.apiprefix, usually empty
	Username    string
	Password    string
	AuthToken   string // value for the Auth-Token header (api_settings.enable_token_access)
	TokenHeader string

	HTTP *http.Client

	mu  sync.RWMutex
	jwt string
}

// Options configure a new client.
type Options struct {
	URL, APIPrefix, Username, Password, AuthToken, TokenHeader string
	Insecure                                                   bool
	Timeout                                                    time.Duration
}

// New creates a client.
func New(o Options) *Client {
	if o.Timeout == 0 {
		o.Timeout = 60 * time.Second
	}
	if o.TokenHeader == "" {
		o.TokenHeader = "Auth-Token"
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if o.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in via config
	}
	return &Client{
		BaseURL:     strings.TrimRight(o.URL, "/"),
		APIPrefix:   strings.TrimRight(o.APIPrefix, "/"),
		Username:    o.Username,
		Password:    o.Password,
		AuthToken:   o.AuthToken,
		TokenHeader: o.TokenHeader,
		HTTP:        &http.Client{Timeout: o.Timeout, Transport: tr, CheckRedirect: keepCredentials},
	}
}

// keepCredentials follows a redirect only on the host the client was given,
// and never from https to http: Go drops Authorization on another host but
// keeps the Auth-Token header, and a 307 to /auth carries the password on.
func keepCredentials(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New(i18n.T("stopped after 10 redirects"))
	}
	from := via[0].URL
	if req.URL.Host != from.Host || from.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf(i18n.T("redirect to %s refused: the credentials go to the configured host only"), req.URL.Redacted())
	}
	return nil
}

// maxBody is the most homer-tui reads of an answer: more is a server gone
// wrong, not a call.
const maxBody = 1 << 30

// APIError is a non-2xx answer from homer-app.
type APIError struct {
	Status  int
	Message string
	Body    string
	Hint    string // what the status likely means, for the user
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = truncate(e.Body, 200)
	}
	if e.Hint != "" {
		msg += "; " + e.Hint
	}
	return fmt.Sprintf("homer: HTTP %d: %s", e.Status, msg)
}

// IsUnauthorized reports whether err is a 401.
func IsUnauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized
}

func (c *Client) url(path string) string {
	return c.BaseURL + c.APIPrefix + "/api/v3" + path
}

// LoginResponse is the answer of POST /auth.
type LoginResponse struct {
	Token string `json:"token"`
	Scope string `json:"scope"`
	User  struct {
		Admin bool `json:"admin"`
	} `json:"user"`
}

// Login obtains a JWT. With only an Auth-Token configured it is a no-op.
func (c *Client) Login(ctx context.Context) (*LoginResponse, error) {
	if c.Username == "" {
		if c.AuthToken == "" {
			return nil, errors.New(i18n.T("no credentials: set username/password or token"))
		}
		return &LoginResponse{}, nil
	}
	body, _ := json.Marshal(map[string]string{"username": c.Username, "password": c.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/auth"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	data, err := c.send(req)
	if err != nil {
		return nil, err
	}
	var lr LoginResponse
	if err := json.Unmarshal(data, &lr); err != nil {
		return nil, fmt.Errorf("decode login: %w", err)
	}
	if lr.Token == "" {
		return nil, errors.New("homer: login returned empty token")
	}
	c.mu.Lock()
	c.jwt = lr.Token
	c.mu.Unlock()
	return &lr, nil
}

func (c *Client) send(req *http.Request) ([]byte, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBody {
		return nil, fmt.Errorf(i18n.T("the answer is over %d MB, homer-tui stops reading it"), maxBody>>20)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		ae := &APIError{Status: resp.StatusCode, Body: string(data[:min(len(data), 4096)])}
		var m struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(data, &m) == nil {
			ae.Message = strings.TrimSpace(strings.TrimSpace(m.Error + " " + m.Message))
		}
		return nil, ae
	}
	return data, nil
}

func (c *Client) authorize(req *http.Request) {
	c.mu.RLock()
	jwt := c.jwt
	c.mu.RUnlock()
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	} else if c.AuthToken != "" {
		req.Header.Set(c.TokenHeader, c.AuthToken)
	}
}

// Do performs an authorized request; body may be nil, []byte, io.Reader or any JSON-able value.
// On 401 it re-logins once (JWT expiry) and retries.
func (c *Client) Do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var payload []byte
	switch b := body.(type) {
	case nil:
	case []byte:
		payload = b
	case json.RawMessage:
		payload = b
	default:
		var err error
		if payload, err = json.Marshal(b); err != nil {
			return nil, err
		}
	}
	try := func() ([]byte, error) {
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.url(path), rd)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		c.authorize(req)
		return c.send(req)
	}
	data, err := try()
	if IsUnauthorized(err) && c.Username != "" {
		if _, lerr := c.Login(ctx); lerr == nil {
			return try()
		}
	}
	var ae *APIError
	if c.Username == "" && c.AuthToken != "" && IsUnauthorized(err) && errors.As(err, &ae) {
		ae.Hint = i18n.T("the server refused the token: it expired, was deleted or used up its call limit, or token access is off")
	}
	return data, err
}

// GetJSON GETs path and decodes into out.
func (c *Client) GetJSON(ctx context.Context, path string, out any) error {
	data, err := c.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// UploadFile sends a multipart form with field fileKey (used by PCAP import).
func (c *Client) UploadFile(ctx context.Context, path, field, file string) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile(field, filepath.Base(file))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(path), &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.authorize(req)
	return c.send(req)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
