# livellm

The command line for LiveLLM Cloud: your machines, browsers, apps and databases
from a terminal.

```sh
go install github.com/qalby-tech/livellm_cloud_cli/cmd/livellm@latest

livellm login          # prints a link; press Allow, then run login again if it returned
livellm ls             # what you have
livellm logs web       # why something isn't working
livellm connect shop   # how to reach it
livellm exec box "uname -a"   # run a command on a machine (PowerShell on Windows)
livellm share desk-1   # a link to watch a screen (--control to use it)
livellm stop web       # stop it, keep its disks; livellm start web runs it again
```

`create` takes the same settings as the API. An app with a raw TCP port, a UDP
port and two volumes:

```sh
cat > game.json <<'JSON'
{
  "id": "minecraft",
  "image": "itzg/minecraft-server",
  "env": [{ "name": "EULA", "value": "TRUE" }],
  "ports": [
    { "name": "game", "port": 25565, "tcp": true,
      "access": { "allowCIDRs": ["203.0.113.0/24"] } },
    { "name": "voice", "port": 9987, "udp": true }
  ],
  "volumes": [
    { "name": "world", "size": "20Gi", "mountPath": "/data" },
    { "name": "backups", "size": "10Gi", "mountPath": "/backups" }
  ]
}
JSON
livellm create pod -f game.json
livellm ls --type pod   # the raw ports show as "tcp host:port" and "udp host:port"
```

A `tcp` or `udp` port gets a public `host:port` instead of an HTTPS address;
`access.allowCIDRs` limits who can reach it (it takes no password, so set it
unless the port is meant for everyone). Volumes keep their data across
restarts and stops; a volume can grow but not shrink, and removing one deletes
its data.

Several browsers behind one address, a Browser API:

```sh
livellm browser-api create scrapers --browsers agent-1,agent-2
livellm browser-api add scrapers agent-3      # one more browser, same address
livellm connect scrapers                       # its address and a token
```

A browser running elsewhere joins with `--remote office=wss://…`. If it needs a
login header, put the header in an environment variable and name it:
`--remote-auth office=OFFICE_AUTH`. It is never on the command line and never
shown again.

A call that names no browser goes to the one with the fewest open tabs; a
session (`X-Session-Id`) stays on its browser; `/browsers/agent-2/…` or
`X-Browser-Id: agent-2` picks one. `set ID -f changes.json` changes only the
settings the file holds.

A browser's language, time zone and location (a change restarts it; its
profile stays):

```sh
livellm browser locales                                  # what it takes
livellm create browser --id shop-ru --locale ru-RU --timezone Europe/Moscow
livellm browser locale shop-ru --geolocation 55.75,37.62 # or off, or default
livellm browser locale shop-ru --locale ''               # back to the default
```

Proxies, with a login and a mobile proxy's change-IP link. Secrets come from
variables or stdin, never the command line; a login already stored is kept
when a later `set` sends none:

```sh
export RES_USER=… RES_PW=… MOBILE_ROTATE='https://provider.example/rotate?key=…'
livellm browser proxy set shop-ru \
  --upstream res=socks5://res.example:1080 --username-env res=RES_USER --password-env res=RES_PW \
  --upstream mob=http://mobile.example:8000 --change-ip-env mob=MOBILE_ROTATE \
  --rotation interval --every 30
livellm browser proxy show shop-ru          # the address sites see, and where it comes from
livellm browser proxy rotate shop-ru        # the next proxy now; open connections drop, pages reconnect
livellm browser proxy set shop-ru --rotation session   # proxies and logins stay as they are
livellm browser proxy clear shop-ru         # go out directly (no restart); its proxies and logins are dropped
```

In a file (`-f proxy.json`) a secret may read `"password": "env:RES_PW"`.
`--upstream` or a list in the file replaces the list; left out, the list
stays as it is.
`session` gives a Browser API session a new proxy when it starts on a browser
no other recent session uses. The proxy covers the browser as LiveLLM starts
it; a program connected to the browser (over CDP) can go around it, and an
extension allowed to manage proxy or privacy settings can change it.
Before you change a browser's proxies (set, clear or rotate), ask the user
and wait for their agreement: it changes where the browser's traffic goes and
the address sites see. `proxy remove`, and a `create` or `set -f FILE` that
carries proxy settings, count too.

A browser's profile (its sign-ins, cookies and history): snapshots to switch
back to, and a file to move it with. Taking a snapshot, or exporting the
profile as it is now, closes the browser's tabs for a few seconds (an export
of a snapshot doesn't).

```sh
livellm browser profile snapshot shop-ru --name signed-in
livellm browser profile list shop-ru
livellm browser profile restore shop-ru SNAPSHOT --keep-current
PW=… livellm browser profile export shop-ru -o shop.llcprofile.age --password-env PW
livellm create browser --id shop-2 --profile shop.llcprofile.age --profile-password-env PW
livellm browser profile copy shop-3 --from shop-ru      # browser to browser
livellm browser cookies import shop-ru cookies.json     # a list of cookies, or a Playwright storage state
```

An exported file holds sign-ins: keep it private (the command saves it
readable by you alone, and without `-o` never over a file already there), or
protect it with a password (`age -d` opens it too). Only profiles exported from LiveLLM browsers can be imported; from
another Chrome, import its cookies. A profile holds the user's sign-ins:
before you export, import or copy one (`create browser --profile` imports
one too), or add cookies, ask the user and wait for their agreement, and never
upload an exported file.

Two browser engines: Chrome (the default, driven over CDP) and Camoufox
(Firefox-based, driven with Playwright). The engine is chosen when a browser
is made and can't change later; `ls` and `status` show `"engine": "camoufox"`
for Camoufox ones.

```sh
livellm browser engines                                   # what this platform offers
livellm create browser --id fox --engine camoufox --locale ru-RU --timezone Europe/Moscow
livellm browser-api create mixed --browsers fox,shop      # both engines, one address
livellm connect fox                                       # playwright.url and playwright.headers
```

Drive it with the Playwright version the answer names (`playwright.version`,
1.62 today; other versions are refused):

```python
from playwright.sync_api import sync_playwright  # pip install "playwright==1.62.*"
with sync_playwright() as p:
    b = p.firefox.connect(url, headers=headers)   # from livellm connect fox
    page = b.contexts[0].new_page()               # contexts[0] holds its cookies and sign-ins
```

Work in `contexts[0]` and close your pages, never the context or the browser;
a `new_context()` needs `no_viewport=True`. A Camoufox browser takes no
extensions (uBlock Origin is built in).

A Browser API holds browsers of both engines (`--all` is every browser in the
workspace, either engine; remote browsers are Chrome). Its `POST /start_session`
takes an optional engine, `-d '{"engine":"camoufox"}'` or `chrome`: the session
starts on the browser of that engine with the fewest open tabs, or of any
engine without it. A Browser API holding no browser of that engine refuses it.
`/browsers/<name>/…` and `X-Browser-Id` pin one browser as before.

Profiles move only between browsers of one engine; cookies move across
(`cookies import` says how many it couldn't take as `dropped`); there is no
cookies export, so save them from the old browser with Playwright
(`contexts[0].storage_state(path="cookies.json")`) and `livellm browser
cookies import NEW cookies.json`. Scripts written for a CDP address can't
drive a Camoufox browser.

An app and its databases in one step, linked:

```sh
cat > shop.json <<'JSON'
{
  "apps": [{
    "id": "shop-web", "stack": "shop", "hostname": "web", "image": "ghcr.io/acme/shop:1.4",
    "ports": [{ "name": "http", "port": 3000 }],
    "secretEnv": [{ "name": "STRIPE_KEY", "value": "sk_live_…" }],
    "databases": [
      { "id": "shop-db",    "env": { "DATABASE_URL": "url" } },
      { "id": "shop-cache", "env": { "REDIS_HOST": "host", "REDIS_PASSWORD": "password" } }
    ]
  }],
  "databases": [
    { "id": "shop-db",    "engine": "postgres", "storageSize": "10Gi",
      "backup": { "enabled": true, "mode": "daily", "keepDays": 7 } },
    { "id": "shop-cache", "engine": "redis", "storageSize": "1Gi" }
  ]
}
JSON
livellm create apps -f shop.json                  # all of it or none; the passwords are made for you
livellm ls                                        # shop-web lists its databases, each database usedBy
livellm template save shop --from shop-web        # the whole app, its databases and links; no secrets
livellm create --template shop --id shop-2 --secret-env STRIPE_KEY=STRIPE_KEY
livellm rm shop-2-web --with-databases            # the app, and the databases made with it
```

Add a service to an app that is already there with `--join`: the new
services take its stack, and an app on its own gets a stack named after itself
(it keeps its name inside it, and restarts once as it joins):

```sh
echo '[{"id": "nextcloud-cache", "hostname": "cache", "image": "redis:7"}]' > cache.json
livellm create apps -f cache.json --join nextcloud  # nextcloud reaches it as cache, it reaches nextcloud as nextcloud
```

A link puts a database's connection details into the app's environment:
`host`, `port`, `database`, `username`, `password` or `url` (a Redis database
has no `database` or `username`). The password and the URL are read from the
database's own login, never written where anyone can read them, and the app
starts once its databases are up. Links work on an existing app too:
`set shop-web -f` with `{"pod": {"databases": [...]}}` (the list you send is
the whole list). A create from a template names the secrets it still needs,
and the `--secret` flags that give them.

Backups work the same way for machines and databases:

```sh
livellm backups db                              # how it's backed up, and what's kept
livellm backup db                               # one now
livellm restore db BACKUP --as db-copy          # into a NEW database; db keeps running
livellm restore db BACKUP --as db-copy --at 2026-09-25T14:05:00Z   # continuous backups: to the minute
livellm restore box BACKUP                      # a machine goes back in place (stop it first)
```

The restored database keeps the original's login name and gets a new
password: from the variable named by `--password-env`, or made up and shown
once.

Where it runs: every resource is automatic unless you say otherwise, and
LiveLLM picks the host. `livellm hosts` lists the hosts, their regions and
their free room; a location is taken only on a host that is `ready` and
`schedulable` (it takes new resources). Settings may carry `"placement": {"strategy": "region",
"region": "<region>"}` for any host in a region, or `{"strategy": "host",
"host": "<id>"}` to pin one host; `restore … --as NEW`, `browser-api create`
and `create --template` take `--host H` or `--region R`. A template keeps where
its resources ran; `create --template … --automatic` (or `--host`/`--region`)
puts everything it makes somewhere else instead. Changing it restarts the
resource there, and `set ID -f` with `{"pod": {"placement": null}}` (the resource's own
block) makes it automatic again. A resource pinned to a host waits for that host while it
is down, and all copies of a database pinned to a host run on that host.

```sh
livellm hosts                                   # ids and regions to choose from
echo '{"id": "web", "image": "nginx", "placement": {"strategy": "region", "region": "REGION"}}' > web.json
livellm create pod -f web.json
livellm restore db BACKUP --as db-copy --host HOST
livellm create --template shop --id shop-2 --automatic   # automatic, whatever the template says
```

For scripts that run with nobody there, with `LIVELLM_API_KEY` set:

```sh
livellm build web --wait                        # exits 0 once the new build is live, non-zero if it failed
livellm template save small-box --from box      # a resource's settings, never its logins
livellm create --template small-box --id box2 --secret credentials.username=me --secret-env credentials.password=BOX_PASSWORD
livellm activity --object web --limit 20        # what happened to web, newest first
livellm monitoring                              # up or down, uptime, use, open alerts
livellm rdp win-lab -o win-lab.rdp              # a Windows machine's Remote Desktop file
livellm screenshot win-lab -o now.jpg           # a picture of its screen
livellm keys set -f ~/.ssh/id_ed25519.pub       # the workspace's SSH keys, replaced
livellm api-keys create ci                      # a new key; its secret is shown once
livellm plan                                    # the plan and what the workspace uses of it
```

An API key changes the plan (`plan set`, `plan metered on|off`) only when a
person gave it the billing permission on the console's Keys page.

## Inside the workspace

Resources in a workspace can't reach each other unless the user allows it (a
Composable App counts as one resource). Each one says who may connect to it:
nothing (where every new resource starts), the resources it names, or the
whole workspace (`"*"`, also resources made later).

```sh
livellm reach nextcloud-db                      # its setting, what reaches it anyway, its inside addresses
livellm reach nextcloud-db --from nextcloud     # let nextcloud (its whole Composable App) reach it
livellm reach shared-cache --from '*'           # the whole workspace
livellm reach nextcloud-db --none               # nothing else in the workspace
livellm create pod -f api.json --reachable-from web,worker
livellm create apps -f shop.json --reachable-from edge   # one setting for the whole app
```

Whatever the setting, a resource is reached by its own parts, the other
services of its Composable App, the apps that link it (`databases`) or wait for
it (`dependsOn`), and, a browser, the Browser API that drives it; `reach`
lists them under `alsoFrom` (`same app`, `links it`, `waits for it`, `drives
it`, with `through`: what may reach the browser through that Browser API, whose
inside address takes no key). Public addresses keep their own settings.
`livellm connect ID` prints the API's own `inside` block. A resource made
before this came in is reached from the whole workspace, as before, until it is
changed.

Before you let a resource reach another (`--from`, `--reachable-from`, a
database link, `dependsOn`, or a browser put in a Browser API), ask the user
and wait for their agreement, unless you created both or the one reached
already lets the whole workspace in. Letting the whole workspace in always
needs their agreement. An API key or an agent also needs the Network
permission for this, which only a person turns on (on the console's Keys page
for a key, the Agents page for an agent); without it the answer is a 403 with
`code: network_permission`, and `livellm` says what to do next. Narrowing or
closing needs nothing.

`monitoring` prints every resource (up or down, since when, uptime over 24
hours and 7 days, processor, memory and disk use) and the alerts: open ones
(no `resolvedAt`) first, then the last 7 days' resolved ones. `emailAlerts`
says whether the workspace owner is emailed when an alert opens and when it is
over; switch it on the console's Monitoring page. The fields are described at
[docs.live-llm.com/docs/monitoring](https://docs.live-llm.com/docs/monitoring).

`login` asks for full access by default: you are the person who owns the
workspace. `--access use` or `--access create` narrows it, and the console
shows what is being asked for before you press Allow.

Without `--wait`, `login` prints the link as JSON and returns at once, which
suits an agent that can't sit and wait: give the person the link, and once they
have pressed Allow, run `login` again to finish the same sign-in (it waits up to
a minute, then says it is still waiting). A link that ran out or was denied is
replaced by a new one. The started sign-in is kept next to the credentials, in
`credentials.pending.json`, until then.

It is one binary with no dependencies beyond Go's standard library, and it
talks to the same public API as everything else. `LIVELLM_API_KEY` works
instead of signing in, for anything unattended; `LIVELLM_API_URL` points it at
a self-hosted LiveLLM.

Binaries for Linux, macOS and Windows are on the
[releases page](https://github.com/qalby-tech/livellm_cloud_cli/releases).

An AI assistant drives the same API through the
[LiveLLM skill](https://github.com/qalby-tech/livellm_cloud_skills) — this is
the same platform for a person at a keyboard.
