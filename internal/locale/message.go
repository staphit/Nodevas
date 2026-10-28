package locale

import (
	"errors"
	"fmt"
	"strings"
)

// Message is one sentence the hub says, written in both interface languages.
// It is an error, so a failure can carry it up from the store to the handler
// that knows who is reading; Error is the English, which is what logs, agents
// and any caller that does not say read.
//
// The formats take fmt verbs. An argument that is an error is rendered in the
// same language as the sentence around it, and %w wraps it as fmt.Errorf does.
type Message struct {
	english, chinese string
	args             []any
}

// Errorf is a failure worded in both languages.
func Errorf(english, chinese string, args ...any) error {
	return Say(english, chinese, args...)
}

// Say is a sentence worded in both languages.
func Say(english, chinese string, args ...any) *Message {
	return &Message{english: english, chinese: chinese, args: args}
}

// In is the sentence in language.
func (m *Message) In(language Language) string {
	format := m.english
	if language == Chinese {
		format = m.chinese
	}
	args := make([]any, len(m.args))
	for i, arg := range m.args {
		if err, ok := arg.(error); ok {
			args[i] = Render(err, language)
		} else {
			args[i] = arg
		}
	}
	return fmt.Sprintf(strings.ReplaceAll(format, "%w", "%v"), args...)
}

func (m *Message) Error() string  { return m.In(English) }
func (m *Message) String() string { return m.In(English) }

// MarshalText keeps a Message that reaches JSON readable without a language:
// a handler renders it for its reader first.
func (m *Message) MarshalText() ([]byte, error) { return []byte(m.In(English)), nil }

// Unwrap is the errors the sentence wraps with %w, so errors.Is and errors.As
// see through it.
func (m *Message) Unwrap() []error {
	var wrapped []error
	verbs := 0
	for i := 0; i+1 < len(m.english); i++ {
		if m.english[i] != '%' {
			continue
		}
		if m.english[i+1] == '%' {
			i++
			continue
		}
		if m.english[i+1] == 'w' && verbs < len(m.args) {
			if err, ok := m.args[verbs].(error); ok {
				wrapped = append(wrapped, err)
			}
		}
		verbs++
	}
	return wrapped
}

// Render is err for a reader of language. The hub's own sentences in it are
// in that language; anything else — a parser's or the operating system's
// words — stays as it was written.
func Render(err error, language Language) string {
	text := err.Error()
	if language == English {
		return text
	}
	var message *Message
	if !errors.As(err, &message) {
		return text
	}
	// The outermost sentence of the hub's: a wrapper around it that is not
	// one keeps its words, the sentence itself is swapped for its translation.
	return strings.Replace(text, message.Error(), message.In(language), 1)
}

// RenderAll is Render for each of messages.
func RenderAll(messages []*Message, language Language) []string {
	if messages == nil {
		return nil
	}
	rendered := make([]string, len(messages))
	for i, message := range messages {
		rendered[i] = message.In(language)
	}
	return rendered
}
