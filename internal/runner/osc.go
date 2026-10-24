package runner

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"

	"nodevas/internal/runnerwire"
)

// oscScanner finds OSC 7501 sequences (ESC ] 7501 ; payload BEL, or ESC \ as
// the terminator) in a terminal output stream fed to it in arbitrary chunks:
// a sequence may be split anywhere, even between ESC and ']'. Other OSC
// sequences are skipped whole, so their payloads never look like ours.
type oscScanner struct {
	state   oscState
	num     []byte // the OSC's number so far
	payload []byte
	// overflow: this payload grew past maxOSCPayload; it is dropped at its end.
	overflow bool
}

type oscState int

const (
	oscNormal  oscState = iota
	oscEsc              // saw ESC
	oscNumber           // inside ESC ] , reading the number
	oscPayload          // inside ESC ] 7501 ;
	oscOther            // inside some other OSC, skipping
	// after an ESC inside an OSC: '\' ends it (ST); anything else is junk
	// that also ends it (xterm does the same).
	oscPayloadEsc
	oscOtherEsc
)

const (
	esc = 0x1b
	bel = 0x07
	// maxOSCPayload bounds what one sequence may carry; status payloads are tiny.
	maxOSCPayload = 4 << 10
	// maxOSCNumber is longer than any OSC number.
	maxOSCNumber = 8
)

// feed scans p and returns the payloads of the OSC 7501 sequences that ended
// in it.
func (s *oscScanner) feed(p []byte) [][]byte {
	var out [][]byte
	for _, c := range p {
		switch s.state {
		case oscNormal:
			if c == esc {
				s.state = oscEsc
			}
		case oscEsc:
			switch c {
			case ']':
				s.state, s.num = oscNumber, s.num[:0]
			case esc:
				// ESC ESC: still waiting on the second.
			default:
				s.state = oscNormal
			}
		case oscNumber:
			switch {
			case c >= '0' && c <= '9' && len(s.num) < maxOSCNumber:
				s.num = append(s.num, c)
			case c == ';' && string(s.num) == strconv.Itoa(runnerwire.OSC7501):
				s.state, s.payload, s.overflow = oscPayload, s.payload[:0], false
			case c == bel:
				s.state = oscNormal
			case c == esc:
				s.state = oscOtherEsc
			default:
				s.state = oscOther
			}
		case oscPayload:
			switch c {
			case bel:
				out = s.end(out)
			case esc:
				s.state = oscPayloadEsc
			default:
				if len(s.payload) < maxOSCPayload {
					s.payload = append(s.payload, c)
				} else {
					s.overflow = true
				}
			}
		case oscPayloadEsc:
			if c == '\\' {
				out = s.end(out)
			} else {
				// An unterminated sequence cut short by another escape:
				// drop it, and read this byte afresh.
				s.state = oscNormal
				if c == esc {
					s.state = oscEsc
				}
			}
		case oscOther:
			switch c {
			case bel:
				s.state = oscNormal
			case esc:
				s.state = oscOtherEsc
			}
		case oscOtherEsc:
			s.state = oscNormal
			if c == esc {
				s.state = oscEsc
			}
		}
	}
	return out
}

func (s *oscScanner) end(out [][]byte) [][]byte {
	s.state = oscNormal
	if s.overflow {
		return out
	}
	return append(out, bytes.Clone(s.payload))
}

// maxAgentState bounds a state word; anything longer is not a status.
const maxAgentState = 64

// agentState reads an OSC 7501 payload: a JSON object with a "state" field,
// or a bare word. It returns "" for anything else.
func agentState(payload []byte) string {
	text := strings.TrimSpace(string(payload))
	var state string
	if strings.HasPrefix(text, "{") {
		var msg struct {
			State string `json:"state"`
		}
		if json.Unmarshal([]byte(text), &msg) != nil {
			return ""
		}
		state = strings.TrimSpace(msg.State)
	} else {
		state = text
	}
	if state == "" || len(state) > maxAgentState {
		return ""
	}
	for _, r := range state {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_-.: ", r)) {
			return ""
		}
	}
	return state
}

// plainText strips terminal escapes from raw TUI output, for a summary: CSI
// and OSC sequences and other escapes go, carriage returns end a line, and
// the rest of the control characters are dropped. The result is rough (a TUI
// draws with cursor moves) but readable.
func plainText(raw []byte) string {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == esc && i+1 < len(raw):
			i++
			switch raw[i] {
			case '[': // CSI: parameters, then a final byte 0x40–0x7e
				start := i + 1
				for i+1 < len(raw) {
					i++
					if raw[i] >= 0x40 && raw[i] <= 0x7e {
						break
					}
				}
				// TUIs move the cursor instead of writing spaces and newlines.
				switch raw[i] {
				case 'C': // cursor forward
					n, err := strconv.Atoi(string(raw[start:i]))
					if err != nil || n < 1 {
						n = 1
					}
					b.WriteString(strings.Repeat(" ", min(n, 200)))
				case 'H', 'f', 'B', 'E', 'd': // cursor to a position or down
					b.WriteByte('\n')
				}
			case ']', 'P', '_', '^': // OSC, DCS, APC, PM: up to BEL or ST
				for i+1 < len(raw) {
					i++
					if raw[i] == bel {
						break
					}
					if raw[i] == esc && i+1 < len(raw) && raw[i+1] == '\\' {
						i++
						break
					}
				}
			case '(', ')', '*', '+': // charset designation: one more byte
				i++
			}
		case c == esc:
		case c == '\r' || c == '\n':
			b.WriteByte('\n')
		case c == '\t':
			b.WriteByte(' ')
		case c < 0x20 || c == 0x7f:
		default:
			b.WriteByte(c)
		}
	}
	return strings.ToValidUTF8(b.String(), "")
}

// lastLines is the last n non-blank lines of text, each trimmed on the right.
func lastLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	var kept []string
	for i := len(lines) - 1; i >= 0 && len(kept) < n; i-- {
		if line := strings.TrimRightFunc(lines[i], unicode.IsSpace); strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return strings.Join(kept, "\n")
}
