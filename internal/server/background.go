package server

import (
	"context"

	"nodevas/internal/locale"
	"nodevas/internal/safego"
)

// Background work the server starts itself (cost settlement, retention, run
// retries) runs through goBackground: a panic in it is logged instead of
// ending the process, and Shutdown waits for it before returning, because
// serve closes the database right after Shutdown and a prune cut off half
// way leaves its batch half written.

// goBackground runs fn on its own goroutine, recovered by safego and
// counted for Shutdown. Once Shutdown has begun it starts nothing.
func (s *Server) goBackground(name string, fn func()) {
	s.backgroundMu.Lock()
	if s.backgroundStopped {
		s.backgroundMu.Unlock()
		return
	}
	s.background.Add(1)
	s.backgroundRunning.Add(1)
	s.backgroundMu.Unlock()
	go func() {
		defer s.background.Done()
		defer s.backgroundRunning.Add(-1)
		defer safego.Recover(name)
		fn()
	}()
}

// waitBackground stops new background work and waits for what is running,
// until ctx ends. Call it after closing s.stop, which is what asks the
// loops to return.
func (s *Server) waitBackground(ctx context.Context) error {
	s.backgroundMu.Lock()
	s.backgroundStopped = true
	s.backgroundMu.Unlock()
	done := make(chan struct{})
	go func() {
		s.background.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return locale.Errorf("background work still running (%d): %w", "背景工作仍在進行（%d）：%w", s.backgroundRunning.Load(), ctx.Err())
	}
}
