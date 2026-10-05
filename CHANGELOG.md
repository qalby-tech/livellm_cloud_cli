# Changelog

Releasing: add an entry below, then tag `vX.Y.Z`. CI builds a binary for each
platform and attaches them to the GitHub release.

## 0.7.0

- Inside the workspace, resources reach each other only when allowed: each
  one has `reachableFrom` (nothing, the ids it names, or `"*"` for the whole
  workspace). Every resource starts with nothing, those made before included.
  A Composable App is one resource: its services reach each other.
- A database has no `reachableFrom`: it is reached only by what links it (an
  app's `databases` or `dependsOn`, with its whole Composable App; a machine's
  or a Desktop App's `databases`). `--reachable-from` on `create storage`, a
  `reachableFrom` in its `-f` body or in `create apps`' databases, and `reach
  DB --from` are refused before anything is sent, with the API's words;
  `--reachable-from ''` on a database is left out (it has nothing anyway), and
  so is a `reachableFrom: []` in its body. `reach DB` shows what links it and
  its inside addresses (only when something does), and says the whole
  workspace may still reach it while the platform hasn't closed the workspace
  (another resource has no setting yet).
- `livellm link ID DB... [--remove]`: reach-only links. The app (with its
  whole Composable App), the machine or the Desktop App reaches the database;
  nothing goes into its environment and nothing restarts. It reads the
  resource first and sends its whole list (`pod`, `vm` or `desktop`
  `databases`), keeping the links it had, variables and all; `--remove` says
  when a removed link's variables leave the app (it restarts), and what still
  reaches the database (a wait, another service of the Composable App that
  links it or waits for it). A Composable App's name links from its first
  service; `--remove` takes the service's id.
- `create vm-…|desktop --database DB` (repeatable) links databases reach only,
  next to `databases` in the `-f` body; a machine's or Desktop App's link with
  variables is refused as the API does. `ls` lists machine and Desktop App
  links. `rm --force` names what blocks deleting a database: an app, a machine
  or a Desktop App that links it. `rm --with-databases` keeps a database that a
  machine or a Desktop App uses.
- `livellm reach ID` shows the setting (`null`, "not set yet", for a resource
  the platform hasn't given one: never `"*"`), what reaches the resource
  whatever it says (`alsoFrom`: `same app`, `links it`, `waits for it`, `drives it`, with
  `through` for a browser driven by a Browser API, and `via` for the services
  of a Composable App one of whose services links it) and its inside addresses
  (`inStack` on every port of a Composable App's service).
  It reads the workspace only: it never holds a machine or hands out a token.
- `livellm reach ID --from a,b | --from '*' | --none` changes it (a PATCH of
  `reachableFrom`; a Composable App takes it on every service). Names that
  aren't there and words left after the list are refused before anything is
  sent.
- `--reachable-from a,b|'*'|''` on `create` (every type but a database,
  `create browser --id` and `-f` bodies), on each app of `create apps` (its databases are
  reached by the apps linking them) and on `browser-api create`; `''` is
  nothing (there is no `none` keyword: it may be a resource's name). Left out,
  nothing is sent: the same request as before, and the resource starts closed.
  With `create apps --join APP`, the new services take the app's setting: a
  different `--reachable-from` is refused (change the whole app with `reach`),
  and so is any when the app has no setting yet.
  A settings file that names another `reachableFrom` is refused, not
  overridden, and so are words left after the list (`--reachable-from web,
  box`). `--template` takes none.
- A refusal with `code: network_permission` (403, exit 2) says to ask the user
  first, and where a person turns on Network: the Keys page for a key, the
  Agents page for an agent. It covers a database link, `dependsOn` and a
  service added to a Composable App as well as `reachableFrom`; `reach` on a
  Composable App's service says a service added to it lets it in. A key or agent needs no permission to let one
  resource it made reach another it made, nor to narrow; letting the whole
  workspace in always needs Network.
- `connect` prints the API's `inside` block when it sends one, and a line on
  stderr saying who may reach the resource.
- `api-keys create|set --permissions` names `network` too.

## 0.6.1

- `api-keys create|set --permissions` names `billing` only. The proxies and
  profiles permissions are gone: changing a browser's proxies or moving its
  profile needs none (ask the user first). The old names are still sent as
  typed, with a word on stderr; the API takes them and keeps nothing, so
  livellm 0.5 and 0.6.0 keep working.
- `livellm help` and the README say to ask the user and wait for their
  agreement before changing a browser's proxies (`proxy remove` and a create or
  `set -f FILE` carrying proxy settings count too) or exporting, importing or
  copying its profile (`create browser --profile` too) or adding cookies.

## 0.6.0

- `livellm connect BROWSER` with no `--tool` gives the browser's automation
  address (as `--tool cdp`); before, the API refused it. `--tool view` gives the
  live view.
- Camoufox browsers (Firefox-based, driven with Playwright) next to Chrome:
  `livellm create browser --id NAME --engine chrome|camoufox`. The engine is
  fixed once made. Without `--engine`, or with `--engine chrome`, nothing
  changes: the same request as before. `--engine` goes with `create browser`
  only.
- One Browser API holds browsers of both engines: `livellm browser-api create`
  is unchanged (no `--engine`; `--all` is every browser in the workspace,
  either engine; remote browsers are Chrome). Its `POST /start_session` takes
  an optional `engine` (`{"engine":"camoufox"}`) to start the session on a
  browser of that engine; without it, the browser with the fewest open tabs.
- `livellm browser engines` lists the engines this platform offers (no
  sign-in needed).
- `connect` on a Camoufox browser prints the API's answer as it comes, with
  its `playwright` block (`url`, `headers`, `version`), and one line on stderr:
  `Playwright 1.62: firefox.connect(playwright.url, headers=playwright.headers)`.
- `ls` and `status` say `"engine": "camoufox"` for Camoufox browsers
  (`status` prints the API's answer, which says it); Chrome browsers and
  Browser APIs print as before.
- The refusals `engine_fixed`, `engine_unavailable`, `extensions_unsupported`,
  `engine_mismatch` (a profile copied between browsers of different engines)
  and `profile_engine` print the API's message and what to do next. Cookies
  move across engines by saving them with Playwright from the old browser
  (`contexts[0].storage_state(path="cookies.json")`) and `livellm browser
  cookies import NEW cookies.json`.
- `create browser --engine camoufox --profile FILE` with a Chrome profile:
  the browser is made, the profile is refused, and the next step is its
  cookies (importing the profile again would be refused the same way).
- `browser profile import --force` covers a profile from a newer Camoufox too.
- Earlier livellm versions print a Camoufox browser's `connect` answer as it
  comes (its `playwright` block included), without the hint. Scripts written
  for a CDP address (`cdp.url`) find none in a Camoufox answer and can't drive
  it.

## 0.5.0

- `livellm browser locale ID --locale ru-RU --timezone Europe/Moscow
  [--languages …] [--geolocation off|default|LAT,LON]`: a browser's language,
  time zone and location ('' clears one; the browser restarts, its profile
  stays). With no flags it shows them; `livellm browser locales` lists what
  it takes.
- `livellm browser proxy show|set|clear|remove|rotate ID`: proxies (http,
  https, socks5) with logins and mobile change-IP links, rotated off, per
  session or every N minutes, and by hand. `set` takes `--upstream
  NAME=URL` and the logins and links from variables: `--username-env
  NAME=VAR`, `--password-env [NAME=]VAR`, `--password-stdin`,
  `--change-ip-env NAME=VAR`, or `env:VAR` in `-f FILE`, never from the
  command line. A login or link already stored is kept when none is sent;
  `--no-login` / `--no-change-ip` drop them. Without `--upstream` or a list
  in the file, the proxies stay as they are (and `set` refuses when it can't
  read them); a proxy listed again under its name and host keeps its
  change-IP method and least time. `clear` goes direct and drops the
  proxies and their logins; `remove` takes the proxy settings out (the browser restarts).
- `livellm browser profile list|snapshot|restore|delete|export|import|copy`:
  profile snapshots, and a profile as a file (`.llcprofile`, or
  `.llcprofile.age` with a password). Export and import stream to and from
  disk with no time limit; an export cut short saves nothing, and without
  `-o` an export never replaces a file already there.
- `livellm browser cookies import ID FILE`: a JSON list of cookies, or a
  Playwright storage state.
- `livellm create browser --id NAME [--locale] [--timezone] [--profile FILE
  [--profile-password-env VAR]]`: with `--profile`, the new browser starts
  with that profile.
- `api-keys create|set --permissions` names `proxies` and `profiles` too
  (a person gives them, on the console's Keys page).

## 0.4.0

- `livellm hosts` (or `fleet`) lists where resources can run: each host's id,
  region and free room, as the API answers; a location is taken only on a
  host that is `ready` and `schedulable`.
- Where it runs: any resource's settings may carry `placement`
  (`{"strategy": "region", "region": R}` or `{"strategy": "host", "host": H}`)
  in `create TYPE -f`; `set ID -f` with `{"<block>": {"placement": null}}`
  makes it automatic again. Left out, it stays automatic.
- `restore ID BACKUP --as NEW` and `browser-api create` take `--host H` or
  `--region R` (one of them) for where the new resource runs; without them it
  is automatic. A machine restores in place and takes neither.
- `create --template T --id NEW` takes `--host H`, `--region R` or
  `--automatic` (or a `"placement"` in `-f`, for a Composable App's template
  too): where everything it makes runs, in place of the template's own. A
  template saved on a host that is gone can be used again without editing it.

## 0.3.0 (breaking)

- README: what `monitoring` prints, how to read open and resolved alerts,
  and the alert emails.
- `exec` gives a command 300 seconds by default (was 60), as the platform now
  does; `--timeout` still takes up to 600.
- `create apps -f FILE --join APP` (or `"join"` in the file) adds the apps
  to an app that is already there, in one step: they take its stack, and an
  app on its own gets a stack named after itself (it restarts once).
- `create --template T --secret portPasswords.<port>.<user>=…` keeps a
  username with dots (`alice.smith`, `a@b.com`) whole; before, it was split
  and the platform refused the request.
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
  one-call form, for a person at a terminal. Without `--access`, login again
  finishes the pending sign-in with the access it asked for (also one the
  skill started); only a different `--access` starts over.

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
