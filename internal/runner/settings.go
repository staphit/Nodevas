package runner

import (
	"fmt"

	"nodevas/internal/runnerwire"
)

// Settings from the hub (agent workspace, phase 2; runnerwire/settings.go):
// the runner's owner changes how many runs it takes at once from the web.
// Only the owner may (the welcome's owner id), only while remoteSettings is
// on, and never past maxConcurrentCeiling, which is set on this machine only.
//
// The new limit applies at once, then goes into the settings file: read
// fresh and written back with only maxConcurrent changed, so a `nodevas
// runner set` made since the runner started is kept. A CLI change in the
// same instant can still lose to it, or it to the CLI's. A lower limit stops
// no run: offers past it are declined until fewer runs are in hand.
//
// The built-in runner of a single-user hub also takes the settings the app
// shows as "This computer" (localsettings.go); any other runner refuses
// those with runnerwire.SettingsErrLocalOnly.

// MaxConcurrentRuns is how many runs this runner takes at once now: the
// config's, until the hub changed it.
func (c *Client) MaxConcurrentRuns() int {
	if n := c.maxRuns.Load(); n > 0 {
		return int(n)
	}
	return c.cfg().MaxConcurrentRuns()
}

// settingsAnswer is a settings_done on its way out, with the hello to send
// first when the owner asked to look for the agent CLIs again.
type settingsAnswer struct {
	done runnerwire.SettingsDone
	info *runnerwire.Info
}

// applySettings answers a settings_request from the hub of the runner whose
// owner is ownerID. Everything asked for is checked before anything applies.
func (c *Client) applySettings(ownerID string, req runnerwire.SettingsRequest) runnerwire.SettingsDone {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	done := runnerwire.SettingsDone{ID: req.ID, MaxConcurrent: c.MaxConcurrentRuns()}
	refuse := func(code, format string, args ...any) runnerwire.SettingsDone {
		done.Code, done.Error = code, truncate(fmt.Sprintf(format, args...), runnerwire.MaxSettingsError)
		c.logf("settings from the hub refused: %s", done.Error)
		return done
	}
	if ownerID == "" || req.AccountID != ownerID {
		return refuse(runnerwire.SettingsErrRefused, "only this runner's owner may change its settings")
	}
	cfg := c.cfg()
	if !cfg.RemoteSettingsOn() {
		return refuse(runnerwire.SettingsErrOff, "this runner's settings are changed on its own machine only "+
			"(nodevas runner set remoteSettings on)")
	}
	local := localRequest(req)
	if local && !c.BuiltIn() {
		return refuse(runnerwire.SettingsErrLocalOnly, "only this computer's built-in runner takes these settings "+
			"from the app; change them on the runner's own machine")
	}
	if err := checkLocalRequest(req); err != nil {
		return refuse(runnerwire.SettingsErrInvalid, "%v", err)
	}
	n, ceiling := 0, cfg.MaxConcurrentCeilingOrDefault()
	if req.MaxConcurrent != nil {
		if n = *req.MaxConcurrent; n < 1 || n > ceiling {
			return refuse(runnerwire.SettingsErrRange, "maxConcurrent must be from 1 to %d here "+
				"(maxConcurrentCeiling on the runner)", ceiling)
		}
	}
	persisted := true
	var notSaved []string
	if local {
		saved, err := c.updateConfig(editLocal(req))
		switch {
		case err != nil:
			notSaved = append(notSaved, err.Error())
			persisted = false
		case !saved:
			persisted = false
		}
		c.logf("settings changed from Nodevas%s", savedWhere(saved, err, c.ConfigPath))
	}
	if req.MaxConcurrent != nil {
		from := done.MaxConcurrent
		c.maxRuns.Store(int32(n))
		done.MaxConcurrent = n
		switch {
		case c.ConfigPath == "":
			persisted = false
			c.logf("maxConcurrent %d → %d from the hub, until the runner stops (no settings file to save it in)", from, n)
		default:
			if err := saveMaxConcurrent(c.ConfigPath, n); err != nil {
				persisted = false
				notSaved = append(notSaved, err.Error())
				c.logf("maxConcurrent %d → %d from the hub; %v: %v", from, n, errNotSaved, err)
			} else {
				c.logf("maxConcurrent %d → %d from the hub; saved in %s", from, n, c.ConfigPath)
			}
		}
	}
	if !local && req.MaxConcurrent == nil {
		return done
	}
	if len(notSaved) > 0 {
		done.Error = truncate(fmt.Sprintf("%v: %s", errNotSaved, notSaved[0]), runnerwire.MaxSettingsError)
	}
	done.Persisted = persisted && len(notSaved) == 0
	return done
}

// savedWhere ends a log line about a change: where it was saved, or why not.
func savedWhere(saved bool, err error, path string) string {
	switch {
	case err != nil:
		return fmt.Sprintf("; %v: %v", errNotSaved, err)
	case saved:
		return "; saved in " + path
	}
	return ", until the runner stops (no settings file to save it in)"
}

// saveMaxConcurrent rewrites maxConcurrent in the settings file at path and
// nothing else, from the file as it is now.
func saveMaxConcurrent(path string, n int) error {
	cfg, err := LoadConfig(path)
	if err != nil {
		return err
	}
	if cfg.LegacyAutoAccept() {
		// Writing it back would drop the old autoAccept's agents; the CLI
		// migrates it first (nodevas runner status).
		return fmt.Errorf("%s still has the old autoAccept setting; run nodevas runner status once to update it", path)
	}
	cfg.MaxConcurrent = n
	return SaveConfig(path, cfg)
}
