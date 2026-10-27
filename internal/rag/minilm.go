package rag

import (
	"context"
	_ "embed"
	"io"
	"os/exec"
	"time"
)

//go:embed minilm.py
var miniLMScript string

type EmbeddingFunc func(context.Context, []string) ([][]float64, error)

// MiniLM uses Chroma's default ONNX model through an owned, lazy worker.
// Call Close when the owning MCP session or test finishes.
func MiniLM(python string, stderr io.Writer) *MiniLMWorker {
	if python == "" {
		python = "python"
	}
	// -X utf8 makes the helper read and write UTF-8 whatever the host's
	// locale; without it a Windows cp950/cp936 console turns Chinese input
	// into "??" before the model sees it. -I leaves -X intact.
	return &MiniLMWorker{newCommand: func() *exec.Cmd { return exec.Command(python, "-I", "-X", "utf8", "-u", "-c", miniLMScript) }, stderr: stderr,
		gate: make(chan struct{}, 1), idleTimeout: time.Minute}
}
