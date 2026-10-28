// Package locale is how the hub speaks the reader's language.
//
// The interface is English or Traditional Chinese. Whatever the hub writes for
// a person to read — an error, a warning, a title it makes up — is written in
// both and picked per request, so an English reader never gets a Chinese
// sentence and a Chinese reader never gets an English one.
//
// It is a leaf: the store, the project manager and every handler package use
// it, so it imports nothing of theirs.
package locale

import "strings"

// Language is one of the interface languages.
type Language string

const (
	English Language = "en"
	Chinese Language = "zh-TW"
)

// FromTag maps a language tag ("zh-TW", "zh-Hant-TW", "en-US", an
// Accept-Language list...) to an interface language: Chinese for any zh tag,
// English for everything else, nothing included.
func FromTag(tag string) Language {
	first, _, _ := strings.Cut(tag, ",")
	first, _, _ = strings.Cut(first, ";")
	first = strings.ToLower(strings.TrimSpace(first))
	if first == "zh" || strings.HasPrefix(first, "zh-") {
		return Chinese
	}
	return English
}

// CopyTitle is the title a duplicate of a node titled title is given.
func CopyTitle(title string, language Language) string {
	if language == Chinese {
		return title + "（副本）"
	}
	return title + " copy"
}
