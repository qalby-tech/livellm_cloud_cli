# Changelog

Releasing: add an entry below, then tag `vX.Y.Z`. CI builds a binary for each
platform and attaches them to the GitHub release.

## 0.2.0

- `build ID --wait`: waits until the build it started is live and exits 0, or
  exits non-zero with the end of the build's log when it fails (`--timeout`,
  30 minutes by default). An earlier build's answer is never taken for it.
- `templates`, `template save NAME --from ID` (a resource's settings, without
  its logins, env values or pull credential, as the console saves them) or
  `--kind TYPE -f FILE`, `template show T`, `template rm T`, and
  `create --template T --id NEW [-f FILE]`: a resource from a saved template,
  with the file adding what is its own (a login, env values). A template is
  named by its id or its name. A Browser API saves as a template too, as the
  console saves it; a database's template leaves out what it was restored
  from.
- `activity [--actor you|platform|all] [--object ID] [--limit N] [--before
  EVENT]`: what happened in the workspace, newest first.
- `monitoring`: every resource up or down, its uptime and use, and the
  alerts. `monitoring ID [--range 15m|1h|6h|24h|7d]`: one machine in detail
  (the id may come before or after `--range`).
- `rdp ID [--ttl 8h] [-o FILE]`: a Windows or Ubuntu desktop machine's Remote
  Desktop file, saved as `ID.rdp`.
- `screenshot ID [--desktop N] [--width PX] [-o FILE]`: a picture of a
  machine's, a browser's or a desktop's screen (JPEG).
- `api-keys` (`ls`, `create NAME`, `set ID --permissions billing|none`,
  `rm ID`): the workspace's API keys. A new key's secret is printed once.
  Permissions are given by a person in the console; the API says so when a
  key or a sign-in asks.
- `keys set -f FILE`: replace the workspace's SSH keys, from a JSON list or a
  file of public key lines. It takes an API key; an agent's sign-in is
  refused.
- `plan`, `plan catalog`, `plan set PLAN` and `plan metered on|off`: the plan,
  what the workspace uses of it, and changing it (an API key needs the
  billing permission).
- `reservations`: the machines agents are holding.
- `wait ID [--timeout 15m]`: until the resource is ready; non-zero when it
  failed or isn't ready in time, so a script can stop there. A stopped
  resource is said at once (`livellm start ID` first); one just started
  is waited for.
- `install ID`: where a new machine stands on its way to its first boot
  (a Windows machine takes minutes).
- `database ID`: a database's instances as they are now: role, ready, use and
  disk.
- `backups describe ID BACKUP TEXT` and `backups rm ID BACKUP`: note what a
  machine's backup is, or delete it (asks first; `-y` doesn't).
- `invoices [ID]`: the monthly invoices, or one.
- `agents` and `agents rm ID`: the agents signed in to the workspace, and
  signing one out.

- `backups ID`, `backup ID` and `restore ID BACKUP`: backups of machines and
  databases. `backup` takes one now (a machine's is live; `--clean` takes it
  with the machine stopped, `--name` names it). A database restores into a new
  database, `--as NEW`, and keeps running as it is; with continuous backups,
  `--at TIME` restores to that minute. The new database keeps the login name
  and takes a new password from the variable named by `--password-env`, or
  one is made up and shown once. A machine is put back in place and has
  to be stopped first; the command asks before it does that (`-y` doesn't).
  Restore refuses a database without `--as`, and `--as` or `--at` for a
  machine, before sending anything.
- `restart ID` restarts a database too.
- `browser-api create NAME --browsers a,b` (or `--all` for every browser in
  the workspace, `--remote id=wss://…` for a browser running elsewhere): one
  address over several browsers. `browser-api show NAME` lists the browsers
  it drives and how they are doing; `browser-api add NAME BROWSER` and
  `browser-api remove NAME BROWSER` change which browsers it drives, one at a
  time, without rewriting the rest. `create browser-api -f FILE` works too.
  `--remote-auth id=ENV_VAR` gives a remote browser its login header from an
  environment variable, so it never appears on the command line.
- `set ID -f FILE`: change some of a resource's settings. The file holds only
  what changes; everything else stays as it is.
- `stop` and `start` change only whether the resource runs. They used to
  write the whole resource back, which could undo a change made meanwhile in
  the console.
- `stop ID` and `start ID`: stop an app or a machine without deleting it
  (its disks are kept and only they are billed), and start it again. The
  resource is written back exactly as it was, with only its running state
  changed. A browser or a database can't be stopped; the command says so
  instead of writing anything.
- `ls` labels raw ports: `tcp host:port` or `udp host:port`, next to HTTPS
  addresses and `ssh host:port`.
- `connect` gives an app's raw ports their `address` (`host:port`) and
  `protocol`.
- The README shows an app with raw TCP and UDP ports and volumes.

## 0.1.5

- `exec ID "command"`: run a command on a Linux machine or a Desktop App's
  desktop and get its output and exit code (`--session`, `--timeout`,
  `--desktop`).
- `share ID [--control] [--for 1h|24h|7d]`: a link that opens a screen in any
  browser, to watch or to use. `shares ID` lists the open ones, `unshare ID
  LINK` closes one.
- `release ID`: let go of a machine held for work on it.
- `connect --desktop N` for Desktop Apps, and `--screen-width` / `--format`
  for the computer tool's screenshots.

## 0.1.4

- `go install github.com/qalby-tech/livellm_cloud_cli/cmd/livellm@latest`
  installs a binary called `livellm` (the module path alone installed one
  called `livellm_cloud_cli`).

## 0.1.3

- The command line has its own repository. Until now it lived inside the
  skills repository (`livellm_cloud_skills/cli`, tags `cli-v0.1.0`–`cli-v0.1.2`);
  the code is the same.

## 0.1.2

- `create apps -f stack.json`: several apps at once, all or nothing.

## 0.1.1

- `ls` says why it couldn't read how things are running, instead of showing
  "unknown" for everything without a word.
- The README says why `login` asks for full access by default.

## 0.1.0

- `livellm`: sign in, list, status, logs, connect, keys, create, rm, restart,
  build, builds, deploy. One binary, no dependencies, five platforms.
