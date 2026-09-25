# Changelog

Releasing: add an entry below, then tag `vX.Y.Z`. CI builds a binary for each
platform and attaches them to the GitHub release.

## Unreleased

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
