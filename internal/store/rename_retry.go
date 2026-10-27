package store

import (
	"os"
	"time"
)

// renameRetryWindow bounds how long renameReplacing waits out a reader.
const renameRetryWindow = 2 * time.Second

// renameReplacing is root.Rename(from, to), retried for a moment when the
// rename failed only because another handle has the target open.
//
// On Windows, replacing a file that someone is reading fails with "Access is
// denied" (or a sharing violation): Go opens files without FILE_SHARE_DELETE,
// so any concurrent reader of graph.yaml (a GET, the search index
// revalidating, the watcher) briefly blocks the replace. The reader is gone
// within milliseconds, so the write waits for it instead of failing the
// request. Elsewhere the error never happens and nothing is retried.
func renameReplacing(root *os.Root, from, to string) error {
	deadline := time.Now().Add(renameRetryWindow)
	delay := 2 * time.Millisecond
	for {
		err := root.Rename(from, to)
		if err == nil || !transientRenameError(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 64*time.Millisecond {
			delay *= 2
		}
	}
}
