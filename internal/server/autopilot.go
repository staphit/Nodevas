package server

import (
	"context"
	"net/http"
	"time"

	"nodevas/internal/auth"
	"nodevas/internal/autopilot"
	"nodevas/internal/db"
)

// startAutopilot builds the W7 orchestrator on the run dispatcher and runs it
// until the server stops. It needs the workspace's projects to read goals'
// task DAGs, so a server without a project manager has none.
func (s *Server) startAutopilot(database *db.DB) {
	if s.pm == nil || s.runs == nil || database == nil {
		return
	}
	s.autopilot = autopilot.New(autopilot.Options{
		Store:   autopilot.NewStore(database),
		Runs:    autopilot.DispatcherRuns{D: s.runs},
		Runners: autopilot.RegistryRunners{R: s.runners},
		Board:   autopilot.ProjectBoard{PM: s.pm},
		Changed: func(project, id string) { s.broadcastProject(project, autopilot.ChangedEvent, id) },
		BoardChanged: func(project string) {
			s.broadcastProject(project, "state-changed", "")
		},
	})
	s.runs.Observe(s.autopilot.Observe)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-s.stop
		cancel()
	}()
	s.autopilot.Start(ctx)
}

// configureAutopilotStepUp records the creator's step-up on an autopilot's
// runs, as configureStepUp does for runs a person starts.
func (s *Server) configureAutopilotStepUp(api interface {
	UseStepUp(func(*http.Request) (time.Time, bool))
}) {
	if accounts, ok := s.auth.(*auth.SessionAuth); ok {
		api.UseStepUp(accounts.SteppedUpAt)
	}
}
