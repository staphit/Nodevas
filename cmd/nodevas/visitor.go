package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"nodevas/internal/auth"
)

// visitor turns the shared read-only credential on and off.
//
// It deliberately does not take the workspace lock that `user` takes for its
// mutating actions. The lock is there because account changes and a running
// server used to race over a file; this writes two rows in the settings table
// of the same SQLite database the server has open, under WAL, which is a case
// SQLite is built for. Requiring a lock here would mean stopping the service
// to turn visitor access off — and the situation an operator turns it off in
// is one where they want it off this second, not after everyone else's editing
// session has been dropped.
func visitor(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	action := args[0]
	fs := flag.NewFlagSet("visitor", flag.ExitOnError)
	projectFlag := fs.String("project", ".", "workspace directory")
	pin := fs.String("pin", "",
		"the shared pin visitors type. It is meant to be published, so unlike an account pin it may be short")
	passcode := fs.String("passcode", "",
		"the fixed second factor. Omit it to have a strong one generated, which is the intended path")
	_ = fs.Parse(args[1:])

	root, err := filepath.Abs(*projectFlag)
	if err != nil {
		log.Fatal(err)
	}
	users, err := auth.NewUserStore(root)
	if err != nil {
		log.Fatalf("accounts: %v", err)
	}
	defer users.Close()
	ctx := context.Background()

	switch action {
	case "show":
		current, code, err := users.VisitorCredential(ctx)
		if err != nil {
			log.Fatal(err)
		}
		if current == "" {
			fmt.Println("visitor access: off")
			return
		}
		// Both halves printed in the clear. This credential is published by
		// definition, and an operator who cannot read it back cannot tell
		// anybody what it is.
		fmt.Println("visitor access: on")
		fmt.Printf("pin:      %s\n", current)
		fmt.Printf("passcode: %s\n", code)
	case "on":
		if strings.TrimSpace(*pin) == "" {
			log.Fatal("--pin is required: it is what a visitor types")
		}
		code := strings.TrimSpace(*passcode)
		if code == "" {
			if code, err = auth.GenerateVisitorOTP(); err != nil {
				log.Fatalf("generate a passcode: %v", err)
			}
		}
		if err := users.SetVisitorCredential(ctx, *pin, code); err != nil {
			log.Fatal(err)
		}
		fmt.Println("visitor access: on")
		fmt.Printf("pin:      %s\n", strings.TrimSpace(*pin))
		fmt.Printf("passcode: %s\n", code)
		fmt.Fprintln(os.Stderr,
			"\nAnyone with these two can read every project on this server, including its\n"+
				"attachments, and may copy or save anything they can see. They cannot write,\n"+
				"upload, bulk-export, administer the server, or browse the host filesystem.\n"+
				"There is no per-person accountability behind them and no way to revoke one\n"+
				"visitor without revoking all of them.\n"+
				"A running server picks this up on the next sign-in; nothing needs restarting.")
	case "off":
		if err := users.ClearVisitorCredential(ctx); err != nil {
			log.Fatal(err)
		}
		// Worth stating, because the alternative assumption — that the people
		// already looking keep looking until their session expires — is the one
		// an operator would reasonably make of any other credential change.
		fmt.Println("visitor access: off; sessions opened with it stop at their next request")
	default:
		usage()
		os.Exit(2)
	}
}
