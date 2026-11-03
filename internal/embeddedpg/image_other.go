//go:build unix && !linux && !darwin

package embeddedpg

// processImage is unknown on this platform: a live pid is treated as an
// unrelated process and its stale postmaster.pid removed.
func processImage(int) string { return "" }
