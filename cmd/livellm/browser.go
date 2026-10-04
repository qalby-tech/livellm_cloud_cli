package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A browser's own settings: its language and time zone, the proxies it goes
// out through, and its profile (sign-ins, cookies, history) with snapshots,
// export and import. Secret values (a proxy's password, a change-IP link, a
// profile's password) are read from environment variables, stdin or a file,
// never from the command line, so they stay out of ps and shell history.

const browserUsage = "browser what? locales, locale, proxy, profile or cookies"

func cmdBrowser(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(browserUsage)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "locales", "languages":
		return browserLocales(rest)
	case "locale", "language", "timezone":
		return browserLocale(rest)
	case "proxy", "proxies":
		return cmdBrowserProxy(rest)
	case "profile", "profiles":
		return cmdBrowserProfile(rest)
	case "cookies":
		return cmdBrowserCookies(rest)
	}
	return fmt.Errorf("browser has no %q: locales, locale, proxy, profile or cookies", sub)
}

// hint adds what to do next to a refusal the platform words generally.
func hint(id string, err error) error {
	var p *problem
	if !asProblem(err, &p) || p.Next != "" {
		return err
	}
	switch p.Code {
	case "needs_restart":
		p.Next = "livellm restart " + id + " (once; it keeps its profile)"
	case "profile_newer":
		p.Next = "add --force to import it anyway"
	}
	return err
}

// flagsGiven are the flags the command line set, even to "".
func flagsGiven(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// parseArgs reads flags that may come before or after the positional
// arguments (`profile import b1 file --force` and `--force b1 file` alike).
func parseArgs(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		_ = fs.Parse(args)
		args = fs.Args()
		if len(args) == 0 {
			return pos
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// --- secrets -------------------------------------------------------------------

// secretFrom reads a secret from the variable named, or from stdin. Both or
// neither given is "" with no error when optional.
func secretFrom(envVar string, stdin bool, what string) (string, error) {
	switch {
	case envVar != "" && stdin:
		return "", fmt.Errorf("%s: pass --password-env or --password-stdin, not both", what)
	case envVar != "":
		v := os.Getenv(envVar)
		if v == "" {
			return "", fmt.Errorf("%s: the variable %s is empty or unset", what, envVar)
		}
		return v, nil
	case stdin:
		return readStdinSecret(what)
	}
	return "", nil
}

// stdinReader is where --password-stdin reads (a test swaps it).
var stdinReader io.Reader = os.Stdin

func readStdinSecret(what string) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(stdinReader, 64<<10))
	if err != nil {
		return "", err
	}
	v := strings.TrimRight(string(raw), "\r\n")
	if v == "" {
		return "", fmt.Errorf("%s: nothing came on stdin", what)
	}
	return v, nil
}

// fileSecret is a value from a settings file: as written, or from a
// variable when written env:NAME.
func fileSecret(v, what string) (string, error) {
	name, ok := strings.CutPrefix(v, "env:")
	if !ok {
		return v, nil
	}
	got := os.Getenv(name)
	if got == "" {
		return "", fmt.Errorf("%s: the variable %s is empty or unset", what, name)
	}
	return got, nil
}

// --- language, time zone, location ------------------------------------------------

func browserLocales([]string) error {
	var out map[string]any
	if err := call("GET", "/v1/browsers/locales", nil, &out); err != nil {
		return err
	}
	return print(out)
}

// geolocation reads --geolocation: off (sites are refused), default (Chrome
// asks, as it does with nothing set) or LAT,LON[,ACCURACY] (always there).
// Fields it doesn't set are sent as null so a merge drops the old ones.
func geolocation(v string) (map[string]any, error) {
	g := map[string]any{"latitude": nil, "longitude": nil, "accuracy": nil}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "deny", "denied":
		g["mode"] = "off"
		return g, nil
	case "", "default", "prompt", "ask":
		g["mode"] = "prompt"
		return g, nil
	}
	parts := strings.Split(v, ",")
	if len(parts) != 2 && len(parts) != 3 {
		return nil, fmt.Errorf("--geolocation takes off, default or LAT,LON[,ACCURACY], e.g. 55.75,37.62")
	}
	nums := make([]float64, len(parts))
	for i, p := range parts {
		n, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, fmt.Errorf("--geolocation: %q isn't a number", p)
		}
		nums[i] = n
	}
	if nums[0] < -90 || nums[0] > 90 || nums[1] < -180 || nums[1] > 180 {
		return nil, fmt.Errorf("--geolocation: latitude goes from -90 to 90, longitude from -180 to 180")
	}
	g["mode"], g["latitude"], g["longitude"] = "fixed", nums[0], nums[1]
	if len(nums) == 3 {
		if nums[2] < 1 || nums[2] > 10000 {
			return nil, fmt.Errorf("--geolocation: accuracy is 1 to 10000 metres")
		}
		g["accuracy"] = nums[2]
	}
	return g, nil
}

// localeChange is the browser block a locale change sends: only what the
// command line set; "" clears a value.
func localeChange(set map[string]bool, locale, timezone, languages, geo string) (map[string]any, error) {
	b := map[string]any{}
	if set["locale"] {
		b["locale"] = strings.TrimSpace(locale)
	}
	if set["timezone"] {
		b["timezone"] = strings.TrimSpace(timezone)
	}
	if set["languages"] {
		list := []string{}
		for _, l := range strings.Split(languages, ",") {
			if l = strings.TrimSpace(l); l != "" {
				list = append(list, l)
			}
		}
		b["languages"] = list
	}
	if set["geolocation"] {
		g, err := geolocation(geo)
		if err != nil {
			return nil, err
		}
		b["geolocation"] = g
	}
	return b, nil
}

func browserLocale(args []string) error {
	id, rest, err := needArg(args, "browser")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("browser locale", flag.ExitOnError)
	locale := fs.String("locale", "", "its language and region, e.g. ru-RU ('' clears; livellm browser locales lists them)")
	timezone := fs.String("timezone", "", "its time zone, e.g. Europe/Moscow ('' clears)")
	languages := fs.String("languages", "", "the languages sites are told, in order, e.g. ru-RU,ru,en ('' derives them from --locale)")
	geo := fs.String("geolocation", "", "off, default, or LAT,LON[,ACCURACY]")
	_ = fs.Parse(rest)
	set := flagsGiven(fs)
	if len(set) == 0 {
		w, err := findWorkload(id)
		if err != nil {
			return err
		}
		b, _ := w["browser"].(map[string]any)
		out := map[string]any{"id": id}
		for _, k := range []string{"locale", "timezone", "languages", "geolocation"} {
			if v, ok := b[k]; ok {
				out[k] = v
			}
		}
		return print(out)
	}
	block, err := localeChange(set, *locale, *timezone, *languages, *geo)
	if err != nil {
		return err
	}
	if err := patchWorkload(id, map[string]any{"browser": block}); err != nil {
		return err
	}
	return print(map[string]any{"changed": id, "note": "the browser restarts to take it; its profile stays"})
}

// --- proxies ---------------------------------------------------------------------

func proxyPath(id string) string { return workloadPath(id) + "/proxy" }

// A rotation may first ask a mobile proxy for a new address and then wait for
// it to show (the platform answers within 150 seconds); setting proxies
// checks the address sites see.
var proxyClient = longClient(3 * time.Minute)

func cmdBrowserProxy(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("browser proxy what? show, set, clear, remove or rotate")
	}
	sub, rest := args[0], args[1:]
	id, rest, err := needArg(rest, "browser")
	if err != nil {
		return err
	}
	switch sub {
	case "show", "status", "get":
		var out map[string]any
		if err := call("GET", proxyPath(id), nil, &out); err != nil {
			return hint(id, err)
		}
		return print(out)
	case "set", "put":
		return proxySet(id, rest)
	case "clear", "direct", "off":
		raw, _, err := sendWith(proxyClient, "DELETE", proxyPath(id), nil)
		if err != nil {
			return hint(id, err)
		}
		return printAnswer(raw, map[string]any{"cleared": id, "note": "it goes out directly now; set proxies again with livellm browser proxy set"})
	case "remove", "rm":
		raw, _, err := sendWith(proxyClient, "DELETE", proxyPath(id)+"?remove=true", nil)
		if err != nil {
			return hint(id, err)
		}
		return printAnswer(raw, map[string]any{"removed": id, "note": "the browser restarts without proxies"})
	case "rotate", "next":
		fs := flag.NewFlagSet("browser proxy rotate", flag.ExitOnError)
		to := fs.String("to", "", "this proxy, by name, instead of the next one")
		_ = fs.Parse(rest)
		body := map[string]any{}
		if *to != "" {
			body["to"] = *to
		}
		raw, _, err := sendWith(proxyClient, "POST", proxyPath(id)+"/rotate", body)
		if err != nil {
			return hint(id, err)
		}
		return printAnswer(raw, map[string]any{"rotated": id})
	}
	return fmt.Errorf("browser proxy has no %q: show, set, clear, remove or rotate", sub)
}

// printAnswer prints the platform's answer, or fallback when it said nothing.
func printAnswer(raw []byte, fallback map[string]any) error {
	var out any
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &out) != nil {
		return print(fallback)
	}
	if m, ok := out.(map[string]any); ok && len(m) == 0 {
		return print(fallback)
	}
	return print(out)
}

// proxyFlags are what `proxy set` takes on the command line. Everything that
// names an upstream is NAME=VALUE; secrets are names of variables.
type proxyFlags struct {
	file                         string
	upstreams, logins, passwords repeated
	changeIPs, methods, mins     repeated
	noLogin, noChangeIP          repeated
	passwordStdin                bool
	rotation, order, checkURL    string
	every                        int
	set                          map[string]bool
}

// upstreamKeys are what an upstream may hold; the credential fields are sent
// once and never come back.
var upstreamKeys = map[string]bool{
	"name": true, "server": true, "hasAuth": true, "hasChangeIp": true,
	"changeIpMethod": true, "minChangeIpSeconds": true,
	"username": true, "password": true, "changeIpUrl": true,
}

// proxyBlock is the block in a `proxy show` answer or in a settings file:
// {"proxy": {...}} or the block itself.
func proxyBlock(m map[string]any) map[string]any {
	if p, ok := m["proxy"].(map[string]any); ok {
		return p
	}
	if p, ok := m["spec"].(map[string]any); ok {
		return proxyBlock(p)
	}
	if _, ok := m["upstreams"]; ok {
		return m
	}
	if _, ok := m["rotation"]; ok {
		return m
	}
	return nil
}

func listOfMaps(v any) []map[string]any {
	var out []map[string]any
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func nameValue(flag, v string) (string, string, error) {
	n, val, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(n) == "" {
		return "", "", fmt.Errorf("--%s takes NAME=VALUE, got %q", flag, v)
	}
	return strings.TrimSpace(n), val, nil
}

// checkServer refuses an address with a login in it: the login would be on
// the command line, and the platform wants it apart anyway.
func checkServer(name, server string) error {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return fmt.Errorf("proxy %s: %q isn't an address like socks5://host:1080 or http://host:3128", name, server)
	}
	if u.User != nil {
		return fmt.Errorf("proxy %s: put the login in --login %s=USER and --password-env, not in the address", name, name)
	}
	return nil
}

// proxyRequest builds the whole proxy block `proxy set` sends from what is
// there now (current, the `proxy show` answer), the file and the flags.
// Upstreams the file or --upstream don't list are kept as they are when
// neither lists any; a stored login or change-IP link is kept unless new
// values come or --no-login / --no-change-ip says to drop it.
func proxyRequest(current map[string]any, f proxyFlags) (map[string]any, error) {
	var file map[string]any
	if f.file != "" {
		raw, err := os.ReadFile(f.file)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("%s isn't a JSON object: %w", f.file, err)
		}
		if file = proxyBlock(m); file == nil {
			return nil, fmt.Errorf("%s should hold {\"upstreams\": [...], \"rotation\": {...}}", f.file)
		}
	}
	cur := proxyBlock(current)
	if cur == nil {
		cur = map[string]any{}
	}

	// The upstream list: the file's and the flags', or what is there now.
	var ups []map[string]any
	index := map[string]int{}
	add := func(u map[string]any) {
		n, _ := u["name"].(string)
		if i, ok := index[n]; ok {
			for k, v := range u {
				ups[i][k] = v
			}
			return
		}
		index[n] = len(ups)
		ups = append(ups, u)
	}
	_, fileHasList := file["upstreams"]
	if fileHasList || len(f.upstreams) > 0 {
		for _, u := range listOfMaps(file["upstreams"]) {
			c := map[string]any{}
			for k, v := range u {
				if !upstreamKeys[k] {
					return nil, fmt.Errorf("%s: an upstream has no %q (it takes name, server, username, password, changeIpUrl, changeIpMethod, minChangeIpSeconds, hasAuth, hasChangeIp)", f.file, k)
				}
				c[k] = v
			}
			n, _ := c["name"].(string)
			if n == "" {
				return nil, fmt.Errorf("%s: every upstream needs a name", f.file)
			}
			for _, k := range []string{"username", "password", "changeIpUrl"} {
				if s, ok := c[k].(string); ok {
					v, err := fileSecret(s, "proxy "+n+" "+k)
					if err != nil {
						return nil, err
					}
					c[k] = v
				}
			}
			add(c)
		}
		for _, v := range f.upstreams {
			n, server, err := nameValue("upstream", v)
			if err != nil {
				return nil, err
			}
			add(map[string]any{"name": n, "server": strings.TrimSpace(server)})
		}
	} else {
		for _, u := range listOfMaps(cur["upstreams"]) {
			c := map[string]any{}
			for _, k := range []string{"name", "server", "changeIpMethod", "minChangeIpSeconds"} {
				if v, ok := u[k]; ok && v != nil {
					c[k] = v
				}
			}
			add(c)
		}
	}
	find := func(flag, n string) (map[string]any, error) {
		if i, ok := index[n]; ok {
			return ups[i], nil
		}
		return nil, fmt.Errorf("--%s %s: there is no proxy called %s (add it with --upstream %s=socks5://host:port)", flag, n, n, n)
	}

	// Logins: --login NAME=USER with its password from --password-env
	// [NAME=]VAR or --password-stdin (one login then).
	pwVar := map[string]string{}
	bare := ""
	for _, v := range f.passwords {
		if n, val, ok := strings.Cut(v, "="); ok {
			pwVar[strings.TrimSpace(n)] = strings.TrimSpace(val)
		} else {
			bare = strings.TrimSpace(v)
		}
	}
	if (bare != "" || f.passwordStdin) && len(f.logins) > 1 {
		return nil, fmt.Errorf("with several --login, give each its password: --password-env NAME=VAR")
	}
	if bare != "" && f.passwordStdin {
		return nil, fmt.Errorf("pass --password-env or --password-stdin, not both")
	}
	for _, v := range f.logins {
		n, user, err := nameValue("login", v)
		if err != nil {
			return nil, err
		}
		u, err := find("login", n)
		if err != nil {
			return nil, err
		}
		u["username"] = user
		envVar := pwVar[n]
		delete(pwVar, n)
		if envVar == "" {
			envVar = bare
			bare = ""
		}
		if envVar == "" && !f.passwordStdin {
			return nil, fmt.Errorf("--login %s needs its password: --password-env %s=VAR or --password-stdin", n, n)
		}
		pw, err := secretFrom(envVar, f.passwordStdin && envVar == "", "proxy "+n+" password")
		if err != nil {
			return nil, err
		}
		u["password"] = pw
	}
	if len(f.logins) == 0 && (bare != "" || f.passwordStdin) {
		// A password for the one upstream the file gave a username without one.
		var target map[string]any
		for _, u := range ups {
			if _, hasUser := u["username"]; hasUser && u["password"] == nil {
				if target != nil {
					return nil, fmt.Errorf("several proxies need a password: --password-env NAME=VAR for each")
				}
				target = u
			}
		}
		if target == nil {
			return nil, fmt.Errorf("a password for which proxy? add --login NAME=USER")
		}
		pw, err := secretFrom(bare, f.passwordStdin, fmt.Sprintf("proxy %v password", target["name"]))
		if err != nil {
			return nil, err
		}
		target["password"] = pw
	}
	for n, envVar := range pwVar {
		u, err := find("password-env", n)
		if err != nil {
			return nil, err
		}
		if _, hasUser := u["username"]; !hasUser {
			return nil, fmt.Errorf("--password-env %s: add --login %s=USER (a login is a username and a password)", n, n)
		}
		pw, err := secretFrom(envVar, false, "proxy "+n+" password")
		if err != nil {
			return nil, err
		}
		u["password"] = pw
	}
	for _, v := range f.changeIPs {
		n, envVar, err := nameValue("change-ip-env", v)
		if err != nil {
			return nil, err
		}
		u, err := find("change-ip-env", n)
		if err != nil {
			return nil, err
		}
		link, err := secretFrom(strings.TrimSpace(envVar), false, "proxy "+n+" change-IP link")
		if err != nil {
			return nil, err
		}
		u["changeIpUrl"] = link
	}
	for _, v := range f.methods {
		n, m, err := nameValue("change-ip-method", v)
		if err != nil {
			return nil, err
		}
		u, err := find("change-ip-method", n)
		if err != nil {
			return nil, err
		}
		m = strings.ToUpper(strings.TrimSpace(m))
		if m != "GET" && m != "POST" {
			return nil, fmt.Errorf("--change-ip-method %s: GET or POST", n)
		}
		u["changeIpMethod"] = m
	}
	for _, v := range f.mins {
		n, s, err := nameValue("min-change-ip", v)
		if err != nil {
			return nil, err
		}
		u, err := find("min-change-ip", n)
		if err != nil {
			return nil, err
		}
		secs, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "s"))
		if err != nil || secs < 10 || secs > 3600 {
			return nil, fmt.Errorf("--min-change-ip %s: seconds from 10 to 3600", n)
		}
		u["minChangeIpSeconds"] = secs
	}
	for _, n := range f.noLogin {
		u, err := find("no-login", n)
		if err != nil {
			return nil, err
		}
		if _, sent := u["password"]; sent {
			return nil, fmt.Errorf("proxy %s: a new login and --no-login at once", n)
		}
		delete(u, "username")
		u["hasAuth"] = false
	}
	for _, n := range f.noChangeIP {
		u, err := find("no-change-ip", n)
		if err != nil {
			return nil, err
		}
		if _, sent := u["changeIpUrl"]; sent {
			return nil, fmt.Errorf("proxy %s: a new change-IP link and --no-change-ip at once", n)
		}
		u["hasChangeIp"] = false
	}
	for _, u := range ups {
		n, _ := u["name"].(string)
		s, _ := u["server"].(string)
		if s == "" {
			return nil, fmt.Errorf("proxy %s needs its address: --upstream %s=socks5://host:port", n, n)
		}
		if err := checkServer(n, s); err != nil {
			return nil, err
		}
		_, user := u["username"]
		_, pw := u["password"]
		if user != pw {
			return nil, fmt.Errorf("proxy %s: a login is a username and a password, both", n)
		}
	}

	// Rotation and the address check: the file's or what is there now, then
	// the flags.
	rot := map[string]any{}
	src, _ := cur["rotation"].(map[string]any)
	if r, ok := file["rotation"].(map[string]any); ok {
		src = r
	}
	for _, k := range []string{"mode", "everyMinutes", "order"} {
		if v, ok := src[k]; ok && v != nil {
			rot[k] = v
		}
	}
	if f.set["rotation"] {
		switch f.rotation {
		case "off", "session", "interval":
		default:
			return nil, fmt.Errorf("--rotation: off, session or interval")
		}
		rot["mode"] = f.rotation
	}
	if f.set["order"] && f.order != "sequential" && f.order != "random" {
		return nil, fmt.Errorf("--order: sequential or random")
	}
	if f.set["every"] {
		if f.every < 1 || f.every > 1440 {
			return nil, fmt.Errorf("--every: minutes from 1 to 1440")
		}
		if !f.set["rotation"] {
			rot["mode"] = "interval"
		}
		rot["everyMinutes"] = f.every
	}
	if f.set["order"] {
		rot["order"] = f.order
	}
	if m, _ := rot["mode"].(string); m != "interval" {
		delete(rot, "everyMinutes")
	}
	if ups == nil {
		ups = []map[string]any{}
	}
	body := map[string]any{"upstreams": ups}
	if len(rot) > 0 {
		body["rotation"] = rot
	}
	check, _ := cur["checkUrl"].(string)
	if c, ok := file["checkUrl"].(string); ok {
		check = c
	}
	if f.set["check-url"] {
		check = strings.TrimSpace(f.checkURL)
	}
	if check != "" {
		body["checkUrl"] = check
	}
	return body, nil
}

func proxySet(id string, rest []string) error {
	fs := flag.NewFlagSet("browser proxy set", flag.ExitOnError)
	var f proxyFlags
	fs.StringVar(&f.file, "f", "", "a JSON file: {\"upstreams\": [...], \"rotation\": {...}, \"checkUrl\": ...}; a secret in it may read env:VAR")
	fs.Var(&f.upstreams, "upstream", "a proxy: NAME=socks5://host:port (http, https, socks5; repeatable). Given, these are the list")
	fs.Var(&f.logins, "login", "a proxy's username: NAME=USER (its password from --password-env or --password-stdin)")
	fs.Var(&f.passwords, "password-env", "the variable holding a proxy's password: [NAME=]VAR (repeatable)")
	fs.BoolVar(&f.passwordStdin, "password-stdin", false, "read the one proxy password from stdin")
	fs.Var(&f.changeIPs, "change-ip-env", "a mobile proxy's change-IP link, from a variable: NAME=VAR")
	fs.Var(&f.methods, "change-ip-method", "how to call it: NAME=GET or NAME=POST (GET when left out)")
	fs.Var(&f.mins, "min-change-ip", "the least time between new addresses: NAME=SECONDS (10 to 3600, 60 when left out)")
	fs.Var(&f.noLogin, "no-login", "drop a proxy's stored login: NAME")
	fs.Var(&f.noChangeIP, "no-change-ip", "drop a proxy's stored change-IP link: NAME")
	fs.StringVar(&f.rotation, "rotation", "", "off, session (a new proxy for each Browser API session) or interval")
	fs.IntVar(&f.every, "every", 0, "with interval: minutes between proxies (1 to 1440)")
	fs.StringVar(&f.order, "order", "", "sequential or random")
	fs.StringVar(&f.checkURL, "check-url", "", "where to read the address sites see ('' for the platform's)")
	_ = fs.Parse(rest)
	f.set = flagsGiven(fs)
	if len(f.set) == 0 {
		return fmt.Errorf("set what? --upstream NAME=socks5://host:port, -f FILE, or --rotation")
	}
	current := map[string]any{}
	if err := call("GET", proxyPath(id), nil, &current); err != nil {
		var p *problem
		// Nothing set yet reads as not found; the write says if the browser
		// itself isn't there.
		if !asProblem(err, &p) || p.Status != 404 {
			return hint(id, err)
		}
	}
	body, err := proxyRequest(current, f)
	if err != nil {
		return err
	}
	raw, _, err := sendWith(proxyClient, "PUT", proxyPath(id), body)
	if err != nil {
		return hint(id, err)
	}
	names := []string{}
	for _, u := range body["upstreams"].([]map[string]any) {
		names = append(names, fmt.Sprint(u["name"]))
	}
	sort.Strings(names)
	return printAnswer(raw, map[string]any{"set": id, "proxies": names})
}

// --- profiles ------------------------------------------------------------------

func profilePath(id string) string { return workloadPath(id) + "/profile" }

// A snapshot or a restore pauses the browser while it copies the profile
// (the platform answers within 10 minutes).
var profileClient = longClient(11 * time.Minute)

const pauseNote = "Taking a snapshot closes the browser's tabs for a few seconds."

func cmdBrowserProfile(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("browser profile what? list, snapshot, restore, delete, export, import or copy")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls", "show":
		id, _, err := needArg(rest, "browser")
		if err != nil {
			return err
		}
		var out map[string]any
		if err := call("GET", profilePath(id), nil, &out); err != nil {
			return hint(id, err)
		}
		return print(out)
	case "snapshot", "save":
		return profileSnapshot(rest)
	case "restore", "switch":
		return profileRestore(rest)
	case "delete", "rm":
		return profileDeleteSnapshot(rest)
	case "export":
		return profileExport(rest)
	case "import":
		return profileImport(rest)
	case "copy":
		return profileCopy(rest)
	}
	return fmt.Errorf("browser profile has no %q: list, snapshot, restore, delete, export, import or copy", sub)
}

func profileSnapshot(args []string) error {
	fs := flag.NewFlagSet("browser profile snapshot", flag.ExitOnError)
	name := fs.String("name", "", "what to call it")
	pos := parseArgs(fs, args)
	if len(pos) != 1 {
		return fmt.Errorf("which browser? livellm browser profile snapshot ID [--name N]")
	}
	id := pos[0]
	body := map[string]any{}
	if *name != "" {
		body["name"] = *name
	}
	raw, _, err := sendWith(profileClient, "POST", profilePath(id)+"/snapshots", body)
	if err != nil {
		return hint(id, err)
	}
	return printAnswer(raw, map[string]any{"snapshot": id, "note": pauseNote})
}

func profileRestore(args []string) error {
	fs := flag.NewFlagSet("browser profile restore", flag.ExitOnError)
	keep := fs.Bool("keep-current", false, "keep the profile it has now as a snapshot first")
	yes := fs.Bool("y", false, "don't ask")
	pos := parseArgs(fs, args)
	if len(pos) != 2 {
		return fmt.Errorf("livellm browser profile restore ID SNAPSHOT (livellm browser profile list ID shows them)")
	}
	id, snap := pos[0], pos[1]
	if !*keep && !*yes && !confirm(fmt.Sprintf("Switch %s back to snapshot %s? Its profile now is replaced (--keep-current keeps it as a snapshot).", id, snap)) {
		return fmt.Errorf("nothing was restored")
	}
	raw, _, err := sendWith(profileClient, "POST", profilePath(id)+"/snapshots/"+url.PathEscape(snap)+"/restore",
		map[string]any{"keepCurrent": *keep})
	if err != nil {
		return hint(id, err)
	}
	return printAnswer(raw, map[string]any{"restored": snap, "to": id})
}

func profileDeleteSnapshot(args []string) error {
	fs := flag.NewFlagSet("browser profile delete", flag.ExitOnError)
	yes := fs.Bool("y", false, "don't ask")
	pos := parseArgs(fs, args)
	if len(pos) != 2 {
		return fmt.Errorf("livellm browser profile delete ID SNAPSHOT")
	}
	id, snap := pos[0], pos[1]
	if !*yes && !confirm(fmt.Sprintf("Delete snapshot %s of %s?", snap, id)) {
		return fmt.Errorf("nothing was deleted")
	}
	raw, _, err := sendWith(profileClient, "DELETE", profilePath(id)+"/snapshots/"+url.PathEscape(snap), nil)
	if err != nil {
		return hint(id, err)
	}
	return printAnswer(raw, map[string]any{"deleted": snap, "of": id})
}

// exportName is the file name the platform suggests, kept to a plain name in
// the current folder.
func exportName(h http.Header, id string, encrypted bool) string {
	if _, params, err := mime.ParseMediaType(h.Get("Content-Disposition")); err == nil {
		if n := filepath.Base(params["filename"]); n != "" && n != "." && n != "/" && n != ".." {
			return n
		}
	}
	n := id + "-" + time.Now().UTC().Format("2006-01-02") + ".llcprofile"
	if encrypted {
		n += ".age"
	}
	return n
}

// saveStream writes body to path as it arrives, through a file beside it
// that takes its place only when the whole profile is there: a broken
// download never leaves half a profile under the name asked for. A profile
// holds sign-ins, so only this user reads it.
func saveStream(body io.Reader, path string, want int64) (int64, error) {
	if path == "-" {
		return io.Copy(os.Stdout, body)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".livellm-export-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return 0, err
	}
	n, err := io.Copy(tmp, body)
	if err == nil && want >= 0 && n != want {
		err = fmt.Errorf("the download stopped after %d of %d bytes", n, want)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("export didn't finish, nothing was saved: %w", err)
	}
	return n, os.Rename(tmp.Name(), path)
}

func profileExport(args []string) error {
	fs := flag.NewFlagSet("browser profile export", flag.ExitOnError)
	out := fs.String("o", "", "the file to write (- for stdout; the platform's name for it when left out)")
	snap := fs.String("snapshot", "", "export this snapshot instead of the profile as it is now")
	pwEnv := fs.String("password-env", "", "protect the file with the password in this variable")
	pwStdin := fs.Bool("password-stdin", false, "protect the file with a password read from stdin")
	pos := parseArgs(fs, args)
	if len(pos) != 1 {
		return fmt.Errorf("which browser? livellm browser profile export ID -o FILE")
	}
	id := pos[0]
	password, err := secretFrom(*pwEnv, *pwStdin, "the export's password")
	if err != nil {
		return err
	}
	body := map[string]any{}
	if *snap != "" {
		body["snapshot"] = *snap
	}
	if password != "" {
		body["password"] = password
	}
	b, _ := json.Marshal(body)
	res, err := do(streamClient, "POST", profilePath(id)+"/export", bytes.NewReader(b), int64(len(b)), "application/json", nil)
	if err != nil {
		return hint(id, err)
	}
	defer res.Body.Close()
	path := *out
	if path == "" {
		path = exportName(res.Header, id, password != "")
	}
	n, err := saveStream(res.Body, path, res.ContentLength)
	if err != nil || path == "-" {
		return err
	}
	abs, _ := filepath.Abs(path)
	answer := map[string]any{"saved": abs, "bytes": n, "passwordProtected": password != ""}
	if *snap == "" {
		answer["note"] = "Exporting closed the browser's tabs for a few seconds."
	}
	return print(answer)
}

// importProfile sends a profile file to a browser as it is read from disk.
func importProfile(id, file, password string, force bool) (map[string]any, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a folder; pass the .llcprofile file", file)
	}
	path := profilePath(id) + "/import"
	if force {
		path += "?force=1"
	}
	hdr := map[string]string{}
	if password != "" {
		hdr["X-Profile-Password"] = password
	}
	res, err := do(streamClient, "POST", path, f, st.Size(), "application/octet-stream", hdr)
	if err != nil {
		return nil, hint(id, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	if len(out) == 0 {
		out = map[string]any{"imported": file, "to": id}
	}
	return out, nil
}

func profileImport(args []string) error {
	fs := flag.NewFlagSet("browser profile import", flag.ExitOnError)
	force := fs.Bool("force", false, "import a profile from a newer Chrome than the browser runs")
	pwEnv := fs.String("password-env", "", "the file's password, from this variable")
	pwStdin := fs.Bool("password-stdin", false, "the file's password, from stdin")
	yes := fs.Bool("y", false, "don't ask")
	pos := parseArgs(fs, args)
	if len(pos) != 2 {
		return fmt.Errorf("livellm browser profile import ID FILE")
	}
	id, file := pos[0], pos[1]
	if _, err := os.Stat(file); err != nil {
		return err
	}
	password, err := secretFrom(*pwEnv, *pwStdin, "the file's password")
	if err != nil {
		return err
	}
	if !*yes {
		if *pwStdin {
			return fmt.Errorf("with --password-stdin there is no asking: add -y to replace %s's profile", id)
		}
		if !confirm(fmt.Sprintf("Replace %s's profile with %s? Its sign-ins and cookies now are lost (take a snapshot first to keep them).", id, file)) {
			return fmt.Errorf("nothing was imported")
		}
	}
	out, err := importProfile(id, file, password, *force)
	if err != nil {
		return err
	}
	return print(out)
}

func profileCopy(args []string) error {
	fs := flag.NewFlagSet("browser profile copy", flag.ExitOnError)
	from := fs.String("from", "", "the browser to copy the profile from (it stays as it is)")
	snap := fs.String("snapshot", "", "copy this snapshot of it instead of its profile now")
	yes := fs.Bool("y", false, "don't ask")
	pos := parseArgs(fs, args)
	if len(pos) != 1 || *from == "" {
		return fmt.Errorf("livellm browser profile copy ID --from OTHER [--snapshot S]")
	}
	id := pos[0]
	if !*yes && !confirm(fmt.Sprintf("Replace %s's profile with %s's?", id, *from)) {
		return fmt.Errorf("nothing was copied")
	}
	body := map[string]any{"from": *from}
	if *snap != "" {
		body["snapshot"] = *snap
	}
	raw, _, err := sendWith(streamClient, "POST", profilePath(id)+"/copy", body)
	if err != nil {
		return hint(id, err)
	}
	return printAnswer(raw, map[string]any{"copied": *from, "to": id})
}

// --- cookies -------------------------------------------------------------------

const (
	maxCookies     = 5000
	maxCookieBytes = 5 << 20
)

// cookieList reads a cookie file: a JSON list of cookies (Playwright's
// shape), or a Playwright storage state, whose "cookies" it takes.
func cookieList(raw []byte) ([]any, error) {
	var list []any
	if err := json.Unmarshal(raw, &list); err != nil {
		var state struct {
			Cookies []any `json:"cookies"`
		}
		if json.Unmarshal(raw, &state) != nil || state.Cookies == nil {
			return nil, fmt.Errorf("the file should hold a JSON list of cookies, or a Playwright storage state")
		}
		list = state.Cookies
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("the file holds no cookies")
	}
	if len(list) > maxCookies {
		return nil, fmt.Errorf("%d cookies; at most %d at once", len(list), maxCookies)
	}
	return list, nil
}

func cmdBrowserCookies(args []string) error {
	if len(args) == 0 || args[0] != "import" {
		return fmt.Errorf("livellm browser cookies import ID FILE")
	}
	pos := args[1:]
	if len(pos) != 2 {
		return fmt.Errorf("livellm browser cookies import ID FILE")
	}
	id, file := pos[0], pos[1]
	st, err := os.Stat(file)
	if err != nil {
		return err
	}
	if st.Size() > maxCookieBytes {
		return fmt.Errorf("%s is %d bytes; at most 5 MiB at once", file, st.Size())
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	list, err := cookieList(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	ans, _, err := send("POST", workloadPath(id)+"/cookies", list)
	if err != nil {
		return hint(id, err)
	}
	return printAnswer(ans, map[string]any{"imported": len(list), "to": id})
}

// profileWait is how long a new browser gets to come up before its profile
// goes in.
var profileWait = 15 * time.Minute

// createBrowser makes a browser, and with a profile file waits until it is up
// and imports the profile into it. The file and its password are checked
// before anything is made.
func createBrowser(body map[string]any, locale, timezone, profile, profilePwEnv string) error {
	id, _ := body["id"].(string)
	if id == "" {
		return fmt.Errorf("the browser needs an id: --id NAME")
	}
	if locale != "" {
		body["locale"] = locale
	}
	if timezone != "" {
		body["timezone"] = timezone
	}
	if profilePwEnv != "" && profile == "" {
		return fmt.Errorf("--profile-password-env goes with --profile FILE")
	}
	password := ""
	if profile != "" {
		st, err := os.Stat(profile)
		if err != nil {
			return err
		}
		if st.IsDir() {
			return fmt.Errorf("%s is a folder; pass the .llcprofile file", profile)
		}
		if password, err = secretFrom(profilePwEnv, false, "the profile's password"); err != nil {
			return err
		}
	}
	if err := call("POST", "/v1/workloads/browser", body, nil); err != nil {
		return err
	}
	out := map[string]any{"created": id, "type": "browser"}
	if profile == "" {
		return print(out)
	}
	again := fmt.Sprintf("the browser is made; put the profile in with: livellm browser profile import %s %s -y", id, profile)
	if _, err := waitReady(id, profileWait); err != nil {
		return fmt.Errorf("%w — %s", err, again)
	}
	imported, err := importProfile(id, profile, password, false)
	if err != nil {
		return fmt.Errorf("%w — %s", err, again)
	}
	out["profile"] = imported
	return print(out)
}
