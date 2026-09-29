package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Signing in takes two calls, so an agent that can't sit and wait can do it:
// the first starts a sign-in, prints its link and returns; once the person has
// pressed Allow, the second finishes it. The started sign-in is kept next to
// the credentials until then. --wait is the one-call form for a person at a
// terminal: it prints the link and waits for Allow.

// loginWait is how long a second `login` waits for the person to press Allow
// before it says it is still waiting.
var loginWait = 60 * time.Second

// pollUnit is one second of the server's poll interval; tests shorten it.
var pollUnit = time.Second

// pendingLogin is a sign-in started and not finished yet.
type pendingLogin struct {
	DeviceCode string  `json:"device_code"`
	UserCode   string  `json:"user_code"`
	Link       string  `json:"link"`
	Interval   int     `json:"interval"`
	ExpiresAt  float64 `json:"expires_at"`          // unix seconds
	PolledAt   float64 `json:"polled_at,omitempty"` // unix seconds, when it was last asked about
	Access     string  `json:"access"`
}

func (p *pendingLogin) expires() time.Time { return time.Unix(int64(p.ExpiresAt), 0) }

// pendingPath is next to the credentials: credentials.json keeps its
// started sign-ins in credentials.pending.json.
func pendingPath() string {
	p := credsPath()
	ext := filepath.Ext(p)
	return strings.TrimSuffix(p, ext) + ".pending" + ext
}

func loadPending() *pendingLogin {
	b, err := os.ReadFile(pendingPath())
	if err != nil {
		return nil
	}
	var f map[string]*pendingLogin
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	return f[apiBase()]
}

// savePending keeps p for this LiveLLM, or forgets it when p is nil.
func savePending(p *pendingLogin) error {
	path := pendingPath()
	f := map[string]*pendingLogin{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	if p == nil {
		delete(f, apiBase())
	} else {
		f[apiBase()] = p
	}
	if len(f) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	// The device code is a credential until it is used.
	return os.WriteFile(path, b, 0o600)
}

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	access := fs.String("access", "full", "how far this sign-in reaches: use, create or full")
	wait := fs.Bool("wait", false, "wait here until the link is allowed, instead of running login again")
	_ = fs.Parse(args)

	p := loadPending()
	if p != nil && (float64(time.Now().Unix()) >= p.ExpiresAt || p.Access != *access) {
		p = nil
	}
	fresh := p == nil
	if fresh {
		var err error
		if p, err = startLogin(*access); err != nil {
			return err
		}
	}

	if *wait {
		for {
			fmt.Fprintf(os.Stderr, "Open this and press Allow:\n\n  %s\n\n(code %s)\nWaiting…\n", p.Link, p.UserCode)
			c, gone, err := pollLogin(p, p.expires())
			switch {
			case err != nil:
				return err
			case gone != "" && !fresh:
				// an earlier run's link that can't be finished: start again
				if p, err = startLogin(*access); err != nil {
					return err
				}
				fresh = true
				continue
			case gone != "":
				_ = savePending(nil)
				return fmt.Errorf("%s — run livellm login again", gone)
			case c == nil:
				_ = savePending(nil)
				return fmt.Errorf("nobody allowed it in time — run livellm login again")
			}
			return finishLogin(c)
		}
	}

	if fresh {
		return print(linkAnswer(p, ""))
	}
	until := time.Now().Add(loginWait)
	if exp := p.expires(); exp.Before(until) {
		until = exp
	}
	c, gone, err := pollLogin(p, until)
	switch {
	case err != nil:
		return err
	case gone != "":
		// That link can't be finished any more: start again.
		if p, err = startLogin(*access); err != nil {
			return err
		}
		return print(linkAnswer(p, "the last link can't be used any more ("+gone+"); this is a new one"))
	case c == nil:
		return &problem{Status: 409, Msg: "still waiting — run livellm login again once the link is allowed",
			Next: "the link: " + p.Link}
	}
	return finishLogin(c)
}

// startLogin asks for a new sign-in and keeps it as the pending one.
func startLogin(access string) (*pendingLogin, error) {
	var start struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		Verify     string `json:"verification_uri_complete"`
		Interval   int    `json:"interval"`
		ExpiresIn  int    `json:"expires_in"`
	}
	if err := form("/v1/oauth/device/code", url.Values{
		"client_name": {"livellm on " + hostname()}, "scope": {access},
	}, &start); err != nil {
		return nil, err
	}
	if start.ExpiresIn <= 0 {
		start.ExpiresIn = 600
	}
	p := &pendingLogin{
		DeviceCode: start.DeviceCode, UserCode: start.UserCode, Link: start.Verify,
		Interval: start.Interval, ExpiresAt: float64(time.Now().Add(time.Duration(start.ExpiresIn) * time.Second).Unix()),
		Access: access,
	}
	if err := savePending(p); err != nil {
		return nil, err
	}
	return p, nil
}

func linkAnswer(p *pendingLogin, note string) map[string]any {
	out := map[string]any{
		"signedIn":  false,
		"link":      p.Link,
		"code":      p.UserCode,
		"expiresAt": p.expires().UTC().Format(time.RFC3339),
		"next":      "open the link and press Allow, then run: livellm login",
	}
	if note != "" {
		out["note"] = note
	}
	return out
}

// pollLogin asks about the pending sign-in until it is allowed or until runs
// out. It answers the new sign-in when it was allowed; gone, the server's
// reason, when the link can't be finished any more (it expired, was denied or
// was already used); neither when it is still waiting.
func pollLogin(p *pendingLogin, until time.Time) (*creds, string, error) {
	if p.Interval < 1 {
		p.Interval = 5
	}
	// The server asks for a pause between polls, the last run's included.
	next := time.Now()
	if p.PolledAt > 0 {
		next = time.UnixMilli(int64(p.PolledAt * 1000)).Add(time.Duration(p.Interval) * pollUnit)
	}
	for {
		if d := time.Until(next); d > 0 {
			if time.Now().Add(d).After(until) {
				return nil, "", nil
			}
			time.Sleep(d)
		}
		var out tokenAnswer
		err := form("/v1/oauth/token", url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {p.DeviceCode},
		}, &out)
		p.PolledAt = float64(time.Now().UnixMilli()) / 1000
		if err == nil {
			return out.creds(), "", nil
		}
		var pr *problem
		if !asProblem(err, &pr) {
			return nil, "", err
		}
		switch pr.Code {
		case "authorization_pending":
		case "slow_down":
			p.Interval += 5
		case "expired_token", "access_denied", "invalid_grant":
			return nil, pr.Msg, nil
		default:
			return nil, "", err
		}
		_ = savePending(p)
		next = time.Now().Add(time.Duration(p.Interval) * pollUnit)
	}
}

func finishLogin(c *creds) error {
	if err := saveCreds(c); err != nil {
		return err
	}
	_ = savePending(nil)
	return print(map[string]any{"signedIn": true, "workspace": c.Workspace, "access": c.Access})
}
