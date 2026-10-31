package testpg

import (
	"context"
	"fmt"
	"os"
	"time"

	"nodevas/internal/embeddedpg"
)

var scratch *embeddedpg.Scratch
var scratchDir string

// startEmbedded starts the per-binary embedded server from a cached template
// data directory (os.UserCacheDir()/nodevas-testpg/<version>-<target>/,
// initdb'd once per machine under a lock file) with fsync, synchronous_commit
// and full_page_writes off, and returns its admin URL.
func startEmbedded() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	binaries, err := embeddedpg.Binaries(ctx, os.Stderr)
	if err != nil {
		return "", fmt.Errorf("embedded PostgreSQL for tests (or set %s): %w", Env, err)
	}
	target, err := embeddedpg.CurrentTarget()
	if err != nil {
		return "", err
	}
	cache, err := embeddedpg.ScratchCache(target)
	if err != nil {
		return "", err
	}
	scratchDir, err = os.MkdirTemp("", "nodevas-testpg-")
	if err != nil {
		return "", err
	}
	scratch, err = embeddedpg.StartScratch(ctx, embeddedpg.ScratchOptions{
		Binaries: binaries, Dir: scratchDir, TemplateCache: cache,
	})
	if err != nil {
		_ = os.RemoveAll(scratchDir)
		return "", fmt.Errorf("embedded PostgreSQL for tests (or set %s): %w", Env, err)
	}
	return scratch.AdminURL(), nil
}

// stopEmbedded stops the server startEmbedded started, if any.
func stopEmbedded() {
	if scratch == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_ = scratch.Stop(ctx)
	_ = os.RemoveAll(scratchDir)
	scratch = nil
}
