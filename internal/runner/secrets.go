package runner

import (
	"path"
	"strings"
)

// The secret deny list of repo browsing (repobrowse.go): files that may hold
// credentials are never listed, read, or shown in a status or a diff.

// secretDirs are directories everything under is hidden.
var secretDirs = map[string]bool{".ssh": true, ".aws": true, ".gnupg": true}

// secretNames are file names hidden wherever they are.
var secretNames = map[string]bool{
	".npmrc": true, ".pypirc": true, ".netrc": true, ".git-credentials": true,
	"credentials.json": true, "runner.json": true,
}

// secretExts are extensions of key and keystore files.
var secretExts = map[string]bool{".pem": true, ".key": true, ".p12": true, ".pfx": true, ".keystore": true, ".kdbx": true}

// envExamples are the .env files meant to be shared.
var envExamples = map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true}

// secretName reports whether a file or directory named name is hidden.
func secretName(name string) bool {
	n := strings.ToLower(name)
	switch {
	case secretDirs[n], secretNames[n]:
		return true
	case n == ".env" || (strings.HasPrefix(n, ".env.") && !envExamples[n]):
		return true
	case strings.HasPrefix(n, "id_rsa"), strings.HasPrefix(n, "id_ed25519"), strings.HasPrefix(n, "id_ecdsa"):
		return true
	case strings.HasPrefix(n, "secrets."):
		return true
	}
	return secretExts[path.Ext(n)]
}

// secretPath reports whether a slash-separated repo path is hidden: any of
// its segments is.
func secretPath(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if seg != "" && secretName(seg) {
			return true
		}
	}
	return false
}
