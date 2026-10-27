package runs

// SensitivePath reports whether p is a path whose change can neuter the test
// command or the CI gate (package manifests, go.mod, Makefiles, CI configs).
// Moved here from internal/autopilot so the diff guard and the autopilot
// share one list.
//
// RED PHASE STUB: always false.
func SensitivePath(p string) bool { return false }
