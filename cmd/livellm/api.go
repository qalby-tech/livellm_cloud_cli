package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Talking to LiveLLM: the public API, with either a workspace key or the
// sign-in this command saved. Paths never name the workspace — the credential
// says which one it is.

const defaultAPI = "https://api.live-llm.com"

func apiBase() string {
	if v := strings.TrimRight(os.Getenv("LIVELLM_API_URL"), "/"); v != "" {
		return v
	}
	return defaultAPI
}

// problem is an answer the platform refused, kept whole so the message it
// wrote reaches the person unchanged.
type problem struct {
	Status int
	Msg    string
	Next   string
	// Code is the OAuth error code of a refusal from the sign-in endpoints.
	Code string
	// Missing are the body paths a create from a template still needs (the
	// secrets the template left out).
	Missing []string
}

func (p *problem) Error() string {
	if p.Next != "" {
		return fmt.Sprintf("%s (%d) — %s", p.Msg, p.Status, p.Next)
	}
	return fmt.Sprintf("%s (%d)", p.Msg, p.Status)
}

func asProblem(err error, out **problem) bool { return errors.As(err, out) }

var client = &http.Client{Timeout: 60 * time.Second}

// longClient waits as long as the platform says a long call may take (it
// answers by then); streamClient has no limit at all, for a browser profile
// on its way to or from disk.
func longClient(d time.Duration) *http.Client { return &http.Client{Timeout: d} }

var streamClient = &http.Client{}

// call makes one request with whatever credential is to hand, and decodes
// a JSON answer into out.
func call(method, path string, body any, out any) error {
	raw, _, err := send(method, path, body)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("LiveLLM answered something unexpected: %w", err)
	}
	return nil
}

// send makes one request and hands back the answer as it came: a file (a
// screen's picture, a Remote Desktop file) is not JSON.
func send(method, path string, body any) ([]byte, http.Header, error) {
	return sendWith(client, method, path, body)
}

// sendWith is send with a client of the caller's: a call the platform takes
// minutes to answer (a proxy rotation, a snapshot) gets a longer limit.
func sendWith(c *http.Client, method, path string, body any) ([]byte, http.Header, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	res, err := do(c, method, path, rdr, -1, "application/json", nil)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	return raw, res.Header, nil
}

// do makes one request with whatever credential is to hand. size is the
// body's length when it is known (-1 otherwise). An answer of 400 or more is
// the platform's refusal, as a problem; any other answer comes back open,
// for the caller to read and close.
func do(c *http.Client, method, path string, body io.Reader, size int64, contentType string, headers map[string]string) (*http.Response, error) {
	tok, kind, err := credential()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, apiBase()+path, body)
	if err != nil {
		return nil, err
	}
	if size >= 0 && body != nil {
		req.ContentLength = size
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if kind == "key" {
		req.Header.Set("x-api-key", tok)
	} else {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach LiveLLM: %w", err)
	}
	if res.StatusCode >= 400 {
		defer res.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		p := problemFrom(res.StatusCode, raw)
		if res.StatusCode == 401 && kind == "signin" {
			p.Next = "run: livellm login"
		}
		return nil, p
	}
	return res, nil
}

// problemFrom reads a refusal: the platform's own words when it wrote JSON,
// the text as it came otherwise.
func problemFrom(status int, raw []byte) *problem {
	p := &problem{Status: status, Msg: strings.TrimSpace(string(raw))}
	var e struct {
		Error   string   `json:"error"`
		Message string   `json:"message"`
		Code    string   `json:"code"`
		Next    string   `json:"next"`
		Missing []string `json:"missing"`
	}
	if json.Unmarshal(raw, &e) == nil {
		msg := e.Error
		if msg == "" {
			msg = e.Message
		}
		if msg == "" {
			msg = e.Code
		}
		if msg != "" {
			p.Msg, p.Next, p.Missing = msg, e.Next, e.Missing
		}
		p.Code = e.Code
	}
	return p
}

// credential is the API key if one is set, otherwise the saved sign-in,
// renewed when it has aged out.
func credential() (tok, kind string, err error) {
	if k := strings.TrimSpace(os.Getenv("LIVELLM_API_KEY")); k != "" {
		return k, "key", nil
	}
	c, err := loadCreds()
	if err != nil || c == nil {
		return "", "", &problem{Status: 401, Msg: "not signed in", Next: "run: livellm login"}
	}
	if time.Now().After(c.AccessExpires.Add(-time.Minute)) {
		if err := refresh(c); err != nil {
			return "", "", err
		}
	}
	return c.AccessToken, "signin", nil
}

// --- the saved sign-in ---------------------------------------------------------

type creds struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	AccessExpires time.Time `json:"access_expires"`
	Workspace     string    `json:"workspace"`
	Access        string    `json:"access"`
}

func credsPath() string {
	if v := os.Getenv("LIVELLM_CREDENTIALS"); v != "" {
		return v
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "livellm", "credentials.json")
}

// byAPI keys the file by which LiveLLM it is, so a self-hosted one and the
// cloud don't overwrite each other.
type credsFile map[string]*creds

func loadCreds() (*creds, error) {
	b, err := os.ReadFile(credsPath())
	if err != nil {
		return nil, nil
	}
	var f credsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, nil
	}
	return f[apiBase()], nil
}

func saveCreds(c *creds) error {
	path := credsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f := credsFile{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	if c == nil {
		delete(f, apiBase())
	} else {
		f[apiBase()] = c
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	// The sign-in is a credential: nobody else on this machine reads it.
	return os.WriteFile(path, b, 0o600)
}

// form posts to the token endpoint, which speaks form encoding like every
// OAuth server.
func form(path string, values url.Values, out any) error {
	res, err := client.Post(apiBase()+path, "application/x-www-form-urlencoded",
		strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("couldn't reach LiveLLM: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
			Desc  string `json:"error_description"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Desc
		if msg == "" {
			msg = e.Error
		}
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return &problem{Status: res.StatusCode, Msg: msg, Code: e.Error}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Workspace    string `json:"workspace"`
}

func (t tokenAnswer) creds() *creds {
	return &creds{
		AccessToken:   t.AccessToken,
		RefreshToken:  t.RefreshToken,
		AccessExpires: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second),
		Workspace:     t.Workspace,
		Access:        t.Scope,
	}
}

func refresh(c *creds) error {
	var out tokenAnswer
	err := form("/v1/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.RefreshToken},
	}, &out)
	if err != nil {
		return &problem{Status: 401, Msg: "this sign-in has ended", Next: "run: livellm login"}
	}
	fresh := out.creds()
	*c = *fresh
	return saveCreds(fresh)
}

// print writes JSON the way a person reads it and a pipe parses it.
func print(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}
