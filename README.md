# livellm

The command line for LiveLLM Cloud: your machines, browsers, apps and databases
from a terminal.

```sh
go install github.com/qalby-tech/livellm_cloud_skills/cli@latest

livellm login          # opens a link; press Allow in the console
livellm ls             # what you have
livellm logs web       # why something isn't working
livellm connect shop   # how to reach it
```

`login` asks for full access by default: you are the person who owns the
workspace. `--access use` or `--access create` narrows it, and the console
shows what is being asked for before you press Allow.

It is one binary with no dependencies beyond Go's standard library, and it
talks to the same public API as everything else. `LIVELLM_API_KEY` works
instead of signing in, for anything unattended; `LIVELLM_API_URL` points it at
a self-hosted LiveLLM.

The agent skill in this repo drives the same API — this is the same platform
for a person at a keyboard.
