# livellm

The command line for LiveLLM Cloud: your machines, browsers, apps and databases
from a terminal.

```sh
go install github.com/qalby-tech/livellm_cloud_cli/cmd/livellm@latest

livellm login          # opens a link; press Allow in the console
livellm ls             # what you have
livellm logs web       # why something isn't working
livellm connect shop   # how to reach it
livellm exec box "uname -a"   # run a command on a Linux machine
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

Backups work the same way for machines and databases:

```sh
livellm backups db                              # what's kept, newest first
livellm backup db                               # one now
livellm restore db BACKUP --as db-copy          # into a NEW database; db keeps running
livellm restore db BACKUP --as db-copy --at 2026-09-25T14:05:00Z   # continuous backups: to the minute
livellm restore box BACKUP                      # a machine goes back in place (stop it first)
```

`login` asks for full access by default: you are the person who owns the
workspace. `--access use` or `--access create` narrows it, and the console
shows what is being asked for before you press Allow.

It is one binary with no dependencies beyond Go's standard library, and it
talks to the same public API as everything else. `LIVELLM_API_KEY` works
instead of signing in, for anything unattended; `LIVELLM_API_URL` points it at
a self-hosted LiveLLM.

Binaries for Linux, macOS and Windows are on the
[releases page](https://github.com/qalby-tech/livellm_cloud_cli/releases).

An AI assistant drives the same API through the
[LiveLLM skill](https://github.com/qalby-tech/livellm_cloud_skills) — this is
the same platform for a person at a keyboard.
