package util

// Utilities to strip ANSI escape codes and terminal control sequences from byte streams.
// This is useful when persisting PTY output to log files where human-readable text is desired.

const (
	ansiStateNormal = iota
	ansiStateEsc
	ansiStateCSI
	ansiStateOSC // OSC, also used for APC/PM/DCS style string terminators with BEL or ST
	ansiStateDCS
	ansiStateAPC
	ansiStatePM
)

// ANSIStripper removes ANSI/VT100 control sequences from a byte stream.
// It maintains minimal state so it can be fed chunked data safely.
type ANSIStripper struct {
	state  int
	oscEsc bool // tracks ESC seen inside OSC/DCS/APC/PM strings to detect ST (ESC \\)
}

// NewANSIStripper creates a new stateful stripper.
func NewANSIStripper() *ANSIStripper { return &ANSIStripper{} }

// Strip processes b and returns a new slice with control sequences removed.
// It preserves \n, \r and \t, and drops other C0 controls like BEL when not meaningful.
func (s *ANSIStripper) Strip(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch s.state {
		case ansiStateNormal:
			if c == 0x1b { // ESC
				s.state = ansiStateEsc
				continue
			}
			if c < 0x20 { // C0 controls
				// Keep only TAB, LF, CR
				if c == '\n' || c == '\r' || c == '\t' {
					out = append(out, c)
				}
				// Drop others (e.g., BEL 0x07)
				continue
			}
			out = append(out, c)

		case ansiStateEsc:
			// Determine escape family
			switch c {
			case '[':
				s.state = ansiStateCSI
			case ']':
				s.state = ansiStateOSC
				s.oscEsc = false
			case 'P':
				s.state = ansiStateDCS
				s.oscEsc = false
			case '_':
				s.state = ansiStateAPC
				s.oscEsc = false
			case '^':
				s.state = ansiStatePM
				s.oscEsc = false
			default:
				// One-char escapes like ESC=, ESC>, ESC7/8 etc. Drop them.
				s.state = ansiStateNormal
			}
			continue

		case ansiStateCSI:
			// Consume until a final byte 0x40..0x7E
			if c >= 0x40 && c <= 0x7e {
				s.state = ansiStateNormal
			}
			continue

		case ansiStateOSC, ansiStateDCS, ansiStateAPC, ansiStatePM:
			// Terminate on BEL or ST (ESC \\)
			if c == 0x07 { // BEL
				s.state = ansiStateNormal
				s.oscEsc = false
				continue
			}
			if s.oscEsc {
				// Previous byte was ESC inside OSC-like string
				if c == '\\' { // ST
					s.state = ansiStateNormal
				}
				s.oscEsc = false
				continue
			}
			if c == 0x1b { // ESC, could start ST
				s.oscEsc = true
			}
			// Otherwise, consume without output
			continue
		}
	}
	return out
}

// StripANSIEscapes is a convenience for single-shot sanitization without cross-chunk state.
// Prefer using ANSIStripper for streaming where escape sequences can span chunks.
func StripANSIEscapes(b []byte) []byte {
	s := NewANSIStripper()
	return s.Strip(b)
}
