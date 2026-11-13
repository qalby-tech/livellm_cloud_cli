package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// gpuReq is one request the fake GPU API got.
type gpuReq struct {
	Method, Path, Query string
	Body                map[string]any
	Raw                 string
}

type gpuAnswer struct {
	status int
	body   string
}

// fakeGPU answers each request in turn from answers (the last one again once
// they run out) and keeps every request.
type fakeGPU struct {
	mu      sync.Mutex
	answers []gpuAnswer
	reqs    []gpuReq
}

func newFakeGPU(t *testing.T, answers ...gpuAnswer) *fakeGPU {
	t.Helper()
	f := &fakeGPU{answers: answers}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		req := gpuReq{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery, Raw: string(raw)}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &req.Body)
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		a := f.answers[0]
		if len(f.answers) > 1 {
			f.answers = f.answers[1:]
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	old, oldWait, oldNow := sleep, gpuWait, gpuNow
	sleep = func(time.Duration) {}
	gpuWait = 0
	t.Cleanup(func() { sleep, gpuWait, gpuNow = old, oldWait, oldNow })
	return f
}

func summaryOf(t *testing.T, out string) []string {
	t.Helper()
	var got struct {
		Summary []string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return got.Summary
}

const rentalJSON = `{"id":"g-3f9a12bc","model":"rtx5090","name":"RTX 5090","use":"machine","state":"active","phase":"ready",
 "startsAt":"2026-11-13T10:00:00Z","endsAt":"2026-11-13T14:00:00Z","booked":false,"usedBy":["box"],
 "cards":[{"id":"g-3f9a12bc","model":"rtx5090","name":"RTX 5090","state":"active","answering":true}]}`

func TestGPUModels(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{200, `{"models":[{"model":"rtx5090","name":"RTX 5090","centsHour":60,"total":4,
		"freeNow":{"machine":1,"apps":2},"freeUntil":{"machine":null,"apps":null}},{"model":"a100","name":"A100","total":1,"freeNow":{"machine":0,"apps":0}}]}`})
	out, err := stdoutOf(t, func() error {
		return cmdGPU([]string{"models", "--use", "machine", "--from", "2026-11-14T10:00:00Z", "--to", "2026-11-14 12:00"})
	})
	if err != nil {
		t.Fatal(err)
	}
	r := f.reqs[0]
	if r.Method != "GET" || r.Path != "/v1/gpus/models" {
		t.Fatalf("sent %s %s", r.Method, r.Path)
	}
	if !strings.Contains(r.Query, "use=machine") || !strings.Contains(r.Query, "from=2026-11-14T10%3A00%3A00Z") || !strings.Contains(r.Query, "to=2026-11-14T12%3A00%3A00Z") {
		t.Fatalf("query %q", r.Query)
	}
	want := []string{"RTX 5090 (rtx5090): $0.60 an hour; 4 cards, free now 1 for a machine, 2 for apps",
		"A100 (a100): no price yet; 1 cards, free now 0 for a machine, 0 for apps"}
	if got := summaryOf(t, out); !reflect.DeepEqual(got, want) {
		t.Fatalf("summary %q", got)
	}
	if err := cmdGPU([]string{"models", "--from", "2026-11-14T10:00:00Z"}); err == nil {
		t.Fatal("--from without --to was sent")
	}
}

func TestGPUList(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{200, `{"rentals":[` + rentalJSON + `,
		{"id":"g-00000001","use":"apps","state":"booked","booked":true,"phase":"waiting","startsAt":"2026-11-14T10:00:00Z","endsAt":"2026-11-14T12:00:00Z",
		 "cards":[{"id":"g-00000001","model":"rtx5090","name":"RTX 5090"},{"id":"g-00000002","model":"rtx5090","name":"RTX 5090"}]}]}`})
	out, err := stdoutOf(t, func() error { return cmdGPU([]string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	if f.reqs[0].Method != "GET" || f.reqs[0].Path != "/v1/gpus" {
		t.Fatalf("sent %+v", f.reqs[0])
	}
	want := []string{
		"g-3f9a12bc: RTX 5090 (rtx5090) for a machine, active, ends 2026-11-13 14:00 UTC, used by box",
		"g-00000001: 2 × RTX 5090 (rtx5090) for apps, booked, waiting, starts 2026-11-14 10:00 UTC, ends 2026-11-14 12:00 UTC",
	}
	if got := summaryOf(t, out); !reflect.DeepEqual(got, want) {
		t.Fatalf("summary %q", got)
	}
}

func TestGPUFit(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{200, `{"clusters":[{"fits":false,"max":{"rtx5090":1}}],"hosts":[{"name":"s1"}],"regions":[]}`})
	out, err := stdoutOf(t, func() error {
		return cmdGPU([]string{"fit", "--cluster", "use=machine,cards=rtx5090:2,location=host:s1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	r := f.reqs[0]
	want := map[string]any{"clusters": []any{map[string]any{"use": "machine", "cards": map[string]any{"rtx5090": float64(2)},
		"location": map[string]any{"mode": "host", "host": "s1"}}}}
	if r.Method != "POST" || r.Path != "/v1/gpus/fit" || !reflect.DeepEqual(r.Body, want) {
		t.Fatalf("sent %s %s %v", r.Method, r.Path, r.Body)
	}
	if got := summaryOf(t, out); !reflect.DeepEqual(got, []string{"cluster 1: doesn't fit (at most rtx5090 1)", "1 server(s) this workspace may use"}) {
		t.Fatalf("summary %q", got)
	}
	// No flags: an empty ask, which the API takes (it lists the servers).
	if _, err := stdoutOf(t, func() error { return cmdGPU([]string{"fit"}) }); err != nil {
		t.Fatal(err)
	}
	if f.reqs[1].Raw != "{}" {
		t.Fatalf("an empty fit sent %s", f.reqs[1].Raw)
	}
}

func TestGPURentOne(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{201, `{"rental":` + rentalJSON + `}`})
	gpuNow = func() time.Time { return time.Date(2026, 11, 13, 10, 20, 0, 0, time.UTC) }
	out, err := stdoutOf(t, func() error {
		return cmdGPU([]string{"rent", "--model", "rtx5090", "--use", "machine", "--hours", "2", "--location", "region:eu"})
	})
	if err != nil {
		t.Fatal(err)
	}
	r := f.reqs[0]
	want := map[string]any{"model": "rtx5090", "use": "machine", "endsAt": "2026-11-13T13:00:00Z",
		"location": map[string]any{"mode": "region", "region": "eu"}}
	if r.Method != "POST" || r.Path != "/v1/gpus" || !reflect.DeepEqual(r.Body, want) {
		t.Fatalf("sent %s %s %v", r.Method, r.Path, r.Body)
	}
	if s := summaryOf(t, out); len(s) != 1 || !strings.HasPrefix(s[0], "rented g-3f9a12bc: ") {
		t.Fatalf("summary %q", s)
	}
	// On a whole hour, --hours adds exactly that many.
	if got := hoursEnd(time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC), 1); !got.Equal(time.Date(2026, 11, 13, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("hoursEnd %v", got)
	}
}

func TestGPURentNoEndSaysSo(t *testing.T) {
	newFakeGPU(t, gpuAnswer{201, `{"rental":` + rentalJSON + `}`})
	out, err := stdoutOf(t, func() error { return cmdGPU([]string{"rent", "--model", "rtx5090", "--use", "apps"}) })
	if err != nil {
		t.Fatal(err)
	}
	s := summaryOf(t, out)
	if len(s) != 2 || !strings.Contains(s[1], "until you release it") {
		t.Fatalf("summary %q", s)
	}
}

func TestGPURentClusters(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{201, `{"clusters":[` + rentalJSON + `,` + rentalJSON + `]}`})
	_, err := stdoutOf(t, func() error {
		return cmdGPU([]string{"book", "--starts-at", "2026-11-14T10:00:00Z", "--hours", "3",
			"--cluster", "use=machine,cards=rtx5090:2+a100,target=box",
			"--cluster", "use=apps,cards=rtx5090:1,cards=l4:3,location=auto,target=web+api"})
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"startsAt": "2026-11-14T10:00:00Z", "endsAt": "2026-11-14T13:00:00Z",
		"clusters": []any{
			map[string]any{"use": "machine", "cards": map[string]any{"rtx5090": float64(2), "a100": float64(1)}, "target": map[string]any{"machine": "box"}},
			map[string]any{"use": "apps", "cards": map[string]any{"rtx5090": float64(1), "l4": float64(3)},
				"location": map[string]any{"mode": "auto"}, "target": map[string]any{"apps": []any{"web", "api"}}},
		},
	}
	if !reflect.DeepEqual(f.reqs[0].Body, want) {
		t.Fatalf("sent %v", f.reqs[0].Body)
	}
}

func TestGPUBookOne(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{201, `{"rental":` + rentalJSON + `}`})
	_, err := stdoutOf(t, func() error {
		return cmdGPU([]string{"book", "--model", "rtx5090", "--use", "apps", "--starts-at", "2026-11-14T10:00:00Z",
			"--ends-at", "2026-11-14T12:00:00Z", "--target", "web,api"})
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"model": "rtx5090", "use": "apps", "startsAt": "2026-11-14T10:00:00Z",
		"endsAt": "2026-11-14T12:00:00Z", "target": map[string]any{"apps": []any{"web", "api"}}}
	if !reflect.DeepEqual(f.reqs[0].Body, want) {
		t.Fatalf("sent %v", f.reqs[0].Body)
	}
}

// Refused before anything is sent.
func TestGPUAskRefusals(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{201, `{}`})
	for _, args := range [][]string{
		{"rent", "--use", "machine"},
		{"rent", "--model", "rtx5090"},
		{"rent", "--model", "rtx5090", "--use", "gpu"},
		{"rent", "--model", "rtx5090", "--use", "apps", "--starts-at", "2026-11-14T10:00:00Z", "--hours", "1"},
		{"rent", "--model", "rtx5090", "--use", "apps", "--target", "web"},
		{"rent", "--model", "rtx5090", "--use", "apps", "--hours", "1", "--ends-at", "2026-11-14T10:00:00Z"},
		{"rent", "--model", "rtx5090", "--use", "apps", "--hours", "0"},
		{"rent", "--model", "rtx5090", "--use", "apps", "--location", "moon"},
		{"rent", "--model", "rtx5090", "--use", "apps", "--ends-at", "tomorrow"},
		{"book", "--model", "rtx5090", "--use", "apps", "--ends-at", "2026-11-14T10:00:00Z"},
		{"book", "--model", "rtx5090", "--use", "apps", "--starts-at", "2026-11-14T10:00:00Z"},
		{"book", "--model", "rtx5090", "--use", "machine", "--starts-at", "2026-11-14T10:00:00Z", "--hours", "1", "--target", "a,b"},
		{"rent", "--model", "rtx5090", "--cluster", "use=apps,cards=rtx5090:1"},
		{"rent", "--cluster", "cards=rtx5090:1"},
		{"rent", "--cluster", "use=apps"},
		{"rent", "--cluster", "use=apps,cards=rtx5090:0"},
		{"rent", "--cluster", "use=apps,cards=rtx5090:1+rtx5090:2"},
		{"rent", "--cluster", "use=apps,cards=rtx5090:1,size=big"},
		{"rent", "--cluster", "use=apps,cards"},
		{"use", "g-1"},
		{"use", "g-1", "both"},
		{"extend", "g-1"},
		{"remove-card", "g-1"},
		{"nothing"},
	} {
		if err := cmdGPU(args); err == nil {
			t.Errorf("%v: not refused", args)
		}
	}
	if len(f.reqs) != 0 {
		t.Fatalf("sent %d requests for refused flags: %+v", len(f.reqs), f.reqs)
	}
}

func TestGPUExtend(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{200, `{"rental":` + rentalJSON + `}`})
	out, err := stdoutOf(t, func() error { return cmdGPU([]string{"extend", "g-3f9a12bc", "--ends-at", "2026-11-13T16:00:00Z"}) })
	if err != nil {
		t.Fatal(err)
	}
	r := f.reqs[0]
	if r.Method != "PATCH" || r.Path != "/v1/gpus/g-3f9a12bc" || r.Body["endsAt"] != "2026-11-13T16:00:00Z" || len(r.Body) != 1 {
		t.Fatalf("sent %+v", r)
	}
	if s := summaryOf(t, out); len(s) != 1 || !strings.HasPrefix(s[0], "extended g-3f9a12bc") {
		t.Fatalf("summary %q", s)
	}
}

func TestGPUUseWaits(t *testing.T) {
	switching := strings.Replace(rentalJSON, `"phase":"ready"`, `"phase":"switching-to-apps"`, 1)
	f := newFakeGPU(t,
		gpuAnswer{202, `{"rental":` + switching + `}`},
		gpuAnswer{200, `{"rentals":[` + switching + `]}`},
		gpuAnswer{200, `{"rentals":[` + rentalJSON + `]}`})
	out, err := stdoutOf(t, func() error { return cmdGPU([]string{"use", "g-3f9a12bc", "apps"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 3 || f.reqs[0].Method != "POST" || f.reqs[0].Path != "/v1/gpus/g-3f9a12bc/use" || f.reqs[0].Body["use"] != "apps" ||
		f.reqs[1].Path != "/v1/gpus" || f.reqs[2].Path != "/v1/gpus" {
		t.Fatalf("sent %+v", f.reqs)
	}
	if s := summaryOf(t, out); len(s) != 1 || !strings.HasPrefix(s[0], "g-3f9a12bc") {
		t.Fatalf("summary %q", s)
	}
}

func TestGPUUseNoWait(t *testing.T) {
	switching := strings.Replace(rentalJSON, `"phase":"ready"`, `"phase":"switching-to-machine"`, 1)
	f := newFakeGPU(t, gpuAnswer{202, `{"rental":` + switching + `}`})
	if _, err := stdoutOf(t, func() error { return cmdGPU([]string{"use", "g-3f9a12bc", "machine", "--no-wait"}) }); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 1 {
		t.Fatalf("--no-wait asked again: %+v", f.reqs)
	}
}

func TestGPUUseFailed(t *testing.T) {
	switching := strings.Replace(rentalJSON, `"phase":"ready"`, `"phase":"switching-to-apps"`, 1)
	failed := strings.Replace(rentalJSON, `"phase":"ready"`, `"phase":"failed"`, 1)
	newFakeGPU(t, gpuAnswer{202, `{"rental":` + switching + `}`}, gpuAnswer{200, `{"rentals":[` + failed + `]}`})
	_, err := stdoutOf(t, func() error { return cmdGPU([]string{"use", "g-3f9a12bc", "apps"}) })
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("a failed switch: %v", err)
	}
}

func TestGPUUseTimeout(t *testing.T) {
	switching := strings.Replace(rentalJSON, `"phase":"ready"`, `"phase":"switching-to-apps"`, 1)
	newFakeGPU(t, gpuAnswer{202, `{"rental":` + switching + `}`}, gpuAnswer{200, `{"rentals":[` + switching + `]}`})
	clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	gpuNow = func() time.Time { clock = clock.Add(time.Minute); return clock }
	_, err := stdoutOf(t, func() error { return cmdGPU([]string{"use", "g-3f9a12bc", "apps", "--timeout", "5m"}) })
	if err == nil || !strings.Contains(err.Error(), "still switching-to-apps") {
		t.Fatalf("a timeout: %v", err)
	}
}

const confirm428 = `{"error":"This booking starts within a day, so it is charged in full. Send its id as confirm to cancel it.","code":"confirm_required"}`

// Without --confirm-charge: one request, no body, the API's words and how to
// accept, non-zero.
func TestGPUReleaseNeedsConfirm(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{428, confirm428})
	err := cmdGPU([]string{"release", "g-3f9a12bc"})
	var p *problem
	if !asProblem(err, &p) || p.Status != 428 {
		t.Fatalf("not the 428: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "This booking starts within a day") || !strings.Contains(err.Error(), "add --confirm-charge to accept the charge") {
		t.Fatalf("said %q", err.Error())
	}
	if exitCode(err) == 0 {
		t.Fatal("exit 0")
	}
	if len(f.reqs) != 1 || f.reqs[0].Method != "DELETE" || f.reqs[0].Raw != "" {
		t.Fatalf("sent %+v", f.reqs)
	}
}

func TestGPUReleaseConfirmCharge(t *testing.T) {
	cancelled := strings.Replace(rentalJSON, `"state":"active"`, `"state":"cancelled"`, 1)
	f := newFakeGPU(t, gpuAnswer{428, confirm428}, gpuAnswer{200, `{"rental":` + cancelled + `,"chargedCents":240}`})
	out, err := stdoutOf(t, func() error { return cmdGPU([]string{"release", "g-3f9a12bc", "--confirm-charge"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 2 || f.reqs[0].Raw != "" || f.reqs[1].Method != "DELETE" || f.reqs[1].Path != "/v1/gpus/g-3f9a12bc" ||
		!reflect.DeepEqual(f.reqs[1].Body, map[string]any{"confirm": "g-3f9a12bc"}) {
		t.Fatalf("sent %+v", f.reqs)
	}
	s := summaryOf(t, out)
	if len(s) != 2 || !strings.HasPrefix(s[0], "cancelled g-3f9a12bc") || s[1] != "charged $2.40 in all" {
		t.Fatalf("summary %q", s)
	}
}

// A free release with --confirm-charge never carries a confirm.
func TestGPUReleaseFreeSendsNoConfirm(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{200, `{"rental":` + rentalJSON + `,"chargedCents":0}`})
	if _, err := stdoutOf(t, func() error { return cmdGPU([]string{"release", "g-3f9a12bc", "--confirm-charge"}) }); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 1 || f.reqs[0].Raw != "" {
		t.Fatalf("sent %+v", f.reqs)
	}
}

func TestGPURemoveCard(t *testing.T) {
	f := newFakeGPU(t, gpuAnswer{428, confirm428})
	err := cmdGPU([]string{"remove-card", "g-3f9a12bc", "g-7c01d2e4"})
	if err == nil || !strings.Contains(err.Error(), confirmChargeNext) || len(f.reqs) != 1 {
		t.Fatalf("%v %+v", err, f.reqs)
	}
	f = newFakeGPU(t, gpuAnswer{428, confirm428}, gpuAnswer{200, `{"rental":` + rentalJSON + `}`})
	out, err := stdoutOf(t, func() error { return cmdGPU([]string{"remove-card", "g-3f9a12bc", "g-7c01d2e4", "--confirm-charge"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 2 || f.reqs[1].Path != "/v1/gpus/g-3f9a12bc/cards/g-7c01d2e4" ||
		!reflect.DeepEqual(f.reqs[1].Body, map[string]any{"confirm": "g-7c01d2e4"}) {
		t.Fatalf("sent %+v", f.reqs)
	}
	if s := summaryOf(t, out); len(s) != 1 || !strings.HasPrefix(s[0], "took g-7c01d2e4 off: ") {
		t.Fatalf("summary %q", s)
	}
}

// Every 404 on the GPU routes is renting not offered: the router's (GPUs off)
// and the rentals' own.
func TestGPUNotOffered(t *testing.T) {
	for _, body := range []string{`404 page not found`, `{"error":"GPUs aren't offered on this cloud."}`} {
		for _, args := range [][]string{
			{"models"}, {"ls"}, {"fit"},
			{"rent", "--model", "x", "--use", "apps", "--hours", "1"},
			{"extend", "g-1", "--ends-at", "2026-11-14T10:00:00Z"},
			{"use", "g-1", "apps"}, {"release", "g-1"}, {"remove-card", "g-1", "g-2"},
		} {
			newFakeGPU(t, gpuAnswer{404, body})
			_, err := stdoutOf(t, func() error { return cmdGPU(args) })
			var p *problem
			if !asProblem(err, &p) || p.Status != 404 || p.Msg != gpuOffMsg {
				t.Errorf("%v on %s: %v", args, body, err)
			}
			if exitCode(err) != 1 {
				t.Errorf("%v: exit %d", args, exitCode(err))
			}
		}
	}
}

// An agent's 403 and the other refusals reach the person in the API's words.
func TestGPURefusalsInTheAPIsWords(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		exit   int
	}{
		{403, `{"error":"Agents can't rent GPUs. Do it in the LiveLLM console."}`, 2},
		{403, `{"error":"This API key can't rent GPUs. A person can give it the billing permission on the Keys page."}`, 2},
		{422, `{"error":"One GPU per cluster on this cloud.","code":"gpu_clusters_off"}`, 1},
		{409, `{"error":"No free RTX 5090 for those hours.","code":"gpu_none_free"}`, 4},
		{402, `{"error":"Free includes no resources.","code":"payg_off"}`, 2},
	} {
		newFakeGPU(t, gpuAnswer{c.status, c.body})
		_, err := stdoutOf(t, func() error {
			return cmdGPU([]string{"rent", "--cluster", "use=machine,cards=rtx5090:2", "--hours", "1"})
		})
		var api struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal([]byte(c.body), &api)
		if err == nil || !strings.HasPrefix(err.Error(), api.Error) {
			t.Errorf("%s: said %v", c.body, err)
		}
		if exitCode(err) != c.exit {
			t.Errorf("%s: exit %d, want %d", c.body, exitCode(err), c.exit)
		}
	}
}

// --gpu on create goes flat; on set into the kind's block, beside an app's
// dedicated cores; --no-gpu sends "".
func TestGPUOnCreateAndSet(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/app.json"
	if err := writeFile(file, `{"id":"web","image":"nginx"}`); err != nil {
		t.Fatal(err)
	}
	g := newGoldenAPI(t, map[string]string{
		"GET /v1/workspace": `{"spec":{"workloads":[{"id":"web","type":"pod","pod":{}},{"id":"box","type":"vm-ubuntu","vm":{}},{"id":"b","type":"browser"}]}}`,
	})
	if _, _, err := captured(t, func() error { return cmdCreate([]string{"pod", "-f", file, "--gpu", "g-3f9a12bc"}) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captured(t, func() error {
		return cmdSet([]string{"web", "--gpu", "g-3f9a12bc", "--dedicated-cores"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captured(t, func() error { return cmdSet([]string{"box", "--no-gpu"}) }); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`POST /v1/workloads/pod {"gpu":"g-3f9a12bc","id":"web","image":"nginx"}`,
		`PATCH /v1/workloads/web {"pod":{"dedicatedCores":true,"gpu":"g-3f9a12bc"}}`,
		`PATCH /v1/workloads/box {"vm":{"gpu":""}}`,
	}
	if !reflect.DeepEqual(g.writes, want) {
		t.Fatalf("sent\n%s", strings.Join(g.writes, "\n"))
	}
	// Refused before anything more is sent.
	for _, args := range [][]string{
		{"b", "--gpu", "g-1"},
		{"web", "--gpu", "g-1", "--no-gpu"},
		{"web", "--gpu", ""},
	} {
		if _, _, err := captured(t, func() error { return cmdSet(args) }); err == nil {
			t.Errorf("set %v: not refused", args)
		}
	}
	for _, args := range [][]string{
		{"pod", "-f", file, "--no-gpu"},
		{"storage", "-f", file, "--gpu", "g-1"},
	} {
		if _, _, err := captured(t, func() error { return cmdCreate(args) }); err == nil {
			t.Errorf("create %v: not refused", args)
		}
	}
	if err := writeFile(file, `{"id":"web","image":"nginx","gpu":"g-00000000"}`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captured(t, func() error { return cmdCreate([]string{"pod", "-f", file, "--gpu", "g-3f9a12bc"}) }); err == nil {
		t.Error("a file's other gpu: not refused")
	}
	if len(g.writes) != 3 {
		t.Fatalf("refused flags sent:\n%s", strings.Join(g.writes, "\n"))
	}
}

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }
