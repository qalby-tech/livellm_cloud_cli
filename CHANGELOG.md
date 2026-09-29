# Changelog

Releasing: add an entry below, then tag `vX.Y.Z`. CI builds a binary for each
platform and attaches them to the GitHub release.

## Unreleased

- `unshare` with a sign-in closes the screen links that sign-in made; another
  agent's, or one made in the console, is refused (403) unless the sign-in
  may change anything (full access). With an API key nothing changes.
- A Desktop App is one desktop (breaking): `--desktop N` is gone from
  `connect`, `exec`, `share` and `screenshot`, and `create desktop` takes no
  `replicas` (more than 1 is refused). Make a Desktop App for each desktop
  you need.
- Apps and their databases: `create apps -f FILE` takes
  `{"apps": [...], "databases": [...]}` and makes all of it in one step (a
  database's password may be left out: the platform makes one); each app
  links its databases in `databases: [{id, env: {VAR: host|port|database|
  username|password|url}}]`, the same in `create pod`. The answer lists the
  `databases` made. `ls` shows an app's links (`databases`, as its settings
  hold them) and the apps a database serves (`usedBy`).
- `rm ID --with-databases` deletes an app together with the databases made
  with it that no other app uses, and prints which went and which stayed
  (`databases: {deleted, kept}`); `--force` deletes what another app's
  settings still name. `rm` waits up to three minutes for the answer (a
  delete clears away what belonged to the resource first), and when the
  answer is lost on the way it looks at the workspace: gone is deleted.
- `template save NAME --from ID` has LiveLLM read the resource, as the console
  saves it: an app keeps its plain env values and the names of its secrets;
  an app of a Composable App (a stack, or made with databases) saves the whole
  app as kind `stack`, with its databases and links. `--kind stack -f FILE`
  saves one from a file.
- `create --template T --id NEW` creates through the template: a Composable
  App's template makes every service and its databases (with passwords made
  for them), already linked, `--id` being the app's name. The secrets the
  template leaves out come from `--secret PATH=VALUE` or `--secret-env
  PATH=VAR` (a bare name is a secret env value; in a Composable App it goes to
  every service with that secret), or from `-f FILE` with `secretEnv`,
  `imagePassword`, `gitToken`, `portPasswords`, `credentials` or `services`.
  A refusal names the missing ones as the flags to add. **Changed:** `-f` no
  longer changes a template's settings (`livellm set NEW -f` after does), and
  a Composable App's answer lists `created` ids.
- Older versions: 0.2.0 and before build the resource from a template's
  settings themselves. A template saved now keeps its secrets as names only
  (and a Composable App's as kind `stack`), so `create --template` there is
  refused (`value required`, or an unknown kind). Update to create from one;
  templates saved before still work there.
- `exec` waits for a long command: a command still going when the call
  answers (after 55 s) keeps going on the machine, and `exec` looks at it
  again until it ends, up to its `--timeout` (the platform stops it then, exit
  code 124) and two minutes more. The answer is the platform's new shape:
  `done`, `exitCode`, `output` (stdout and stderr together), `truncated`,
  `durationMs` and `runId`.
- `exec ID "COMMAND"` runs on Windows machines too, in PowerShell (bash on
  Linux machines and Desktop Apps); the help says so.
- `login` signs in in two calls: the first prints the link as JSON
  (`signedIn: false`, `link`, `code`, `expiresAt`) and returns at once; once
  the link is allowed, `login` again finishes the same sign-in, waiting up to a
  minute and then saying it is still waiting (exit 4). A link that ran out or
  was denied is replaced by a new one. The started sign-in is kept in
  `credentials.pending.json` next to the credentials. `login --wait` is the
  one-call form, for a person at a terminal.

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
