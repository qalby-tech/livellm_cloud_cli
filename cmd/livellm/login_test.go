package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSignIn is the platform's sign-in endpoints: it hands out device codes
// and answers each poll the way answer says.
type fakeSignIn struct {
	mu      sync.Mutex
	started int
	polls   int
	// answer is what a poll of this device code gets: "pending", "allow",
	// "deny", "slow" or "expired".
	answer func(code string, poll int) string
}

func (f *fakeSignIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = r.ParseForm()
	w.Header().Set("Content-Type", "application/json")
	oauthErr := func(code, desc string) {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
	}
	switch r.URL.Path {
	case "/v1/oauth/device/code":
		f.started++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": fmt.Sprintf("dev-%d", f.started), "user_code": fmt.Sprintf("CODE-%d", f.started),
			"verification_uri_complete": fmt.Sprintf("https://console.test/device?code=CODE-%d", f.started),
			"interval":                  1, "expires_in": 600, "scope": r.Form.Get("scope"),
		})
	case "/v1/oauth/token":
		f.polls++
		switch f.answer(r.Form.Get("device_code"), f.polls) {
		case "allow":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "llt_a", "refresh_token": "r", "expires_in": 3600,
				"scope": "full", "workspace": "acme",
			})
		case "deny":
			oauthErr("access_denied", "the user denied the sign-in")
		case "slow":
			oauthErr("slow_down", "poll every 6 seconds")
		case "expired":
			oauthErr("expired_token", "the sign-in link expired; start again")
		default:
			oauthErr("authorization_pending", "waiting for the user to allow the sign-in")
		}
	default:
		w.WriteHeader(404)
	}
}

func (f *fakeSignIn) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.polls
}

// signInEnv points the CLI at a fake platform and a credentials file of its
// own, with polls a hundred times faster than the server's seconds.
func signInEnv(t *testing.T, answer func(code string, poll int) string) (*fakeSignIn, string) {
	t.Helper()
	f := &fakeSignIn{answer: answer}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "")
	t.Setenv("LIVELLM_CREDENTIALS", filepath.Join(dir, "credentials.json"))
	oldUnit, oldWait := pollUnit, loginWait
	pollUnit, loginWait = 10*time.Millisecond, time.Second
	t.Cleanup(func() { pollUnit, loginWait = oldUnit, oldWait })
	return f, dir
}

// stdout runs fn and answers what it printed.
func stdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	err := fn()
	os.Stdout = old
	_ = w.Close()
	b, _ := io.ReadAll(r)
	return string(b), err
}

func printed(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not JSON: %q", out)
	}
	return m
}

// The first login starts a sign-in, prints its link and returns without
// waiting; the started sign-in is kept, readable only by this user.
func TestLoginFirstCallPrintsTheLink(t *testing.T) {
	f, dir := signInEnv(t, func(string, int) string { return "pending" })
	start := time.Now()
	out, err := stdout(t, func() error { return cmdLogin(nil) })
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("the first call waited %s", time.Since(start))
	}
	m := printed(t, out)
	if m["signedIn"] != false || m["link"] != "https://console.test/device?code=CODE-1" || m["code"] != "CODE-1" || m["expiresAt"] == "" {
		t.Errorf("printed %v", m)
	}
	if started, polls := f.counts(); started != 1 || polls != 0 {
		t.Errorf("started %d sign-ins and polled %d times", started, polls)
	}
	path := filepath.Join(dir, "credentials.pending.json")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("pending sign-in is %v", st.Mode().Perm())
	}
	if p := loadPending(); p == nil || p.DeviceCode != "dev-1" || p.Access != "full" {
		t.Errorf("pending: %+v", p)
	}
}

// Once the person has allowed it, login again finishes the same sign-in.
func TestLoginAgainFinishesIt(t *testing.T) {
	var allowed atomic.Bool
	f, dir := signInEnv(t, func(code string, _ int) string {
		if allowed.Load() && code == "dev-1" {
			return "allow"
		}
		return "pending"
	})
	if _, err := stdout(t, func() error { return cmdLogin(nil) }); err != nil {
		t.Fatal(err)
	}
	allowed.Store(true)
	out, err := stdout(t, func() error { return cmdLogin(nil) })
	if err != nil {
		t.Fatal(err)
	}
	if m := printed(t, out); m["signedIn"] != true || m["workspace"] != "acme" {
		t.Errorf("printed %v", m)
	}
	if started, _ := f.counts(); started != 1 {
		t.Errorf("started %d sign-ins", started)
	}
	if c, _ := loadCreds(); c == nil || c.AccessToken != "llt_a" {
		t.Errorf("saved %+v", c)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.pending.json")); !os.IsNotExist(err) {
		t.Errorf("the finished sign-in is still pending: %v", err)
	}
}

// Before Allow, login again waits a bounded while and says it is still
// waiting; the same sign-in stays pending for the next try.
func TestLoginAgainStillWaiting(t *testing.T) {
	f, _ := signInEnv(t, func(string, int) string { return "pending" })
	loginWait = 100 * time.Millisecond
	if _, err := stdout(t, func() error { return cmdLogin(nil) }); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := stdout(t, func() error { return cmdLogin(nil) })
	var p *problem
	if !asProblem(err, &p) || !strings.Contains(p.Msg, "still waiting") || !strings.Contains(p.Next, "CODE-1") {
		t.Fatalf("answered %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("waited %s", time.Since(start))
	}
	started, polls := f.counts()
	if started != 1 || polls < 1 {
		t.Errorf("started %d, polled %d", started, polls)
	}
	if pend := loadPending(); pend == nil || pend.DeviceCode != "dev-1" || pend.PolledAt == 0 {
		t.Errorf("pending after waiting: %+v", pend)
	}
}

// A link that was denied or ran out is replaced by a new one.
func TestLoginStartsAgainWhenTheLinkIsGone(t *testing.T) {
	for _, answer := range []string{"deny", "expired"} {
		t.Run(answer, func(t *testing.T) {
			f, _ := signInEnv(t, func(code string, _ int) string {
				if code == "dev-1" {
					return answer
				}
				return "pending"
			})
			if _, err := stdout(t, func() error { return cmdLogin(nil) }); err != nil {
				t.Fatal(err)
			}
			out, err := stdout(t, func() error { return cmdLogin(nil) })
			if err != nil {
				t.Fatal(err)
			}
			m := printed(t, out)
			if m["link"] != "https://console.test/device?code=CODE-2" || m["note"] == nil {
				t.Errorf("printed %v", m)
			}
			if started, _ := f.counts(); started != 2 {
				t.Errorf("started %d sign-ins", started)
			}
			if p := loadPending(); p == nil || p.DeviceCode != "dev-2" {
				t.Errorf("pending: %+v", p)
			}
		})
	}
}

// A pending sign-in past its time, or for other access, isn't asked about:
// a new one starts.
func TestLoginStartsAgainWhenPendingIsStale(t *testing.T) {
	f, _ := signInEnv(t, func(string, int) string { return "pending" })
	if _, err := stdout(t, func() error { return cmdLogin(nil) }); err != nil {
		t.Fatal(err)
	}
	p := loadPending()
	p.ExpiresAt = float64(time.Now().Add(-time.Minute).Unix())
	if err := savePending(p); err != nil {
		t.Fatal(err)
	}
	out, _ := stdout(t, func() error { return cmdLogin(nil) })
	if m := printed(t, out); m["code"] != "CODE-2" {
		t.Errorf("after expiry printed %v", m)
	}
	out, _ = stdout(t, func() error { return cmdLogin([]string{"--access", "use"}) })
	if m := printed(t, out); m["code"] != "CODE-3" {
		t.Errorf("for other access printed %v", m)
	}
	if started, polls := f.counts(); started != 3 || polls != 0 {
		t.Errorf("started %d, polled %d", started, polls)
	}
}

// A sign-in started with other access than the default is finished by a
// plain login again, as the printed next step says, not replaced.
func TestLoginAgainKeepsThePendingAccess(t *testing.T) {
	var allowed atomic.Bool
	f, _ := signInEnv(t, func(code string, _ int) string {
		if allowed.Load() && code == "dev-1" {
			return "allow"
		}
		return "pending"
	})
	if _, err := stdout(t, func() error { return cmdLogin([]string{"--access", "use"}) }); err != nil {
		t.Fatal(err)
	}
	allowed.Store(true)
	out, err := stdout(t, func() error { return cmdLogin(nil) })
	if err != nil {
		t.Fatal(err)
	}
	if m := printed(t, out); m["signedIn"] != true {
		t.Errorf("printed %v", m)
	}
	if started, _ := f.counts(); started != 1 {
		t.Errorf("started %d sign-ins", started)
	}
}

// --wait is one call: it waits for Allow, slowing down when asked.
func TestLoginWait(t *testing.T) {
	f, _ := signInEnv(t, func(_ string, poll int) string {
		switch {
		case poll == 2:
			return "slow"
		case poll >= 4:
			return "allow"
		}
		return "pending"
	})
	out, err := stdout(t, func() error { return cmdLogin([]string{"--wait"}) })
	if err != nil {
		t.Fatal(err)
	}
	if m := printed(t, out); m["signedIn"] != true {
		t.Errorf("printed %v", m)
	}
	if started, polls := f.counts(); started != 1 || polls != 4 {
		t.Errorf("started %d, polled %d", started, polls)
	}
	if p := loadPending(); p != nil {
		t.Errorf("still pending: %+v", p)
	}
}

// A pending sign-in written by the skill (fractional times) is read too.
func TestPendingReadsFractionalTimes(t *testing.T) {
	signInEnv(t, func(string, int) string { return "pending" })
	raw := fmt.Sprintf(`{%q: {"device_code": "dev-9", "user_code": "C", "link": "L", "interval": 5, "expires_at": %f, "polled_at": %f, "access": "full"}}`,
		apiBase(), float64(time.Now().Add(time.Minute).Unix())+0.5, float64(time.Now().Unix())+0.25)
	if err := os.WriteFile(pendingPath(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := loadPending(); p == nil || p.DeviceCode != "dev-9" {
		t.Errorf("read %+v", p)
	}
}
