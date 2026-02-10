package emulator

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxIntermediateLen = 64      // Cap intermediate string at 64 bytes
	maxOSCStringLen    = 64 * 1024 // Cap OSC string at 64KB
)

// ParserState represents the parser state machine state
type ParserState uint8

const (
	StateGround ParserState = iota
	StateEscape
	StateEscapeIntermediate
	StateCSI
	StateCSIParam
	StateCSIIntermediate
	StateOSC
	StateOSCString
	StateDCS
	StateDCSString
)

// Parser is an ANSI/xterm escape sequence parser
type Parser struct {
	state        ParserState
	screen       *Screen
	scrollback   *Scrollback
	params       []int
	intermediate string
	oscString    strings.Builder
	title        string
	onTitle      func(string)
	utf8Buf      [4]byte // Buffer for UTF-8 multi-byte sequences
	utf8Len      int     // Current bytes in utf8Buf
	utf8Need     int     // Total bytes needed for current sequence
}

// NewParser creates a new parser connected to a screen and scrollback
func NewParser(screen *Screen, scrollback *Scrollback) *Parser {
	return &Parser{
		state:      StateGround,
		screen:     screen,
		scrollback: scrollback,
		params:     make([]int, 0, 16),
	}
}

// SetOnTitle sets the callback for window title changes
func (p *Parser) SetOnTitle(fn func(string)) {
	p.onTitle = fn
}

// Title returns the current window title
func (p *Parser) Title() string {
	return p.title
}

// Screen returns the parser's screen
func (p *Parser) Screen() *Screen {
	return p.screen
}

// Scrollback returns the parser's scrollback
func (p *Parser) Scrollback() *Scrollback {
	return p.scrollback
}

// Parse processes a byte slice through the parser
func (p *Parser) Parse(data []byte) {
	for _, b := range data {
		p.parseByte(b)
	}
}

func (p *Parser) parseByte(b byte) {
	switch p.state {
	case StateGround:
		p.parseGround(b)
	case StateEscape:
		p.parseEscape(b)
	case StateEscapeIntermediate:
		p.parseEscapeIntermediate(b)
	case StateCSI:
		p.parseCSI(b)
	case StateCSIParam:
		p.parseCSIParam(b)
	case StateCSIIntermediate:
		p.parseCSIIntermediate(b)
	case StateOSC:
		p.parseOSC(b)
	case StateOSCString:
		p.parseOSCString(b)
	default:
		p.state = StateGround
		p.parseGround(b)
	}
}

func (p *Parser) parseGround(b byte) {
	// If we're in the middle of a UTF-8 sequence, continue it
	if p.utf8Need > 0 {
		if b >= 0x80 && b < 0xC0 { // Valid continuation byte
			p.utf8Buf[p.utf8Len] = b
			p.utf8Len++
			if p.utf8Len == p.utf8Need {
				// Complete sequence - decode and write
				r, _ := utf8.DecodeRune(p.utf8Buf[:p.utf8Len])
				if r != utf8.RuneError {
					p.screen.Write(r)
				}
				p.utf8Need = 0
				p.utf8Len = 0
			}
		} else {
			// Invalid continuation - reset and process this byte normally
			p.utf8Need = 0
			p.utf8Len = 0
			p.parseGroundByte(b)
		}
		return
	}

	p.parseGroundByte(b)
}

func (p *Parser) parseGroundByte(b byte) {
	switch {
	case b == 0x1b: // ESC
		p.state = StateEscape
	case b == 0x07: // BEL
		// Bell - ignore for now
	case b == 0x08: // BS
		if p.screen.cursor.X > 0 {
			p.screen.cursor.X--
		}
	case b == 0x09: // HT (tab)
		p.screen.cursor.X = (p.screen.cursor.X + 8) &^ 7
		cols, _ := p.screen.Size()
		if p.screen.cursor.X >= cols {
			p.screen.cursor.X = cols - 1
		}
	case b == 0x0a, b == 0x0b, b == 0x0c: // LF, VT, FF
		p.lineFeed()
	case b == 0x0d: // CR
		p.screen.cursor.X = 0
	case b >= 0x20 && b < 0x7f: // Printable ASCII
		p.screen.Write(rune(b))
	case b >= 0xC0 && b < 0xE0: // 2-byte UTF-8 start
		p.utf8Buf[0] = b
		p.utf8Len = 1
		p.utf8Need = 2
	case b >= 0xE0 && b < 0xF0: // 3-byte UTF-8 start
		p.utf8Buf[0] = b
		p.utf8Len = 1
		p.utf8Need = 3
	case b >= 0xF0 && b < 0xF8: // 4-byte UTF-8 start
		p.utf8Buf[0] = b
		p.utf8Len = 1
		p.utf8Need = 4
	}
}

func (p *Parser) lineFeed() {
	_, scrollBot := p.screen.ScrollRegion()
	if p.screen.cursor.Y >= scrollBot {
		scrolled := p.screen.ScrollUp(1)
		for _, line := range scrolled {
			p.scrollback.Push(line)
		}
	} else {
		p.screen.cursor.Y++
	}
}

func (p *Parser) parseEscape(b byte) {
	switch {
	case b == '[': // CSI
		p.state = StateCSI
		p.params = p.params[:0]
		p.intermediate = ""
	case b == ']': // OSC
		p.state = StateOSC
		p.oscString.Reset()
	case b == 'P': // DCS
		p.state = StateDCS
	case b == '\\': // ST
		p.state = StateGround
	case b == 'c': // RIS - Reset
		p.screen.Clear()
		p.screen.ResetAttrs()
		p.state = StateGround
	case b == 'D': // IND - Index (line feed)
		p.lineFeed()
		p.state = StateGround
	case b == 'E': // NEL - Next Line
		p.screen.cursor.X = 0
		p.lineFeed()
		p.state = StateGround
	case b == 'M': // RI - Reverse Index
		scrollTop, _ := p.screen.ScrollRegion()
		if p.screen.cursor.Y <= scrollTop {
			p.screen.ScrollDown(1)
		} else {
			p.screen.cursor.Y--
		}
		p.state = StateGround
	case b == '7': // DECSC - Save cursor
		// TODO: implement cursor save
		p.state = StateGround
	case b == '8': // DECRC - Restore cursor
		// TODO: implement cursor restore
		p.state = StateGround
	case b == '=': // DECKPAM
		p.state = StateGround
	case b == '>': // DECKPNM
		p.state = StateGround
	case b >= 0x20 && b <= 0x2f: // Intermediate
		p.intermediate = string(b)
		p.state = StateEscapeIntermediate
	default:
		p.state = StateGround
	}
}

func (p *Parser) parseEscapeIntermediate(b byte) {
	if b >= 0x20 && b <= 0x2f {
		if len(p.intermediate) >= maxIntermediateLen {
			p.intermediate = ""
			p.state = StateGround
			return
		}
		p.intermediate += string(b)
	} else if b >= 0x30 && b <= 0x7e {
		// Final byte - handle sequence
		p.state = StateGround
	} else {
		p.state = StateGround
	}
}

func (p *Parser) parseCSI(b byte) {
	if b >= '0' && b <= '9' {
		p.params = append(p.params, int(b-'0'))
		p.state = StateCSIParam
	} else if b == ';' {
		p.params = append(p.params, 0)
		p.state = StateCSIParam
	} else if b == '?' || b == '>' || b == '!' {
		p.intermediate = string(b)
		p.state = StateCSIParam
	} else if b >= 0x40 && b <= 0x7e {
		p.executeCSI(b)
		p.state = StateGround
	} else {
		p.state = StateGround
	}
}

func (p *Parser) parseCSIParam(b byte) {
	if b >= '0' && b <= '9' {
		if len(p.params) == 0 {
			p.params = append(p.params, 0)
		}
		p.params[len(p.params)-1] = p.params[len(p.params)-1]*10 + int(b-'0')
	} else if b == ';' {
		if len(p.params) == 0 {
			p.params = append(p.params, 0)
		}
		p.params = append(p.params, 0)
	} else if b == ':' {
		// Subparameter separator - used in SGR for underline styles
		// For now, treat like ';'
		if len(p.params) == 0 {
			p.params = append(p.params, 0)
		}
		p.params = append(p.params, 0)
	} else if b >= 0x20 && b <= 0x2f {
		if len(p.intermediate) >= maxIntermediateLen {
			p.intermediate = ""
			p.state = StateGround
			return
		}
		p.intermediate += string(b)
		p.state = StateCSIIntermediate
	} else if b >= 0x40 && b <= 0x7e {
		p.executeCSI(b)
		p.state = StateGround
	} else {
		p.state = StateGround
	}
}

func (p *Parser) parseCSIIntermediate(b byte) {
	if b >= 0x20 && b <= 0x2f {
		if len(p.intermediate) >= maxIntermediateLen {
			p.intermediate = ""
			p.state = StateGround
			return
		}
		p.intermediate += string(b)
	} else if b >= 0x40 && b <= 0x7e {
		p.executeCSI(b)
		p.state = StateGround
	} else {
		p.state = StateGround
	}
}

func (p *Parser) parseOSC(b byte) {
	if b >= '0' && b <= '9' {
		p.oscString.WriteByte(b)
	} else if b == ';' {
		p.oscString.WriteByte(b)
		p.state = StateOSCString
	} else if b == 0x07 || b == 0x1b { // BEL or ESC
		p.executeOSC()
		if b == 0x1b {
			p.state = StateEscape
		} else {
			p.state = StateGround
		}
	} else {
		p.state = StateGround
	}
}

func (p *Parser) parseOSCString(b byte) {
	if b == 0x07 { // BEL
		p.executeOSC()
		p.state = StateGround
	} else if b == 0x1b { // ESC
		p.state = StateEscape
		p.executeOSC()
	} else {
		if p.oscString.Len() >= maxOSCStringLen {
			p.oscString.Reset()
			p.state = StateGround
			return
		}
		p.oscString.WriteByte(b)
	}
}

func (p *Parser) executeCSI(final byte) {
	// Get parameters with defaults
	param := func(i, def int) int {
		if i < len(p.params) && p.params[i] > 0 {
			return p.params[i]
		}
		return def
	}

	cols, rows := p.screen.Size()

	switch final {
	case 'A': // CUU - Cursor Up
		n := param(0, 1)
		p.screen.cursor.Y = max(0, p.screen.cursor.Y-n)

	case 'B': // CUD - Cursor Down
		n := param(0, 1)
		p.screen.cursor.Y = min(rows-1, p.screen.cursor.Y+n)

	case 'C': // CUF - Cursor Forward
		n := param(0, 1)
		p.screen.cursor.X = min(cols-1, p.screen.cursor.X+n)

	case 'D': // CUB - Cursor Back
		n := param(0, 1)
		p.screen.cursor.X = max(0, p.screen.cursor.X-n)

	case 'E': // CNL - Cursor Next Line
		n := param(0, 1)
		p.screen.cursor.X = 0
		p.screen.cursor.Y = min(rows-1, p.screen.cursor.Y+n)

	case 'F': // CPL - Cursor Previous Line
		n := param(0, 1)
		p.screen.cursor.X = 0
		p.screen.cursor.Y = max(0, p.screen.cursor.Y-n)

	case 'G': // CHA - Cursor Horizontal Absolute
		n := param(0, 1)
		p.screen.cursor.X = clamp(n-1, 0, cols-1)

	case 'H', 'f': // CUP - Cursor Position
		row := param(0, 1)
		col := param(1, 1)
		p.screen.SetCursor(col-1, row-1)

	case 'J': // ED - Erase in Display
		mode := param(0, 0)
		p.screen.ClearScreen(mode)

	case 'K': // EL - Erase in Line
		mode := param(0, 0)
		p.screen.ClearLine(mode)

	case 'L': // IL - Insert Lines
		n := param(0, 1)
		p.screen.InsertLines(n)

	case 'M': // DL - Delete Lines
		n := param(0, 1)
		p.screen.DeleteLines(n)

	case 'P': // DCH - Delete Characters
		n := param(0, 1)
		p.screen.DeleteChars(n)

	case 'S': // SU - Scroll Up
		n := param(0, 1)
		scrolled := p.screen.ScrollUp(n)
		for _, line := range scrolled {
			p.scrollback.Push(line)
		}

	case 'T': // SD - Scroll Down
		n := param(0, 1)
		p.screen.ScrollDown(n)

	case 'X': // ECH - Erase Characters
		n := param(0, 1)
		for i := 0; i < n && p.screen.cursor.X+i < cols; i++ {
			p.screen.SetCell(p.screen.cursor.X+i, p.screen.cursor.Y, DefaultCell())
		}

	case '@': // ICH - Insert Characters
		n := param(0, 1)
		p.screen.InsertChars(n)

	case 'd': // VPA - Vertical Position Absolute
		n := param(0, 1)
		p.screen.cursor.Y = clamp(n-1, 0, rows-1)

	case 'h': // SM - Set Mode
		p.setMode(true)

	case 'l': // RM - Reset Mode
		p.setMode(false)

	case 'm': // SGR - Select Graphic Rendition
		p.executeSGR()

	case 'n': // DSR - Device Status Report
		// Ignore for now

	case 'r': // DECSTBM - Set Scrolling Region
		top := param(0, 1)
		bottom := param(1, rows)
		p.screen.SetScrollRegion(top-1, bottom-1)
		p.screen.SetCursor(0, 0)

	case 's': // SCP - Save Cursor Position
		// TODO: implement

	case 'u': // RCP - Restore Cursor Position
		// TODO: implement

	case 't': // Window manipulation
		// Ignore

	case 'c': // DA - Device Attributes
		// Ignore

	case 'q': // DECSCUSR - Set Cursor Style
		if p.intermediate == " " {
			style := param(0, 1)
			switch style {
			case 0, 1, 2:
				p.screen.SetCursorStyle(CursorBlock)
			case 3, 4:
				p.screen.SetCursorStyle(CursorUnderline)
			case 5, 6:
				p.screen.SetCursorStyle(CursorBar)
			}
		}
	}
}

func (p *Parser) setMode(set bool) {
	if len(p.intermediate) > 0 && p.intermediate[0] == '?' {
		// DEC private modes
		for _, mode := range p.params {
			switch mode {
			case 25: // DECTCEM - Cursor visible
				p.screen.SetCursorVisible(set)
			case 1049: // Alternate screen buffer
				// TODO: implement alternate screen
			}
		}
	}
}

func (p *Parser) executeSGR() {
	if len(p.params) == 0 {
		p.screen.ResetAttrs()
		return
	}

	attrs := p.screen.Attrs()
	i := 0
	for i < len(p.params) {
		param := p.params[i]
		i++

		switch param {
		case 0: // Reset
			attrs = DefaultCell()

		case 1: // Bold
			attrs.Attrs |= AttrBold

		case 2: // Dim
			attrs.Attrs |= AttrDim

		case 3: // Italic
			attrs.Attrs |= AttrItalic

		case 4: // Underline
			attrs.Attrs |= AttrUnderline

		case 5: // Blink
			attrs.Attrs |= AttrBlink

		case 7: // Reverse
			attrs.Attrs |= AttrReverse

		case 8: // Hidden
			attrs.Attrs |= AttrHidden

		case 9: // Strikethrough
			attrs.Attrs |= AttrStrikethrough

		case 21: // Double underline (treat as underline)
			attrs.Attrs |= AttrUnderline

		case 22: // Not bold, not dim
			attrs.Attrs &^= (AttrBold | AttrDim)

		case 23: // Not italic
			attrs.Attrs &^= AttrItalic

		case 24: // Not underlined
			attrs.Attrs &^= AttrUnderline

		case 25: // Not blinking
			attrs.Attrs &^= AttrBlink

		case 27: // Not reversed
			attrs.Attrs &^= AttrReverse

		case 28: // Not hidden
			attrs.Attrs &^= AttrHidden

		case 29: // Not strikethrough
			attrs.Attrs &^= AttrStrikethrough

		case 30, 31, 32, 33, 34, 35, 36, 37: // Standard FG colors
			attrs.FG = IndexedColor(uint8(param - 30))

		case 38: // Extended FG color
			i = p.parseExtendedColor(&attrs.FG, i)

		case 39: // Default FG
			attrs.FG = DefaultFG

		case 40, 41, 42, 43, 44, 45, 46, 47: // Standard BG colors
			attrs.BG = IndexedColor(uint8(param - 40))

		case 48: // Extended BG color
			i = p.parseExtendedColor(&attrs.BG, i)

		case 49: // Default BG
			attrs.BG = DefaultBG

		case 90, 91, 92, 93, 94, 95, 96, 97: // Bright FG colors
			attrs.FG = IndexedColor(uint8(param - 90 + 8))

		case 100, 101, 102, 103, 104, 105, 106, 107: // Bright BG colors
			attrs.BG = IndexedColor(uint8(param - 100 + 8))
		}
	}
	p.screen.SetAttrs(attrs)
}

func (p *Parser) parseExtendedColor(c *Color, i int) int {
	if i >= len(p.params) {
		return i
	}

	mode := p.params[i]
	i++

	switch mode {
	case 5: // 256-color
		if i < len(p.params) {
			*c = IndexedColor(uint8(p.params[i]))
			i++
		}
	case 2: // RGB
		if i+2 < len(p.params) {
			*c = RGBColor(
				uint8(p.params[i]),
				uint8(p.params[i+1]),
				uint8(p.params[i+2]),
			)
			i += 3
		}
	}
	return i
}

func (p *Parser) executeOSC() {
	s := p.oscString.String()
	p.oscString.Reset()

	parts := strings.SplitN(s, ";", 2)
	if len(parts) < 2 {
		return
	}

	cmd, err := strconv.Atoi(parts[0])
	if err != nil {
		return
	}

	switch cmd {
	case 0, 1, 2: // Set title
		p.title = parts[1]
		if p.onTitle != nil {
			p.onTitle(p.title)
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
