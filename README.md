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
