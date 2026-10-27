package store

import "time"

// renameRetryDelays is how long renameWithRetry waits before each retry: about
// a second in all, the time a virus scanner, an indexer or the file watcher
// takes to let go of a file it just opened.
var renameRetryDelays = []time.Duration{
	10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond,
	80 * time.Millisecond, 160 * time.Millisecond, 250 * time.Millisecond,
	250 * time.Millisecond, 250 * time.Millisecond,
}

// renameWithRetry runs rename, retrying while it fails the way a file briefly
// held open by another process makes it fail (on Windows: access denied or a
// sharing violation on the target). Any other error, or success, returns at
// once; elsewhere no error is transient, so it is one plain call.
func renameWithRetry(rename func() error) error {
	err := rename()
	for _, delay := range renameRetryDelays {
		if err == nil || !isTransientRenameError(err) {
			return err
		}
		time.Sleep(delay)
		err = rename()
	}
	return err
}
