package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A 429 is sent again after Retry-After (body and all), a few times; a wait
// past a minute, or a stream that can't be sent twice, is passed on at once.
func TestRetryOn429(t *testing.T) {
	var waits []time.Duration
	old := sleep
	sleep = func(d time.Duration) { waits = append(waits, d) }
	defer func() { sleep = old }()

	var calls int
	var bodies []string
	limit := 2
	retryAfter := "3"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if calls <= limit {
			w.Header().Set("Retry-After", retryAfter)
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":"Too many requests. Try again shortly.","code":"rate_limited"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")

	var out map[string]any
	if err := call("POST", "/v1/workloads", map[string]any{"id": "web"}, &out); err != nil {
		t.Fatalf("after two 429s: %v", err)
	}
	if calls != 3 || out["ok"] != true {
		t.Fatalf("%d calls, answer %v; want 3 and the third's", calls, out)
	}
	for _, b := range bodies {
		if b != `{"id":"web"}` {
			t.Fatalf("a retry sent body %q", b)
		}
	}
	if len(waits) != 2 || waits[0] < 3*time.Second || waits[0] > 3*time.Second+750*time.Millisecond {
		t.Fatalf("waits %v; want two of 3 s plus at most a quarter", waits)
	}

	// Past the tries: the refusal, in the API's words.
	calls, waits, limit = 0, nil, 100
	err := call("GET", "/v1/status", nil, nil)
	var p *problem
	if !asProblem(err, &p) || p.Status != 429 || p.Code != "rate_limited" || calls != retryTries+1 {
		t.Fatalf("past the tries: %v after %d calls; want the 429 after %d", err, calls, retryTries+1)
	}

	// A wait of minutes (a sign-in counter) is passed on at once.
	calls, waits, retryAfter = 0, nil, "900"
	if err := call("GET", "/v1/status", nil, nil); !asProblem(err, &p) || p.Status != 429 || calls != 1 || len(waits) != 0 {
		t.Fatalf("Retry-After 900: %v, %d calls, waits %v; want the 429 at once", err, calls, waits)
	}

	// Another 429 (a counter: billing_rate, change_ip_too_soon) is passed on
	// at once.
	calls, waits, retryAfter = 0, nil, "60"
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"Too many tries. Wait a few minutes.","code":"billing_rate"}`))
	}))
	defer other.Close()
	t.Setenv("LIVELLM_API_URL", other.URL)
	if err := call("POST", "/v1/billing", nil, nil); !asProblem(err, &p) || p.Code != "billing_rate" || calls != 1 || len(waits) != 0 {
		t.Fatalf("billing_rate: %v after %d calls, waits %v; want it at once", err, calls, waits)
	}
	t.Setenv("LIVELLM_API_URL", srv.URL)

	// A stream can't be sent twice.
	calls, waits, retryAfter = 0, nil, "1"
	if _, err := do(client, "PUT", "/v1/upload", io.NopCloser(strings.NewReader("data")), 4, "application/octet-stream", nil); !asProblem(err, &p) || calls != 1 {
		t.Fatalf("a stream: %v after %d calls; want the 429 at once", err, calls)
	}
}

func TestRetryWait(t *testing.T) {
	for _, c := range []struct {
		h        string
		try      int
		min, max time.Duration
		ok       bool
	}{
		{"2", 1, 2 * time.Second, 2500 * time.Millisecond, true},
		{"", 1, time.Second, 1250 * time.Millisecond, true},
		{"", 3, 4 * time.Second, 5 * time.Second, true},
		{"soon", 2, 2 * time.Second, 2500 * time.Millisecond, true},
		{"0", 1, 200 * time.Millisecond, 250 * time.Millisecond, true},
		{"60", 1, 60 * time.Second, 75 * time.Second, true},
		{"61", 1, 0, 0, false},
	} {
		d, ok := retryWait(c.h, c.try)
		if ok != c.ok || (ok && (d < c.min || d > c.max)) {
			t.Errorf("retryWait(%q, %d) = %v, %v; want %v..%v, %v", c.h, c.try, d, ok, c.min, c.max, c.ok)
		}
	}
}
