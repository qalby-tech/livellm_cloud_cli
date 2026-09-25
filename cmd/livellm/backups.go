package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"strings"
	"time"
)

// Backups of machines and databases: one family of paths for both,
// /v1/workloads/{id}/backups. A machine restores in place (it has to be
// stopped); a database restores into a NEW database, and the one it came
// from keeps running untouched.

func backupsPath(id string) string {
	return "/v1/workloads/" + url.PathEscape(id) + "/backups"
}

// workloadType is the resource's type, read from the workspace, so a command
// can say what it can't do before sending anything.
func workloadType(id string) (string, error) {
	var ws struct {
		Spec struct {
			Workloads []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"workloads"`
		} `json:"spec"`
	}
	if err := call("GET", "/v1/workspace", nil, &ws); err != nil {
		return "", err
	}
	for _, w := range ws.Spec.Workloads {
		if w.ID == id {
			return w.Type, nil
		}
	}
	return "", fmt.Errorf("there is nothing called %q here — try livellm ls", id)
}

func isMachine(t string) bool { return strings.HasPrefix(t, "vm-") }

func cmdBackups(args []string) error {
	if len(args) > 0 && (args[0] == "rm" || args[0] == "describe") {
		return backupChange(args[0], args[1:])
	}
	id, _, err := needArg(args, "machine or database")
	if err != nil {
		return err
	}
	var out map[string]any
	if err := call("GET", backupsPath(id), nil, &out); err != nil {
		return err
	}
	return print(out)
}

// backupChange deletes a machine's backup, or sets its description. A
// database's backups are named by when they were taken and leave the store
// on their own after its keep days; the API says so.
func backupChange(verb string, args []string) error {
	id, rest, err := needArg(args, "machine")
	if err != nil {
		return err
	}
	backup, rest, err := needArg(rest, "backup")
	if err != nil {
		return fmt.Errorf("which backup? livellm backups %s lists them", id)
	}
	path := backupsPath(id) + "/" + url.PathEscape(backup)
	if verb == "describe" {
		if len(rest) == 0 {
			return fmt.Errorf("describe it how? livellm backups describe %s %s \"before the upgrade\"", id, backup)
		}
		var out map[string]any
		if err := call("PATCH", path, map[string]any{"description": rest[0]}, &out); err != nil {
			return err
		}
		return print(map[string]any{"described": backup, "of": id})
	}
	fs := flag.NewFlagSet("backups rm", flag.ExitOnError)
	yes := fs.Bool("y", false, "don't ask")
	_ = fs.Parse(rest)
	if !*yes && !confirm(fmt.Sprintf("Delete the backup %s of %s? This can't be undone.", backup, id)) {
		return fmt.Errorf("nothing was deleted")
	}
	if err := call("DELETE", path, nil, nil); err != nil {
		return err
	}
	return print(map[string]any{"deleted": backup, "of": id})
}

// cmdBackup takes a backup now. A machine's is live unless --clean asks for
// one taken with the machine stopped; a database's is a full copy.
func cmdBackup(args []string) error {
	id, rest, err := needArg(args, "machine or database")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	clean := fs.Bool("clean", false, "a machine: back up with it stopped (it must already be stopped)")
	name := fs.String("name", "", "a machine: the backup's name")
	desc := fs.String("description", "", "a machine: a note kept with the backup")
	_ = fs.Parse(rest)
	body := map[string]any{}
	if *clean {
		body["mode"] = "clean"
	}
	if *name != "" {
		body["name"] = *name
	}
	if *desc != "" {
		body["description"] = *desc
	}
	var out map[string]any
	if err := call("POST", backupsPath(id), body, &out); err != nil {
		return err
	}
	if len(out) == 0 {
		out = map[string]any{"backingUp": id}
	}
	return print(out)
}

// restoreBody is what a database's restore sends: the new database's id and
// password, and a moment when it restores to a minute rather than to the
// end of the backup.
func restoreBody(as, at, password string) map[string]any {
	body := map[string]any{"id": as, "credentials": map[string]any{"password": password}}
	if at != "" {
		body["pointInTime"] = at
	}
	return body
}

// newPassword is a strong password for a restored database, shown once.
func newPassword() (string, error) {
	const letters = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 24)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			return "", err
		}
		b[i] = letters[n.Int64()]
	}
	return string(b), nil
}

func cmdRestore(args []string) error {
	id, rest, err := needArg(args, "machine or database")
	if err != nil {
		return err
	}
	backup, rest, err := needArg(rest, "backup")
	if err != nil {
		return fmt.Errorf("which backup? livellm backups %s lists them", id)
	}
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	as := fs.String("as", "", "a database: the id of the new database to restore into")
	at := fs.String("at", "", "a database with continuous backups: the moment to restore to (RFC 3339, e.g. 2026-09-25T14:05:00Z)")
	passwordEnv := fs.String("password-env", "", "a database: the environment variable holding the new database's password (one is made up and shown once if left out)")
	yes := fs.Bool("y", false, "don't ask")
	_ = fs.Parse(rest)
	if *at != "" {
		if _, err := time.Parse(time.RFC3339, *at); err != nil {
			return fmt.Errorf("--at %q isn't a time like 2026-09-25T14:05:00Z", *at)
		}
	}
	t, err := workloadType(id)
	if err != nil {
		return err
	}
	switch {
	case t == "storage":
		if *as == "" {
			return fmt.Errorf("a database restores into a new one: pass --as NEW-ID (%s keeps running as it is)", id)
		}
	case isMachine(t):
		if *as != "" || *at != "" || *passwordEnv != "" {
			return fmt.Errorf("a machine restores in place: drop --as, --at and --password-env")
		}
		if !*yes && !confirm(fmt.Sprintf("Put %s's disk back to backup %s? What was written since is lost.", id, backup)) {
			return fmt.Errorf("nothing was restored")
		}
	default:
		return fmt.Errorf("%q has no backups: only machines and databases do", id)
	}
	path := backupsPath(id) + "/" + url.PathEscape(backup) + "/restore"
	var out map[string]any
	if isMachine(t) {
		if err := call("POST", path, nil, &out); err != nil {
			return err
		}
		if len(out) == 0 {
			out = map[string]any{"restoring": backup, "to": id}
		}
		return print(out)
	}
	password, made := "", false
	if *passwordEnv != "" {
		password = os.Getenv(*passwordEnv)
		if password == "" {
			return fmt.Errorf("%s is empty: put the new database's password in it", *passwordEnv)
		}
	} else {
		if password, err = newPassword(); err != nil {
			return err
		}
		made = true
	}
	if err := call("POST", path, restoreBody(*as, *at, password), &out); err != nil {
		return err
	}
	if out == nil {
		out = map[string]any{}
	}
	if len(out) == 0 {
		out = map[string]any{"id": *as, "from": id, "backup": backup}
	}
	if made {
		out["password"] = password
		out["note"] = "the new database's password, shown this once"
	}
	return print(out)
}
