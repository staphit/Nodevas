package document

import "unicode"

// LanguageOf is the language tag of text for an HTML lang attribute:
// "zh-Hant" for Chinese, "en" for text in a Latin script, "" when it has no
// words to tell by. A Chinese document quotes code and names in Latin
// letters, and an English one now and then a Chinese word, so it goes by
// which there is more of: a Han character is about a word.
func LanguageOf(text string) string {
	han, words := 0, 0
	inWord := false
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
			inWord = false
		case unicode.Is(unicode.Latin, r):
			if !inWord {
				words++
			}
			inWord = true
		default:
			inWord = false
		}
	}
	switch {
	case han == 0 && words == 0:
		return ""
	case han >= words:
		return "zh-Hant"
	}
	return "en"
}
