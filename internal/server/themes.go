package server

import (
	"log"
	"path/filepath"

	"nodevas/internal/db"
	"nodevas/internal/themes"
	"nodevas/internal/workspace"
)

// UseThemesDB turns theme packs on against the workspace database. Call it
// before Handler.
//
// The folder is decided here and kept: the database stays with the process if
// the workspace is switched, and packs' files have to stay with their rows.
func (s *Server) UseThemesDB(database *db.DB) {
	s.useDatabase(database)
	s.themes = themes.NewStore(database, filepath.Join(s.pm.Workspace(), workspace.DataDir, themes.DirName))
	if err := s.themes.Recover(); err != nil {
		log.Printf("theme packs: clean up after the last run: %v", err)
	}
}

// themesChanged tells every browser, whatever project it watches: packs are
// the server's.
func (s *Server) themesChanged(id string) {
	s.hub.Broadcast(themes.ChangedEvent, id)
}
