package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
)

// user manages the accounts a networked server authenticates against.
func user(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	action := args[0]
	fs := flag.NewFlagSet("user", flag.ExitOnError)
	projectFlag := fs.String("project", ".", "workspace directory")
	name := fs.String("user", "", "account name")
	role := fs.String("role", "", "account role: admin or member")
	password := fs.String("password", "",
		"DEPRECATED: password on the command line, visible to every process on this machine; use --password-stdin")
	passwordStdin := fs.Bool("password-stdin", false,
		"read the password from stdin (the whole of it, so `printf %s pw | nodevas user add ...` works)")
	email := fs.String("email", "",
		"where this account's one-time passcodes are sent (required by `user pin`)")
	pinValue := fs.String("pin", "",
		"DEPRECATED: pin on the command line, visible to every process on this machine; omit it to have one generated")
	_ = fs.Parse(args[1:])
	if *password != "" {
		fmt.Fprintln(os.Stderr,
			"warning: --password puts the password in this machine's process list and shell history; "+
				"prefer --password-stdin or NODEVAS_PASSWORD")
	}

	root, err := filepath.Abs(*projectFlag)
	if err != nil {
		log.Fatal(err)
	}
	// No workspace lock, for any action.
	//
	// There used to be one on everything that wrote, from when accounts lived in
	// a users.json that both this process and the server rewrote read-modify-
	// write: a CLI edit racing a running server silently discarded one side's
	// change, and refusing to run was the only defence. Accounts are rows in the
	// workspace database now. Two processes writing it is WAL doing its job, and
	// UserStore caches nothing on either side, so a change here is visible to the
	// server on its next query rather than at its next restart.
	//
	// Keeping the lock would have kept the restart under a different name, and
	// the restart is the expensive part: rotating one person's PIN should not
	// cost everybody else their editing session.
	users, err := auth.NewUserStore(root)
	if err != nil {
		log.Fatalf("accounts: %v", err)
	}
	// The CLI owns this handle, and SQLite's WAL sidecars are only tidied away
	// on a clean close.
	defer users.Close()

	// A CLI invocation has no request behind it; nothing cancels these.
	ctx := context.Background()

	switch action {
	case "list":
		for _, account := range users.Records(ctx) {
			fmt.Printf("%s\t%s\n", account.Name, account.Role)
		}
	case "add", "passwd":
		if strings.TrimSpace(*name) == "" {
			log.Fatal("--user is required")
		}
		secret, err := readPassword(*password, *passwordStdin)
		if err != nil {
			log.Fatal(err)
		}
		if action == "add" {
			err = users.AddWithRole(ctx, *name, secret, identity.Role(strings.ToLower(strings.TrimSpace(*role))))
		} else {
			err = users.SetPassword(ctx, *name, secret)
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: ok\n", *name)
	case "pin":
		// The PIN is half of what signs somebody in to the web UI, and it is
		// shown here exactly once: nothing stores it in a form anyone,
		// including this program, can read back.
		if strings.TrimSpace(*name) == "" {
			log.Fatal("--user is required")
		}
		if strings.TrimSpace(*email) == "" {
			log.Fatal("--email is required: without an address the account can never receive a passcode")
		}
		secret := strings.TrimSpace(*pinValue)
		generated := secret == ""
		if generated {
			secret, err = auth.GeneratePin()
			if err != nil {
				log.Fatalf("generate pin: %v", err)
			}
		} else {
			fmt.Fprintln(os.Stderr,
				"warning: --pin puts the pin in this machine's process list and shell history; "+
					"omit it to have one generated instead")
		}
		if err := users.SetPin(ctx, *name, secret, *email); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: pin set, passcodes go to %s\n", *name, *email)
		if generated {
			fmt.Printf("\n  pin: %s\n\n", secret)
			fmt.Fprintln(os.Stderr,
				"Give this to the account holder over a channel you trust, and not by email — "+
					"a mailbox that holds both the pin and the passcodes is one factor, not two. "+
					"It cannot be shown again; run this command to issue a new one.")
		}
		fmt.Fprintln(os.Stderr,
			"Any session this account had is now invalid: changing a pin ends the sessions it authorised.")
	case "pin-clear":
		if strings.TrimSpace(*name) == "" {
			log.Fatal("--user is required")
		}
		if err := users.ClearPin(ctx, *name); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: pin cleared; this account can no longer sign in to the web UI\n", *name)
	case "remove":
		if strings.TrimSpace(*name) == "" {
			log.Fatal("--user is required")
		}
		refuseRemovingTheLastAccount(ctx, users, root)
		if err := users.Remove(ctx, *name); err != nil {
			log.Fatal(err)
		}
		// The server revokes this account's sessions on its own: Authenticate
		// re-reads the row on every request and a session whose account is gone
		// is a session that ends there. Nothing to restart.
		fmt.Printf("%s: removed\n", *name)
	case "role":
		if strings.TrimSpace(*name) == "" {
			log.Fatal("--user is required")
		}
		if strings.TrimSpace(*role) == "" {
			log.Fatal("--role admin|member is required")
		}
		if err := users.SetRole(ctx, *name, identity.Role(strings.ToLower(strings.TrimSpace(*role)))); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: role %s\n", *name, strings.ToLower(strings.TrimSpace(*role)))
	default:
		usage()
		os.Exit(2)
	}
}

// refuseRemovingTheLastAccount stops `user remove` from emptying the table.
//
// This is the one account change that cannot be undone from the CLI, because
// the thing it breaks is the next start: a networked server refuses to serve a
// workspace with no accounts, so the recovery for "I removed the last one" is
// to add another while the server is already down and refusing to come up.
// Nothing about that is obvious at the moment somebody types the command.
//
// It replaced a workspace lock that was claimed to protect account changes and
// did not protect this one either — stopping the server first and then removing
// the last account left exactly the same broken workspace.
func refuseRemovingTheLastAccount(ctx context.Context, users *auth.UserStore, root string) {
	if users.Count(ctx) > 1 {
		return
	}
	log.Fatalf(
		"refusing to remove the only account: a networked server will not start with an "+
			"empty account table. Add the replacement first with "+
			"`nodevas user add --project %q --user <name> --role admin`.", root)
}

// readPassword takes the password from --password-stdin, the flag, the
// environment, or an interactive prompt, in that order. --password-stdin wins
// because it is the one source that never reaches another process: a flag is
// in /proc and the shell history, and an environment variable is readable from
// the process's own environ.
//
// Stdin is not echo-suppressed at the prompt (that needs golang.org/x/term,
// which this module does not depend on), so a scripted caller should pipe the
// password in with --password-stdin rather than type it.
func readPassword(flagValue string, fromStdin bool) (string, error) {
	if fromStdin {
		if flagValue != "" {
			return "", errors.New("--password and --password-stdin are mutually exclusive")
		}
		return readPasswordFromStdin()
	}
	if flagValue != "" {
		return flagValue, nil
	}
	if fromEnv := os.Getenv("NODEVAS_PASSWORD"); fromEnv != "" {
		return fromEnv, nil
	}
	fmt.Fprint(os.Stderr, "password: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read password: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readPasswordFromStdin consumes all of stdin and strips one trailing newline,
// so both `printf %s pw |` and `echo pw |` produce the same secret. Reading to
// EOF rather than to the first newline means a password is never silently
// truncated at a character the caller did not think was special.
func readPasswordFromStdin() (string, error) {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxPasswordBytes+1))
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(raw) > maxPasswordBytes {
		return "", fmt.Errorf("password on stdin exceeds %d bytes", maxPasswordBytes)
	}
	secret := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if secret == "" {
		return "", errors.New("--password-stdin was given but stdin was empty")
	}
	return secret, nil
}

// maxPasswordBytes bounds a piped password so a stray `cat bigfile |` fails
// loudly instead of being hashed.
const maxPasswordBytes = 4096
