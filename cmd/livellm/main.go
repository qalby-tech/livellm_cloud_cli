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
  livellm logs ID [--lines N]                recent logs, and restarts
  livellm connect ID [--tool TOOL]           how to reach it
  livellm keys                               the workspace's SSH keys

  livellm create TYPE -f FILE                create a resource from a JSON file
  livellm rm ID                              delete one (asks first)
  livellm restart ID                         restart one
  livellm build ID                           build an app from its repository
  livellm builds ID                          an app's builds
  livellm deploy ID BUILD                    run an earlier build again

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
	case "keys", "ssh-keys":
		err = cmdKeys(args)
	case "create":
		err = cmdCreate(args)
	case "rm", "delete":
		err = cmdRemove(args)
	case "restart":
		err = cmdRestart(args)
	case "build":
		err = cmdBuild(args)
	case "builds":
		err = cmdBuilds(args)
	case "deploy":
		err = cmdDeploy(args)
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
