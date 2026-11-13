package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GPUs by the hour: the models there are and what is free (gpu models), the
// workspace's rentals (gpu ls), what would fit (gpu fit), renting now (gpu
// rent) or booking for later (gpu book), a later end (gpu extend), a change
// of use (gpu use), releasing or cancelling (gpu release) and taking one GPU
// off a cluster (gpu remove-card). A machine, an app or a Desktop App takes a
// rental by its id (create/set --gpu ID; set --no-gpu takes it off).
//
// Renting spends money. A booking is charged its whole span, and cancelling
// one within a day of its start is charged in full: the API answers 428 until
// the request names the rental (or card) as confirm, which this command sends
// only with --confirm-charge, and only after the API asked for it.
//
// Several GPUs at once are clusters: --cluster, once per cluster, as
// comma-separated key=value pairs:
//
//	use=machine|apps                 (required)
//	cards=MODEL:N[+MODEL:N…]         how many of each model, on one server
//	                                 (cards= may also repeat)
//	location=auto|region:R|host:H    where it may go (left out: auto)
//	target=ID[+ID…]                  a booking's machine, or its apps

// gpuOffMsg is what every gpu command says where the API answers 404: GPUs
// aren't on this server at all ("404 page not found"), or renting is off
// ("GPUs aren't offered on this cloud.").
const gpuOffMsg = "GPU renting isn't offered on this server"

// confirmChargeNext is the step a 428 without --confirm-charge gets.
const confirmChargeNext = "add --confirm-charge to accept the charge"

// gpuWait is how often gpu use asks whether the GPU is ready.
var gpuWait = 5 * time.Second

// gpuNow is the clock --hours counts from (tests replace it).
var gpuNow = time.Now

func cmdGPU(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("gpu what? models, ls, fit, rent, book, extend, use, release or remove-card")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "models":
		return gpuModels(rest)
	case "ls", "list":
		return gpuList(rest)
	case "fit":
		return gpuFit(rest)
	case "rent":
		return gpuRent(rest, false)
	case "book":
		return gpuRent(rest, true)
	case "extend":
		return gpuExtend(rest)
	case "use":
		return gpuUse(rest)
	case "release", "cancel":
		return gpuRelease(rest)
	case "remove-card":
		return gpuRemoveCard(rest)
	}
	return fmt.Errorf("gpu has no %q: models, ls, fit, rent, book, extend, use, release or remove-card", sub)
}

// gpuErr says the GPU routes' refusals plainly: any 404 is GPUs (or renting)
// not offered on this server (an unknown rental is 422 gpu_unknown), and a
// 428 names --confirm-charge. Everything else, an agent's 403 included, is
// passed on in the API's words.
func gpuErr(err error) error {
	var p *problem
	if !asProblem(err, &p) {
		return err
	}
	switch {
	case p.Status == 404:
		return &problem{Status: 404, Msg: gpuOffMsg, Code: "gpu_off"}
	case p.Status == 428:
		p.Next = confirmChargeNext
	}
	return err
}

// --- reads ---------------------------------------------------------------------

func gpuModels(args []string) error {
	fs := flag.NewFlagSet("gpu models", flag.ExitOnError)
	use := fs.String("use", "", "which use the hourly slots count free cards for: machine or apps (default apps)")
	from := fs.String("from", "", "with --to: free cards in each whole UTC hour from this time")
	to := fs.String("to", "", "with --from: … to this time (at most 14 days later)")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %q", fs.Arg(0))
	}
	if (*from == "") != (*to == "") {
		return fmt.Errorf("--from and --to go together")
	}
	q := url.Values{}
	if *use != "" {
		u, err := gpuUseWord(*use)
		if err != nil {
			return err
		}
		q.Set("use", u)
	}
	for name, v := range map[string]string{"from": *from, "to": *to} {
		if v == "" {
			continue
		}
		t, err := gpuTime("--"+name, v)
		if err != nil {
			return err
		}
		q.Set(name, t)
	}
	path := "/v1/gpus/models"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out map[string]any
	if err := call("GET", path, nil, &out); err != nil {
		return gpuErr(err)
	}
	if out == nil {
		out = map[string]any{}
	}
	lines := []string{}
	models, _ := out["models"].([]any)
	for _, raw := range models {
		m, _ := raw.(map[string]any)
		lines = append(lines, gpuModelLine(m))
	}
	if len(lines) == 0 {
		lines = append(lines, "no GPUs on this server")
	}
	out["summary"] = lines
	return print(out)
}

// gpuModelLine is one model as the summary says it: "RTX 5090 (rtx5090):
// $0.60 an hour; 4 cards, free now 1 for a machine, 2 for apps".
func gpuModelLine(m map[string]any) string {
	name := gpuName(m)
	price := "no price yet"
	if c, ok := m["centsHour"].(float64); ok {
		price = fmt.Sprintf("$%.2f an hour", c/100)
	}
	free, _ := m["freeNow"].(map[string]any)
	return fmt.Sprintf("%s: %s; %s cards, free now %s for a machine, %s for apps",
		name, price, num(orZero(m["total"])), num(orZero(free["machine"])), num(orZero(free["apps"])))
}

func gpuName(m map[string]any) string {
	name, _ := m["name"].(string)
	model, _ := m["model"].(string)
	switch {
	case name == "":
		return model
	case model == "" || model == name:
		return name
	}
	return name + " (" + model + ")"
}

func orZero(v any) any {
	if v == nil {
		return 0
	}
	return v
}

func gpuList(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("livellm gpu ls takes nothing more")
	}
	var out map[string]any
	if err := call("GET", "/v1/gpus", nil, &out); err != nil {
		return gpuErr(err)
	}
	if out == nil {
		out = map[string]any{}
	}
	lines := []string{}
	rentals, _ := out["rentals"].([]any)
	for _, raw := range rentals {
		r, _ := raw.(map[string]any)
		lines = append(lines, gpuRentalLine(r))
	}
	if len(lines) == 0 {
		lines = append(lines, "no GPUs in this workspace")
	}
	out["summary"] = lines
	return print(out)
}

// gpuRentalLine is one rental as the summary says it: "g-3f9a12bc: RTX 5090
// for a machine, active, ends 2026-11-14 10:00 UTC, used by box".
func gpuRentalLine(r map[string]any) string {
	id, _ := r["id"].(string)
	what := gpuName(r)
	if cards, _ := r["cards"].([]any); len(cards) > 1 {
		counts := map[string]int{}
		for _, raw := range cards {
			c, _ := raw.(map[string]any)
			counts[gpuName(c)]++
		}
		names := make([]string, 0, len(counts))
		for n := range counts {
			names = append(names, n)
		}
		sort.Strings(names)
		parts := []string{}
		for _, n := range names {
			parts = append(parts, fmt.Sprintf("%d × %s", counts[n], n))
		}
		what = strings.Join(parts, " + ")
	}
	use, _ := r["use"].(string)
	if use == "machine" {
		what += " for a machine"
	} else if use != "" {
		what += " for apps"
	}
	parts := []string{what}
	if state, _ := r["state"].(string); state != "" {
		parts = append(parts, state)
	}
	if phase, _ := r["phase"].(string); phase != "" && phase != "ready" {
		parts = append(parts, phase)
	}
	if noEnd, _ := r["noEnd"].(bool); noEnd {
		parts = append(parts, "no end")
	} else {
		if s, _ := r["startsAt"].(string); s != "" {
			if b, _ := r["booked"].(bool); b {
				parts = append(parts, "starts "+gpuShowTime(s))
			}
		}
		if e, _ := r["endsAt"].(string); e != "" {
			parts = append(parts, "ends "+gpuShowTime(e))
		}
	}
	if used, _ := r["usedBy"].([]any); len(used) > 0 {
		names := []string{}
		for _, u := range used {
			names = append(names, fmt.Sprint(u))
		}
		parts = append(parts, "used by "+strings.Join(names, ", "))
	}
	return id + ": " + strings.Join(parts, ", ")
}

func gpuShowTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

// --- the request: one GPU, or clusters --------------------------------------------

// gpuAskFlags are rent's, book's and fit's flags.
type gpuAskFlags struct {
	fs       *flag.FlagSet
	model    *string
	use      *string
	location *string
	hours    *int
	startsAt *string
	endsAt   *string
	target   *string
	clusters repeated
}

func addGPUAskFlags(fs *flag.FlagSet) *gpuAskFlags {
	f := &gpuAskFlags{fs: fs}
	f.model = fs.String("model", "", "one GPU of this model (livellm gpu models lists them)")
	f.use = fs.String("use", "", "machine (one machine gets the whole card) or apps (apps and Desktop Apps share it)")
	f.location = fs.String("location", "", "auto (the default), region:NAME or host:NAME (livellm hosts)")
	f.hours = fs.Int("hours", 0, "for at least this many hours, to the next whole UTC hour (rent: from now; book: from --starts-at)")
	f.startsAt = fs.String("starts-at", "", "a booking's start, a whole UTC hour (2026-11-14T10:00:00Z)")
	f.endsAt = fs.String("ends-at", "", "its end, a whole UTC hour (rent now: left out, it runs until released)")
	f.target = fs.String("target", "", "a booking: the machine, or the apps (a,b), that get the GPU at its start")
	fs.Var(&f.clusters, "cluster", "several GPUs: use=machine|apps,cards=MODEL:N[+MODEL:N],location=auto|region:R|host:H,target=ID[+ID] (repeat it for more clusters)")
	return f
}

// body is what rent, book and fit send. book says whether it is a booking
// (--starts-at required); fit takes anything, nothing included.
func (f *gpuAskFlags) body(mode string) (map[string]any, error) {
	given := flagsGiven(f.fs)
	body := map[string]any{}
	if len(f.clusters) > 0 {
		for _, name := range []string{"model", "use", "location", "target"} {
			if given[name] {
				return nil, fmt.Errorf("--%s goes with one GPU; with --cluster put it in each cluster (use=, location=, target=)", name)
			}
		}
		list := []any{}
		for _, c := range f.clusters {
			cl, err := parseGPUCluster(c)
			if err != nil {
				return nil, err
			}
			list = append(list, cl)
		}
		body["clusters"] = list
	} else {
		if *f.model != "" || mode != "fit" {
			if *f.model == "" {
				return nil, fmt.Errorf("which GPU? --model MODEL (livellm gpu models lists them), or --cluster for several")
			}
			if *f.use == "" {
				return nil, fmt.Errorf("for what? --use machine or --use apps")
			}
			body["model"] = *f.model
		}
		if *f.use != "" {
			u, err := gpuUseWord(*f.use)
			if err != nil {
				return nil, err
			}
			body["use"] = u
		}
		if *f.location != "" {
			loc, err := parseGPULocation("--location", *f.location)
			if err != nil {
				return nil, err
			}
			body["location"] = loc
		}
		if *f.target != "" {
			if mode != "book" {
				return nil, fmt.Errorf("--target goes with gpu book: a rental now is put on a machine or an app with livellm set ID --gpu RENTAL")
			}
			t, err := gpuTarget(body["use"].(string), *f.target)
			if err != nil {
				return nil, err
			}
			body["target"] = t
		}
	}
	if mode == "rent" && *f.startsAt != "" {
		return nil, fmt.Errorf("--starts-at is a booking: livellm gpu book …")
	}
	if mode == "book" && *f.startsAt == "" {
		return nil, fmt.Errorf("a booking needs --starts-at (a whole UTC hour) and --ends-at or --hours")
	}
	if given["hours"] && *f.endsAt != "" {
		return nil, fmt.Errorf("--hours or --ends-at, not both")
	}
	if given["hours"] && *f.hours < 1 {
		return nil, fmt.Errorf("--hours is a whole number of hours, at least 1")
	}
	var start time.Time
	if *f.startsAt != "" {
		s, err := gpuTime("--starts-at", *f.startsAt)
		if err != nil {
			return nil, err
		}
		body["startsAt"] = s
		start, _ = time.Parse(time.RFC3339, s)
	}
	switch {
	case *f.endsAt != "":
		e, err := gpuTime("--ends-at", *f.endsAt)
		if err != nil {
			return nil, err
		}
		body["endsAt"] = e
	case given["hours"]:
		from := start
		if from.IsZero() {
			from = gpuNow().UTC()
		}
		body["endsAt"] = hoursEnd(from, *f.hours).Format(time.RFC3339)
	case mode == "book":
		return nil, fmt.Errorf("a booking needs --ends-at or --hours")
	}
	return body, nil
}

// hoursEnd is the first whole UTC hour at least n hours after from: a rental
// now ends on a whole hour, so it may run up to an hour more than asked (and
// is charged by the minute until then).
func hoursEnd(from time.Time, n int) time.Time {
	end := from.Add(time.Duration(n) * time.Hour)
	if t := end.Truncate(time.Hour); !t.Equal(end) {
		return t.Add(time.Hour)
	}
	return end
}

// gpuTime reads a time as RFC 3339, or as 2006-01-02T15:04 / 2006-01-02
// 15:04 in UTC, and sends it as RFC 3339 in UTC. Whether it is a whole hour is
// the API's to say (gpu_span).
func gpuTime(flagName, v string) (string, error) {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15", "2006-01-02 15"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
	}
	return "", fmt.Errorf("%s: %q isn't a time; write it as 2026-11-14T10:00:00Z (UTC)", flagName, v)
}

func gpuUseWord(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "machine", "machines", "vm":
		return "machine", nil
	case "apps", "app":
		return "apps", nil
	}
	return "", fmt.Errorf("a GPU's use is machine or apps, not %q", v)
}

// parseGPULocation reads auto, region:NAME or host:NAME (= works too).
func parseGPULocation(what, v string) (map[string]any, error) {
	v = strings.TrimSpace(v)
	if v == "auto" || v == "" {
		return map[string]any{"mode": "auto"}, nil
	}
	sep := strings.IndexAny(v, ":=")
	if sep > 0 {
		mode, name := v[:sep], strings.TrimSpace(v[sep+1:])
		if (mode == "region" || mode == "host") && name != "" {
			return map[string]any{"mode": mode, mode: name}, nil
		}
	}
	return nil, fmt.Errorf("%s is auto, region:NAME or host:NAME, not %q", what, v)
}

// gpuTarget is a booking's target: a machine, or apps (a,b or a+b).
func gpuTarget(use, v string) (map[string]any, error) {
	ids := []string{}
	for _, s := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '+' }) {
		if s = strings.TrimSpace(s); s != "" {
			ids = append(ids, s)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("--target names a machine, or apps")
	}
	if use == "machine" {
		if len(ids) > 1 {
			return nil, fmt.Errorf("a machine's GPU goes to one machine: --target names one")
		}
		return map[string]any{"machine": ids[0]}, nil
	}
	return map[string]any{"apps": ids}, nil
}

// parseGPUCluster reads one --cluster: comma-separated key=value pairs, use=
// required, cards= one or more MODEL:N joined by + (or cards= repeated),
// location= and target= optional.
func parseGPUCluster(spec string) (map[string]any, error) {
	cl := map[string]any{}
	cards := map[string]any{}
	order := []string{}
	target := ""
	for _, pair := range strings.Split(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || v == "" {
			return nil, fmt.Errorf("--cluster %q: %q isn't key=value (use=, cards=, location=, target=)", spec, pair)
		}
		switch k {
		case "use":
			u, err := gpuUseWord(v)
			if err != nil {
				return nil, fmt.Errorf("--cluster %q: %w", spec, err)
			}
			cl["use"] = u
		case "cards", "card":
			for _, c := range strings.Split(v, "+") {
				model, n, hasN := strings.Cut(strings.TrimSpace(c), ":")
				model = strings.TrimSpace(model)
				count := 1
				if hasN {
					var err error
					if count, err = strconv.Atoi(strings.TrimSpace(n)); err != nil || count < 1 {
						return nil, fmt.Errorf("--cluster %q: %q is MODEL:COUNT, a whole number of at least 1", spec, c)
					}
				}
				if model == "" {
					return nil, fmt.Errorf("--cluster %q: cards= names a model (livellm gpu models)", spec)
				}
				if _, seen := cards[model]; seen {
					return nil, fmt.Errorf("--cluster %q names %s twice", spec, model)
				}
				cards[model] = count
				order = append(order, model)
			}
		case "location":
			loc, err := parseGPULocation("--cluster location", v)
			if err != nil {
				return nil, err
			}
			cl["location"] = loc
		case "target":
			target = v
		default:
			return nil, fmt.Errorf("--cluster %q: there is no %q (use=, cards=, location=, target=)", spec, k)
		}
	}
	if cl["use"] == nil {
		return nil, fmt.Errorf("--cluster %q: say its use: use=machine or use=apps", spec)
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("--cluster %q: say its GPUs: cards=MODEL:N", spec)
	}
	cl["cards"] = cards
	if target != "" {
		t, err := gpuTarget(cl["use"].(string), target)
		if err != nil {
			return nil, err
		}
		cl["target"] = t
	}
	return cl, nil
}

// --- fit, rent, book ----------------------------------------------------------------

func gpuFit(args []string) error {
	fs := flag.NewFlagSet("gpu fit", flag.ExitOnError)
	ask := addGPUAskFlags(fs)
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %q", fs.Arg(0))
	}
	body, err := ask.body("fit")
	if err != nil {
		return err
	}
	var out map[string]any
	if err := call("POST", "/v1/gpus/fit", body, &out); err != nil {
		return gpuErr(err)
	}
	if out == nil {
		out = map[string]any{}
	}
	lines := []string{}
	clusters, _ := out["clusters"].([]any)
	for i, raw := range clusters {
		c, _ := raw.(map[string]any)
		fits, _ := c["fits"].(bool)
		line := fmt.Sprintf("cluster %d: ", i+1)
		if fits {
			line += "fits"
		} else {
			line += "doesn't fit"
		}
		if max, _ := c["max"].(map[string]any); len(max) > 0 {
			keys := make([]string, 0, len(max))
			for k := range max {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := []string{}
			for _, k := range keys {
				parts = append(parts, k+" "+num(max[k]))
			}
			line += " (at most " + strings.Join(parts, ", ") + ")"
		}
		lines = append(lines, line)
	}
	hosts, _ := out["hosts"].([]any)
	lines = append(lines, fmt.Sprintf("%d server(s) this workspace may use", len(hosts)))
	out["summary"] = lines
	return print(out)
}

func gpuRent(args []string, book bool) error {
	name, mode := "gpu rent", "rent"
	if book {
		name, mode = "gpu book", "book"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	ask := addGPUAskFlags(fs)
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %q", fs.Arg(0))
	}
	body, err := ask.body(mode)
	if err != nil {
		return err
	}
	var out map[string]any
	if err := call("POST", "/v1/gpus", body, &out); err != nil {
		return gpuErr(err)
	}
	if out == nil {
		out = map[string]any{}
	}
	verb := "rented"
	if book {
		verb = "booked"
	}
	lines := []string{}
	if r, _ := out["rental"].(map[string]any); r != nil {
		lines = append(lines, verb+" "+gpuRentalLine(r))
	}
	clusters, _ := out["clusters"].([]any)
	for _, raw := range clusters {
		r, _ := raw.(map[string]any)
		lines = append(lines, verb+" "+gpuRentalLine(r))
	}
	if !book && body["endsAt"] == nil {
		lines = append(lines, "it runs, and is charged, until you release it: livellm gpu release ID")
	}
	out["summary"] = lines
	return print(out)
}

// --- extend, use, release, remove-card ------------------------------------------------

func gpuPath(id string) string { return "/v1/gpus/" + url.PathEscape(id) }

func gpuExtend(args []string) error {
	id, rest, err := needArg(args, "rental")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("gpu extend", flag.ExitOnError)
	endsAt := fs.String("ends-at", "", "the later end, a whole UTC hour")
	_ = fs.Parse(rest)
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %q", fs.Arg(0))
	}
	if *endsAt == "" {
		return fmt.Errorf("until when? --ends-at 2026-11-14T10:00:00Z (a whole UTC hour)")
	}
	e, err := gpuTime("--ends-at", *endsAt)
	if err != nil {
		return err
	}
	var out map[string]any
	if err := call("PATCH", gpuPath(id), map[string]any{"endsAt": e}, &out); err != nil {
		return gpuErr(err)
	}
	return printRental(out, "extended ")
}

func printRental(out map[string]any, verb string) error {
	if out == nil {
		out = map[string]any{}
	}
	lines := []string{}
	if r, _ := out["rental"].(map[string]any); r != nil {
		lines = append(lines, verb+gpuRentalLine(r))
	}
	out["summary"] = lines
	return print(out)
}

func gpuUse(args []string) error {
	id, rest, err := needArg(args, "rental")
	if err != nil {
		return err
	}
	word, rest, err := needArg(rest, "use (machine or apps)")
	if err != nil {
		return fmt.Errorf("for what? livellm gpu use %s machine|apps", id)
	}
	use, err := gpuUseWord(word)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("gpu use", flag.ExitOnError)
	noWait := fs.Bool("no-wait", false, "return while the GPU is still getting ready")
	timeout := fs.Duration("timeout", 15*time.Minute, "give up waiting after this long")
	_ = fs.Parse(rest)
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %q", fs.Arg(0))
	}
	var out map[string]any
	if err := call("POST", gpuPath(id)+"/use", map[string]any{"use": use}, &out); err != nil {
		return gpuErr(err)
	}
	r, _ := out["rental"].(map[string]any)
	phase, _ := r["phase"].(string)
	if *noWait || !strings.HasPrefix(phase, "switching") {
		return printRental(out, "")
	}
	fmt.Fprintf(os.Stderr, "the GPU is getting ready for %s; waiting (--no-wait returns at once)\n", use)
	deadline := gpuNow().Add(*timeout)
	for {
		sleep(gpuWait)
		var list struct {
			Rentals []map[string]any `json:"rentals"`
		}
		if err := call("GET", "/v1/gpus", nil, &list); err != nil {
			return gpuErr(err)
		}
		var now map[string]any
		for _, x := range list.Rentals {
			if x["id"] == id {
				now = x
			}
		}
		if now == nil {
			return fmt.Errorf("%s is no longer in livellm gpu ls", id)
		}
		phase, _ = now["phase"].(string)
		switch {
		case phase == "ready":
			return printRental(map[string]any{"rental": now}, "")
		case phase == "failed":
			return fmt.Errorf("%s couldn't get ready for %s (phase failed): livellm gpu ls shows it", id, use)
		}
		if gpuNow().After(deadline) {
			return fmt.Errorf("%s is still %s after %s: livellm gpu ls shows when it is ready", id, phase, *timeout)
		}
	}
}

func gpuRelease(args []string) error {
	id, rest, err := needArg(args, "rental")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("gpu release", flag.ExitOnError)
	confirm := fs.Bool("confirm-charge", false, "accept the charge of cancelling a booking that starts within a day")
	_ = fs.Parse(rest)
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %q", fs.Arg(0))
	}
	out, err := gpuChargedDelete(gpuPath(id), id, *confirm)
	if err != nil {
		return err
	}
	if out == nil {
		out = map[string]any{}
	}
	lines := []string{}
	if r, _ := out["rental"].(map[string]any); r != nil {
		verb := "released "
		if r["state"] == "cancelled" {
			verb = "cancelled "
		}
		lines = append(lines, verb+gpuRentalLine(r))
	}
	if c, ok := out["chargedCents"].(float64); ok {
		lines = append(lines, fmt.Sprintf("charged $%.2f in all", c/100))
	}
	out["summary"] = lines
	return print(out)
}

func gpuRemoveCard(args []string) error {
	id, rest, err := needArg(args, "rental")
	if err != nil {
		return err
	}
	card, rest, err := needArg(rest, "GPU of the cluster")
	if err != nil {
		return fmt.Errorf("which GPU? livellm gpu remove-card %s CARD (its cards[].id in livellm gpu ls)", id)
	}
	fs := flag.NewFlagSet("gpu remove-card", flag.ExitOnError)
	confirm := fs.Bool("confirm-charge", false, "accept the charge of cancelling a booked GPU that starts within a day")
	_ = fs.Parse(rest)
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %q", fs.Arg(0))
	}
	out, err := gpuChargedDelete(gpuPath(id)+"/cards/"+url.PathEscape(card), card, *confirm)
	if err != nil {
		return err
	}
	return printRental(out, "took "+card+" off: ")
}

// gpuChargedDelete sends a DELETE with no body. When the API answers 428 (a
// booking within a day, charged in full) it sends it again with
// {"confirm": what} only when the person said --confirm-charge; else the
// API's words and how to accept. A free release never carries a confirm.
func gpuChargedDelete(path, what string, confirmCharge bool) (map[string]any, error) {
	var out map[string]any
	err := call("DELETE", path, nil, &out)
	var p *problem
	if err != nil && asProblem(err, &p) && p.Status == 428 && confirmCharge {
		out = nil
		err = call("DELETE", path, map[string]any{"confirm": what}, &out)
	}
	if err != nil {
		return nil, gpuErr(err)
	}
	return out, nil
}

// --- --gpu on create and set ----------------------------------------------------------

// gpuFlags are create's and set's --gpu and --no-gpu.
type gpuFlags struct {
	fs    *flag.FlagSet
	id    *string
	noGPU *bool
}

func addGPUFlags(fs *flag.FlagSet) *gpuFlags {
	return &gpuFlags{fs: fs,
		id:    fs.String("gpu", "", "a machine, an app or a Desktop App: the GPU rental it uses (livellm gpu ls; left out: kept)"),
		noGPU: fs.Bool("no-gpu", false, "set: take its GPU off"),
	}
}

func (g *gpuFlags) given() bool {
	f := flagsGiven(g.fs)
	return f["gpu"] || f["no-gpu"]
}

// value is what the flags send as gpu ("" takes it off).
func (g *gpuFlags) value(create bool) (string, error) {
	f := flagsGiven(g.fs)
	switch {
	case f["gpu"] && f["no-gpu"]:
		return "", fmt.Errorf("--gpu or --no-gpu, not both")
	case f["no-gpu"] && create:
		return "", fmt.Errorf("--no-gpu goes with set: a new resource has no GPU unless --gpu names one")
	case f["gpu"] && strings.TrimSpace(*g.id) == "":
		return "", fmt.Errorf("--gpu names a rental (livellm gpu ls); --no-gpu takes it off")
	case f["no-gpu"]:
		return "", nil
	}
	return strings.TrimSpace(*g.id), nil
}

// gpuKinds are the kinds that take a GPU.
func gpuKind(kind string) bool { return isMachine(kind) || kind == "pod" || kind == "desktop" }

const gpuKindsMsg = "--gpu goes with a machine, an app (pod) or a Desktop App (desktop)"

// withGPU puts --gpu / --no-gpu into body (a create's flat settings, or a
// set's block): a gpu the file says, if any, must be the same.
func withGPU(kind string, body map[string]any, file string, g *gpuFlags, create bool) error {
	if !g.given() {
		return nil
	}
	if !gpuKind(kind) {
		return fmt.Errorf("%s", gpuKindsMsg)
	}
	v, err := g.value(create)
	if err != nil {
		return err
	}
	if had, ok := body["gpu"]; ok && had != nil && had != v {
		return fmt.Errorf("%s says the GPU %q and the flags say %q: change one of them", file, had, v)
	}
	body["gpu"] = v
	return nil
}
