// Terminal emulator for agent TUI capture.
//
// The agent harnesses (codex/opencode) are full-screen TUIs: their PTY output
// is a stream of printable runes plus cursor/erase/scroll escape sequences.
// Reading that raw stream (and stripping escapes) yields noisy, redrawn text.
// Screen applies the stream to a virtual grid the way a real terminal would,
// so callers can read the *rendered* screen and diff it to extract a single
// response.
package agentsession

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Screen is a minimal ANSI terminal model.
type Screen struct {
	rows, cols     int
	grid           [][]rune
	curRow, curCol int
	savedRow       int
	savedCol       int
	pending        []byte // incomplete UTF-8 between Feed calls
}

// NewScreen creates a blank screen.
func NewScreen(rows, cols int) *Screen {
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	s := &Screen{rows: rows, cols: cols, grid: make([][]rune, rows)}
	for i := range s.grid {
		s.grid[i] = blankRow(cols)
	}
	return s
}

func blankRow(cols int) []rune {
	r := make([]rune, cols)
	for i := range r {
		r[i] = ' '
	}
	return r
}

// Feed applies a chunk of PTY output to the screen.
func (s *Screen) Feed(data []byte) {
	if len(s.pending) > 0 {
		data = append(s.pending, data...)
		s.pending = nil
	}
	for i := 0; i < len(data); {
		b := data[i]
		switch b {
		case 0x1b:
			n := s.consumeEscape(data[i:])
			if n == 0 {
				// Incomplete escape: buffer and wait for more.
				s.pending = append(s.pending, data[i:]...)
				return
			}
			i += n
		case '\r':
			s.curCol = 0
			i++
		case '\n':
			s.lineFeed()
			i++
		case '\b':
			if s.curCol > 0 {
				s.curCol--
			}
			i++
		case '\t':
			s.curCol = ((s.curCol / 8) + 1) * 8
			if s.curCol >= s.cols {
				s.curCol = s.cols - 1
			}
			i++
		default:
			if b < 0x20 {
				i++ // other control chars
				continue
			}
			r, size := utf8.DecodeRune(data[i:])
			if r == utf8.RuneError && size == 1 && !utf8.FullRune(data[i:]) {
				// Incomplete rune: buffer and wait.
				s.pending = append(s.pending, data[i:]...)
				return
			}
			s.put(r)
			i += size
		}
	}
}

// CursorPos returns the current cursor position (0-based).
func (s *Screen) CursorPos() (row, col int) { return s.curRow, s.curCol }

// Text renders the screen as lines with trailing whitespace and blank trailing
// rows removed.
func (s *Screen) Text() string {
	lines := make([]string, s.rows)
	for i := range s.grid {
		lines[i] = strings.TrimRight(string(s.grid[i]), " ")
	}
	// Drop trailing blank lines.
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[:end], "\n")
}

func (s *Screen) put(r rune) {
	if s.curCol >= s.cols {
		s.curCol = 0
		s.lineFeed()
	}
	s.grid[s.curRow][s.curCol] = r
	s.curCol++
}

func (s *Screen) lineFeed() {
	s.curRow++
	if s.curRow >= s.rows {
		s.scrollUp()
		s.curRow = s.rows - 1
	}
}

func (s *Screen) scrollUp() {
	copy(s.grid, s.grid[1:])
	s.grid[s.rows-1] = blankRow(s.cols)
}

func (s *Screen) clear() {
	for i := range s.grid {
		s.grid[i] = blankRow(s.cols)
	}
	s.curRow, s.curCol = 0, 0
}

// consumeEscape parses one escape sequence starting at data[0]==ESC.
// Returns the number of bytes consumed, or 0 if the sequence is incomplete.
func (s *Screen) consumeEscape(data []byte) int {
	if len(data) < 2 {
		return 0
	}
	switch data[1] {
	case '[':
		return s.consumeCSI(data)
	case ']':
		return consumeOSC(data)
	case '(', ')', '#':
		// Charset/line-drawing designators: ESC ( X → 3 bytes.
		if len(data) < 3 {
			return 0
		}
		return 3
	default:
		// Two-byte escape (ESC 7, ESC 8, ESC =, …): ignore.
		if data[1] == '7' {
			s.savedRow, s.savedCol = s.curRow, s.curCol
		} else if data[1] == '8' {
			s.curRow, s.curCol = s.savedRow, s.savedCol
		}
		return 2
	}
}

// consumeCSI parses ESC [ params final.
func (s *Screen) consumeCSI(data []byte) int {
	i := 2
	for i < len(data) && (data[i] == '?' || data[i] == '>' || data[i] == '!' || (data[i] >= '0' && data[i] <= '9') || data[i] == ';' || data[i] == ':') {
		i++
	}
	if i >= len(data) {
		return 0 // incomplete
	}
	final := data[i]
	params := string(data[2:i])
	consumed := i + 1
	s.applyCSI(params, final)
	return consumed
}

func (s *Screen) applyCSI(params string, final byte) {
	// Strip a leading '?' / '>' private marker for number parsing.
	private := strings.HasPrefix(params, "?")
	p := strings.TrimLeft(params, "?>!")
	nums := parseCSIArgs(p)
	n := func(idx, def int) int {
		if idx < len(nums) {
			return nums[idx]
		}
		return def
	}
	switch final {
	case 'A': // up
		s.curRow -= max1(n(0, 1))
		if s.curRow < 0 {
			s.curRow = 0
		}
	case 'B': // down
		s.curRow += max1(n(0, 1))
		if s.curRow >= s.rows {
			s.curRow = s.rows - 1
		}
	case 'C': // right
		s.curCol += max1(n(0, 1))
		if s.curCol >= s.cols {
			s.curCol = s.cols - 1
		}
	case 'D': // left
		s.curCol -= max1(n(0, 1))
		if s.curCol < 0 {
			s.curCol = 0
		}
	case 'E': // next line
		s.curRow += max1(n(0, 1))
		s.curCol = 0
		if s.curRow >= s.rows {
			s.curRow = s.rows - 1
		}
	case 'F': // previous line
		s.curRow -= max1(n(0, 1))
		s.curCol = 0
		if s.curRow < 0 {
			s.curRow = 0
		}
	case 'G': // column
		s.curCol = clamp(n(0, 1)-1, 0, s.cols-1)
	case 'H', 'f': // position
		s.curRow = clamp(n(0, 1)-1, 0, s.rows-1)
		s.curCol = clamp(n(1, 1)-1, 0, s.cols-1)
	case 'd': // row
		s.curRow = clamp(n(0, 1)-1, 0, s.rows-1)
	case 'J': // erase display
		switch n(0, 0) {
		case 2, 3:
			s.clear()
		case 0:
			s.eraseDisplayFromCursor()
		}
	case 'K': // erase line
		switch n(0, 0) {
		case 2:
			s.grid[s.curRow] = blankRow(s.cols)
		default:
			s.eraseLineFromCursor()
		}
	case 'S': // scroll up
		for k := 0; k < max1(n(0, 1)); k++ {
			s.scrollUp()
		}
	case 'T': // scroll down
		for k := 0; k < max1(n(0, 1)); k++ {
			s.scrollDown()
		}
	case 's':
		s.savedRow, s.savedCol = s.curRow, s.curCol
	case 'u':
		s.curRow, s.curCol = s.savedRow, s.savedCol
	case 'h', 'l':
		// Private modes (alt screen, cursor visibility, …). On entering the
		// alternate screen (1049h/1047h) reset to a clean slate.
		if private && final == 'h' && (containsInt(nums, 1049) || containsInt(nums, 1047)) {
			s.clear()
		}
		// Everything else (colors, mouse, bracketed paste) is ignored.
	}
}

func (s *Screen) scrollDown() {
	copy(s.grid[1:], s.grid[:s.rows-1])
	s.grid[0] = blankRow(s.cols)
}

func (s *Screen) eraseDisplayFromCursor() {
	for c := s.curCol; c < s.cols; c++ {
		s.grid[s.curRow][c] = ' '
	}
	for r := s.curRow + 1; r < s.rows; r++ {
		s.grid[r] = blankRow(s.cols)
	}
}

func (s *Screen) eraseLineFromCursor() {
	for c := s.curCol; c < s.cols; c++ {
		s.grid[s.curRow][c] = ' '
	}
}

func consumeOSC(data []byte) int {
	// ESC ] … terminated by BEL or ESC \.
	for i := 2; i < len(data); i++ {
		if data[i] == 0x07 {
			return i + 1
		}
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
			return i + 2
		}
	}
	return 0 // incomplete
}

func parseCSIArgs(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ':' })
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			out = append(out, 0)
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, v)
	}
	return out
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func max1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
