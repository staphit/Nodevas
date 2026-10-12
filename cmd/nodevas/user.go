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
		"the address this account signs in to the web UI with; its one-time passcodes go there "+
			"(required by `user email` unless --clear; optional for `user add`)")
	clearEmail := fs.Bool("clear", false,
		"with `user email`: remove the address, so the account can no longer sign in to the web UI")
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
	// the restart is the expensive part: changing one person's sign-in address
	// should not cost everybody else their editing session.
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
		printAccounts(os.Stdout, users.Records(ctx))
	case "add", "passwd":
		if strings.TrimSpace(*name) == "" {
			log.Fatal("--user is required")
		}
		secret, err := readPassword(*password, *passwordStdin)
		if err != nil {
			log.Fatal(err)
		}
		if action == "add" {
			err = users.AddWithEmail(ctx, *name, secret,
				identity.Role(strings.ToLower(strings.TrimSpace(*role))), *email)
		} else {
			err = users.SetPassword(ctx, *name, secret)
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: ok\n", *name)
	case "email":
		// The address is the whole of a web sign-in: whoever can read that
		// mailbox can sign in as this account. There is nothing to print once
		// and hand over any more — registering the address is the grant.
		if strings.TrimSpace(*name) == "" {
			log.Fatal("--user is required")
		}
		if *clearEmail {
			if strings.TrimSpace(*email) != "" {
				log.Fatal("--email and --clear are mutually exclusive")
			}
			if err := users.ClearEmail(ctx, *name); err != nil {
				log.Fatal(err)
			}
			fmt.Printf("%s: email cleared; this account can no longer sign in to the web UI\n", *name)
			fmt.Fprintln(os.Stderr,
				"Any web session this account had is now invalid.")
			return
		}
		if strings.TrimSpace(*email) == "" {
			log.Fatal("--email is required (or --clear to remove web sign-in)")
		}
		if err := users.SetEmail(ctx, *name, *email); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: signs in with %s\n", *name, strings.TrimSpace(*email))
		// Revocation rides on the account revision, which includes the address:
		// the server refuses an old session on its next request, without a
		// restart. Re-registering the address it already had changes nothing.
		fmt.Fprintln(os.Stderr,
			"Any session this account opened under a different address is now invalid. "+
				"Whoever can read this mailbox can sign in as this account: it is the only factor.")
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

// printAccounts writes one account per line as name, role and sign-in address,
// tab-separated, the address empty when the account cannot sign in to the web
// UI. Scripts read this — the deploy bootstrap does `cut -f1` on it — so the
// name stays the first field and nothing but tabs separates them.
func printAccounts(w io.Writer, records []auth.UserRecord) {
	for _, account := range records {
		fmt.Fprintf(w, "%s\t%s\t%s\n", account.Name, account.Role, account.Email)
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
