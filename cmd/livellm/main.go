// livellm — the command line for LiveLLM Cloud.
//
// One binary, the standard library only, and the same public API the console
// and the agent skill use. Signing in is the device flow: the command prints a
// link, you press Allow in the console, and the sign-in is saved for next time.
package main

import (
	"fmt"
	"os"
	"strings"
)

const usage = `livellm — your machines, browsers, apps and databases.

  livellm login [--access use|create|full]   sign in (prints a link to allow)
  livellm logout                             end this sign-in
  livellm whoami                             workspace, plan and usage

  livellm ls [--type TYPE]                   everything, with its state
  livellm status [ID]                        how things are running right now
  livellm wait ID [--timeout 15m]            until it is ready (exits non-zero if it isn't)
  livellm install ID                         a new machine's way to its first boot
  livellm database ID                        a database's instances, live
  livellm logs ID [--lines N]                recent logs, and restarts
  livellm connect ID [--tool TOOL]           how to reach it
          [--desktop N] [--screen-width PX] [--format png|jpeg]
  livellm exec ID "COMMAND"                  run a command on a Linux machine
          [--session S] [--timeout N] [--desktop N]
  livellm share ID [--control] [--for 1h|24h|7d] [--desktop N]
                                             a link to watch or use a screen
  livellm shares ID                          a screen's open links
  livellm unshare ID LINK                    close a screen link now
  livellm release ID                         let go of a machine held for work on it
  livellm keys                               the workspace's SSH keys
  livellm keys set -f FILE                   replace them (with an API key; a sign-in can't)
  livellm rdp ID [--ttl 8h] [-o FILE]        a Windows or Ubuntu desktop machine's Remote Desktop file
  livellm screenshot ID [--desktop N] [-o FILE]
                                             a picture of its screen (JPEG)
  livellm reservations                       machines agents are holding

  livellm create TYPE -f FILE                create a resource from a JSON file
  livellm create --template T --id NEW [-f FILE]
                                             create one from a saved template
  livellm rm ID                              delete one (asks first)
  livellm restart ID                         restart one
  livellm stop ID                            stop one; its disks are kept
  livellm start ID                           start a stopped one again
  livellm set ID -f FILE                     change some settings (only what the file holds)
  livellm build ID [--wait] [--timeout 30m]  build an app from its repository
  livellm builds ID                          an app's builds
  livellm deploy ID BUILD                    run an earlier build again

  livellm backups ID                         a machine's or a database's backups
  livellm backup ID                          back up now
          [--clean] [--name N]               (a machine: stopped first; its name)
  livellm restore ID BACKUP                  a machine: put its disk back (stop it first)
  livellm restore ID BACKUP --as NEW [--at TIME] [--password-env VAR]
                                             a database: restore into a new database
  livellm backups describe ID BACKUP TEXT    a machine's backup: note what it is
  livellm backups rm ID BACKUP               a machine's backup: delete it (asks first)

  livellm templates                          saved templates
  livellm template save NAME --from ID       save a resource's settings (never its logins)
  livellm template save NAME --kind TYPE -f FILE
  livellm template show T | template rm T

  livellm activity [--actor you|platform] [--object ID] [--limit N] [--before EVENT]
                                             what happened, newest first
  livellm monitoring [ID] [--range 24h]      up or down, uptime, use and alerts
  livellm plan                               the plan, and what the workspace uses of it
  livellm plan catalog | plan set PLAN | plan metered on|off
                                             change it (a key needs the billing permission)
  livellm invoices [ID]                      monthly invoices, or one
  livellm agents [ls] | agents rm ID         agents signed in; sign one out
  livellm api-keys [ls]                      the workspace's API keys
  livellm api-keys create NAME               a new key; its secret is shown once
  livellm api-keys set ID --permissions billing|none
  livellm api-keys rm ID                     revoke one
                                             (permissions are given by a person, in the console)

  livellm browser-api create NAME --browsers a,b | --all [--remote id=wss://…]
                                             one address over several browsers
      [--remote-auth id=ENV_VAR]             a remote browser's login, from a variable
  livellm browser-api show NAME              the browsers it drives, and their tabs
  livellm browser-api add NAME BROWSER       have it drive one more browser
  livellm browser-api remove NAME BROWSER    take a browser out of it

Environment:
  LIVELLM_API_KEY    use a workspace key instead of signing in
  LIVELLM_API_URL    a self-hosted LiveLLM (default https://api.live-llm.com)

Every command prints JSON unless it says otherwise, so it pipes into jq.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "login":
		err = cmdLogin(args)
	case "logout":
		err = cmdLogout(args)
	case "whoami":
		err = cmdWhoami(args)
	case "ls", "list":
		err = cmdList(args)
	case "status":
		err = cmdStatus(args)
	case "logs":
		err = cmdLogs(args)
	case "connect":
		err = cmdConnect(args)
	case "exec":
		err = cmdExec(args)
	case "share":
		err = cmdShare(args)
	case "shares":
		err = cmdShares(args)
	case "unshare":
		err = cmdUnshare(args)
	case "release":
		err = cmdRelease(args)
	case "keys", "ssh-keys":
		err = cmdKeys(args)
	case "rdp":
		err = cmdRDP(args)
	case "screenshot":
		err = cmdScreenshot(args)
	case "reservations":
		err = cmdReservations(args)
	case "wait":
		err = cmdWait(args)
	case "database", "db":
		err = cmdDatabase(args)
	case "install":
		err = cmdInstall(args)
	case "agents":
		err = cmdAgents(args)
	case "invoices", "invoice":
		err = cmdInvoices(args)
	case "templates":
		err = cmdTemplates(args)
	case "template":
		err = cmdTemplate(args)
	case "activity":
		err = cmdActivity(args)
	case "monitoring", "monitor":
		err = cmdMonitoring(args)
	case "plan", "billing":
		err = cmdPlan(args)
	case "api-keys", "api-key":
		err = cmdAPIKeys(args)
	case "create":
		err = cmdCreate(args)
	case "rm", "delete":
		err = cmdRemove(args)
	case "restart":
		err = cmdRestart(args)
	case "stop":
		err = cmdStop(args)
	case "start":
		err = cmdStart(args)
	case "set":
		err = cmdSet(args)
	case "browser-api", "browser-apis":
		err = cmdBrowserAPI(args)
	case "build":
		err = cmdBuild(args)
	case "builds":
		err = cmdBuilds(args)
	case "deploy":
		err = cmdDeploy(args)
	case "backups":
		err = cmdBackups(args)
	case "backup":
		err = cmdBackup(args)
	case "restore":
		err = cmdRestore(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	case "version", "--version":
		fmt.Println(version)
		return
	default:
		fmt.Fprintf(os.Stderr, "livellm: there is no %q command\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "livellm: "+err.Error())
		os.Exit(exitCode(err))
	}
}

// version is stamped at build time; a plain build says so.
var version = "dev"

// exitCode separates "you have to do something" from "it went wrong", so a
// script can tell them apart.
func exitCode(err error) int {
	var p *problem
	if asProblem(err, &p) {
		switch {
		case p.Status == 401 || p.Status == 402 || p.Status == 403:
			return 2
		case p.Status == 409:
			return 4
		}
	}
	return 1
}

func needArg(args []string, what string) (string, []string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", args, fmt.Errorf("which %s? pass its id", what)
	}
	return args[0], args[1:], nil
}
