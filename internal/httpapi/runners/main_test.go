package runners_test

import (
	"os"
	"testing"

	"nodevas/internal/testpg"
)

func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }
