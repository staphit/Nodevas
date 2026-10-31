package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"nodevas/internal/runner"
)

// migrateRunnerConfig finishes migrating W1's `"autoAccept": true` (the
// agents installed here go on the list) and saves the new shape at once, so
// the warning is printed this one time.
func migrateRunnerConfig(path string, cfg *runner.Config, stderr io.Writer) error {
	if !cfg.LegacyAutoAccept() {
		return nil
	}
	warning := cfg.MigrateLegacyAutoAccept(runner.InstalledKinds(context.Background(), runnerCommands, cfg))
	if err := runner.SaveConfig(path, cfg); err != nil {
		return fmt.Errorf("saving the migrated auto-accept policy: %w", err)
	}
	if warning != "" {
		fmt.Fprintln(stderr, warning)
	}
	return nil
}

// runnerAutoAccept is `nodevas runner autoaccept`: the allowlist of offers
// this runner takes without asking at its terminal.
func runnerAutoAccept(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	sub, args := args[0], args[1:]
	fs, configFlag := newRunnerFlags("autoaccept " + sub)
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	path, cfg, err := loadRunnerConfig(*configFlag, stderr)
	if err != nil {
		return err
	}
	installed := func() []string { return runner.InstalledKinds(context.Background(), runnerCommands, cfg) }
	var note string
	switch sub {
	case "show":
		if len(rest) != 0 {
			return errUsage
		}
		printAutoAccept(stdout, cfg)
		return nil
	case "on", "off":
		if len(rest) != 0 {
			return errUsage
		}
		if sub == "on" {
			cfg.EnableAutoAccept(installed())
		} else {
			cfg.SetAutoAccept(false, nil)
		}
	case "step-up", "stepup":
		if len(rest) != 1 {
			return errUsage
		}
		switch strings.ToLower(rest[0]) {
		case "required":
			cfg.SetStepUpRequired(true)
		case "optional":
			cfg.SetStepUpRequired(false)
		default:
			return fmt.Errorf("step-up must be required or optional")
		}
	case "verify":
		if len(rest) != 1 {
			return errUsage
		}
		switch strings.ToLower(rest[0]) {
		case "on":
			cfg.SetAllowVerify(true)
			note = "verify jobs run this repository's test command on code an agent wrote; they are taken unasked only in the repos listed here"
		case "off":
			cfg.SetAllowVerify(false)
		default:
			return fmt.Errorf("verify must be on or off")
		}
	case "allow-shell", "remote-typing":
		if len(rest) != 1 {
			return errUsage
		}
		var on bool
		switch strings.ToLower(rest[0]) {
		case "on":
			on = true
		case "off":
		default:
			return fmt.Errorf("%s must be on or off", sub)
		}
		if sub == "allow-shell" {
			cfg.SetAllowShell(on)
		} else {
			cfg.SetAllowRemoteTyping(on)
		}
	case "repo", "agent", "mode":
		if len(rest) != 2 || (rest[0] != "add" && rest[0] != "remove") {
			return errUsage
		}
		add, arg := rest[0] == "add", rest[1]
		switch {
		case sub == "repo" && add:
			repo, err := cfg.AddAutoAcceptRepo(arg)
			if err != nil {
				return err
			}
			note = fmt.Sprintf("auto-accept now covers %s (id %s)", repo.Name, repo.ID)
		case sub == "repo":
			id, err := cfg.RemoveAutoAcceptRepo(arg)
			if err != nil {
				return err
			}
			note = fmt.Sprintf("removed repo %s from auto-accept", id)
		case sub == "agent" && add:
			kind, err := cfg.AddAutoAcceptAgent(arg)
			if err != nil {
				return err
			}
			note = "auto-accept now covers agent " + kind
			if !slices.Contains(installed(), kind) {
				note += " (not installed here yet)"
			}
		case sub == "agent":
			kind, err := cfg.RemoveAutoAcceptAgent(arg)
			if err != nil {
				return err
			}
			note = "removed agent " + kind + " from auto-accept"
		case add:
			mode, err := cfg.AddAutoAcceptMode(arg)
			if err != nil {
				return err
			}
			note = "auto-accept now covers " + mode + " runs"
		default:
			mode, err := cfg.RemoveAutoAcceptMode(arg)
			if err != nil {
				return err
			}
			note = "removed " + mode + " runs from auto-accept"
		}
	default:
		return errUsage
	}
	if err := runner.SaveConfig(path, cfg); err != nil {
		return err
	}
	if note != "" {
		fmt.Fprintln(stdout, note)
	}
	printAutoAccept(stdout, cfg)
	warnAutoAccept(stderr, cfg, sub == "step-up" || sub == "stepup")
	fmt.Fprintln(stdout, "saved; restart a running runner for it to take effect")
	return nil
}

// warnAutoAccept prints what an enabled policy means; stepUpChanged prints
// the step-up warning even while auto-accept is off.
func warnAutoAccept(w io.Writer, cfg *runner.Config, stepUpChanged bool) {
	policy := cfg.AutoAcceptPolicy()
	if policy.Enabled {
		fmt.Fprintln(w, runner.AutoAcceptWarning)
	}
	if !policy.RequireStepUp && (policy.Enabled || stepUpChanged) {
		fmt.Fprintln(w, runner.StepUpOptionalWarning)
	}
	if policy.AllowShell {
		fmt.Fprintln(w, runner.AllowShellWarning)
	}
	if policy.AllowRemoteTyping {
		fmt.Fprintln(w, runner.RemoteTypingWarning)
	}
	if policy.Enabled && (len(policy.Repos) == 0 || len(policy.Agents) == 0) {
		fmt.Fprintln(w, "note: no repository or no agent is on the auto-accept list, so nothing is auto-accepted yet.")
	}
}

// printAutoAccept shows the policy, repositories by name where this runner
// still has them.
func printAutoAccept(w io.Writer, cfg *runner.Config) {
	policy := cfg.AutoAcceptPolicy()
	row := func(key string, value any) { fmt.Fprintf(w, "  %-22s %v\n", key, value) }
	fmt.Fprintln(w, "auto-accept:")
	if policy.Enabled {
		row("autoAccept", "on (offers covered below start without anyone here saying yes)")
	} else {
		row("autoAccept", "off (every offer is asked about at this terminal)")
	}
	if policy.RequireStepUp {
		row("stepUp", "required (only from a dispatcher who re-entered a passcode on the hub recently)")
	} else {
		row("stepUp", "OPTIONAL (a signed-in hub session alone is enough; nodevas runner autoaccept step-up required)")
	}
	repos := make([]string, 0, len(policy.Repos))
	for _, id := range policy.Repos {
		label := id + " (no longer a repository of this runner)"
		for _, repo := range cfg.Repos {
			if repo.ID == id {
				label = fmt.Sprintf("%s (%s)", repo.Name, id)
			}
		}
		repos = append(repos, label)
	}
	row("repos", listOrNone(repos))
	row("agents", listOrNone(policy.Agents))
	row("modes", listOrNone(runner.AutoAcceptModeNames(policy)))
	if policy.AllowShell {
		row("allowShell", "ON (runs whose agent may run any shell command are auto-accepted too)")
	} else {
		row("allowShell", "off (a run whose agent may run any shell command is asked about here)")
	}
	if policy.AllowVerify {
		row("verify", "ON (verify jobs, the repo's test command, are auto-accepted in the listed repos)")
	} else {
		row("verify", "off (a verify job, the repo's test command, is asked about here)")
	}
	if policy.AllowRemoteTyping {
		row("remoteTyping", "ON (the dispatcher may type into auto-accepted interactive runs)")
	} else {
		row("remoteTyping", "off (auto-accepted interactive runs are view only)")
	}
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
}
