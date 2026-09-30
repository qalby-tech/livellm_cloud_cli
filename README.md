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
