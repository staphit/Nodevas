package project

import (
	"os"
	"testing"

	"nodevas/internal/testpg"
)

// TestMain drops the per-binary template database (and stops the embedded
// server) the search index tests start through internal/testpg.
func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }
