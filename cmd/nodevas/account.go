package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/db"
)

// accountCmd is account recovery that only the server host can do:
//
//	nodevas account totp-reset [--project <dir>] <email-or-id-or-name>
//
// totp-reset turns two-factor authentication off for one account — the way
// back for somebody who lost both their authenticator and its recovery codes.
// It is deliberately not reachable over HTTP: whoever can run it already owns
// the machine the accounts live on. It writes an auth.totp.reset event to the
// audit trail. The account then signs in with its mailed passcode alone until
// its holder enrolls a new authenticator.
func accountCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		accountUsage(stderr)
		return 2
	}
	switch args[0] {
	case "totp-reset":
		if err := accountTOTPReset(args[1:], stdout, stderr); err != nil {
			if errors.Is(err, errAccountUsage) || errors.Is(err, flag.ErrHelp) {
				accountUsage(stderr)
				return 2
			}
			fmt.Fprintf(stderr, "nodevas account totp-reset: %v\n", err)
			return 1
		}
		return 0
	case "help", "-h", "--help":
		accountUsage(stdout)
		return 0
	default:
		accountUsage(stderr)
		return 2
	}
}

// errAccountUsage is a malformed account command line.
var errAccountUsage = errors.New("usage")

func accountUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: nodevas account totp-reset [--project <dir>] <email-or-id-or-name>")
	fmt.Fprintln(w, "       (turns two-factor authentication off for one account; run it on the server host)")
	fmt.Fprintln(w, "       (an ID that starts with \"-\" goes after --: totp-reset --project <dir> -- <id>)")
}

func accountTOTPReset(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("account totp-reset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectFlag := fs.String("project", ".", "workspace directory")
	// Flags may come before or after the account.
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errAccountUsage
	}
	who := strings.TrimSpace(fs.Arg(0))
	if err := fs.Parse(fs.Args()[1:]); err != nil {
		return err
	}
	if who == "" || fs.NArg() != 0 {
		return errAccountUsage
	}

	root, err := filepath.Abs(*projectFlag)
	if err != nil {
		return err
	}
	database, err := db.Open(root)
	if err != nil {
		return fmt.Errorf("open account database: %w", err)
	}
	defer database.Close()
	users := auth.NewUserStoreDB(database)
	ctx := context.Background()

	account, wasEnabled, err := users.ResetTOTP(ctx, who)
	if errors.Is(err, auth.ErrNoSuchAccountToReset) {
		return fmt.Errorf("no account matches %q (give its sign-in address, ID or name)", who)
	}
	if err != nil {
		return err
	}
	operator := os.Getenv("USER")
	if operator == "" {
		operator = os.Getenv("USERNAME")
	}
	if err := audit.New(database).Record(ctx, audit.Event{
		At:        time.Now(),
		ActorName: "server-cli",
		Action:    audit.ActionTOTPReset,
		Target:    account.ID,
		Detail: map[string]any{
			"account": account.Name, "wasEnabled": wasEnabled, "operator": operator,
		},
	}); err != nil {
		// The reset already happened; say so, but do not pretend it did not.
		fmt.Fprintf(stderr, "warning: the reset is done but could not be written to the audit trail: %v\n", err)
	}
	if wasEnabled {
		fmt.Fprintf(stdout, "%s: two-factor authentication turned off; it signs in with its emailed passcode until it enrolls again\n", account.Name)
	} else {
		fmt.Fprintf(stdout, "%s: two-factor authentication was not on; nothing to reset\n", account.Name)
	}
	fmt.Fprintf(stdout, "%s: its saved sessions were removed; restart the server to end the ones it still holds in memory\n", account.Name)
	return nil
}
