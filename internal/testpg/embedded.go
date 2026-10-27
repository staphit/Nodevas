package testpg

// startEmbedded starts the per-binary embedded server from a cached template
// data directory (os.UserCacheDir()/nodevas-testpg/<version>/, initdb'd once
// per machine under a lock file) with fsync, synchronous_commit and
// full_page_writes off, and returns its admin URL.
//
// Not implemented yet: the red phase runs the suite with Env set.
func startEmbedded() (string, error) {
	return "", ErrEmbeddedNotImplemented
}

// stopEmbedded stops the server startEmbedded started, if any.
func stopEmbedded() {}
