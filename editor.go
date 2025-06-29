/*
	Editor is a simple terminal editor.
*/

package editor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"

	"golang.org/x/sys/unix"
)

/*-----------------------------------------------------------------------------
 * Types
 */

type line struct {
	chars  []rune // a line of text
	render []rune // contain the actual characters to draw on the screen for the line of text
}

type point struct {
	x int // x position
	y int // y position
}

type config struct {
	orgTermios       unix.Termios   // termios structure
	termRows         int            // number of terminal rows
	termCols         int            // number of terminal columns
	cursor           point          // cursors x & y position
	rx               int            // the x position (index) into line.render
	lines            []line         // lines of text
	fileY            int            // current line in text the user is scrolled to
	fileX            int            // current colum in the text the user is scrolled to
	tabStop          int            // number of spaces in a tab
	fileName         string         // name of edited file
	statusMsg        string         // status message
	statusMsgTime    time.Time      // timestamp of the status message
	statusMsgTimeout float64        // Timeout for the status message
	dirty            bool           // dirty flag, true if the file has been edited
	quitComfirm      bool           // confirm quit if the file is dirty
	searchPoints     []point        // x and y positions of search results
	searchCursor     point          // the cursor point when a search is started
	signals          chan os.Signal // channel for resize signals
}

/*-----------------------------------------------------------------------------
 * Global variables & constants
 */

var editor config
var errNoInput = errors.New("no input")

const version = "1.0.0"

const (
	kBackSpace  = 127
	kArrowUp    = 1000
	kArrowDown  = 1001
	kArrowLeft  = 1002
	kArrowRight = 1003
	kPageUp     = 1004
	kPageDown   = 1005
	kHome       = 1006
	kEnd        = 1007
	kDelete     = 1008
)

var keymap map[int]string

type KeyCombo struct {
	Ctrl  bool
	Alt   bool
	Shift bool
	Key   rune // or int for special keys
}

/*-----------------------------------------------------------------------------
 * Terminal operations
 */

func ctrlKey(b byte) int {
	return int(b & 0x1f)
}

func windowSize() (int, int, error) {
	ws, err := unix.IoctlGetWinsize(unix.Stdout, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	return int(ws.Row), int(ws.Col), nil
}

func clearTerminal() {
	scrBuf := bytes.Buffer{} // screen buffer

	fmt.Fprint(&scrBuf, "\x1b[?25l") // hide cursor
	fmt.Fprint(&scrBuf, "\x1b[H")    // cursor top-left corner

	for y := 0; y <= editor.termRows+1; y++ {
		fmt.Fprintf(&scrBuf, "\x1b[K") // clear to end of line
		fmt.Fprint(&scrBuf, "\r\n")
	}
	fmt.Fprint(&scrBuf, "\x1b[H")    // cursor top-left corner
	fmt.Fprint(&scrBuf, "\x1b[?25h") // show cursor

	os.Stdout.Write(scrBuf.Bytes()) // write screen buffer to stdout
}

func cleanupBeforeExit() {
	clearTerminal()
	err := disableRawMode()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error disable raw mode %s", err)
		os.Exit(1)
	}
	editor.signals <- syscall.SIGABRT
}

func resizeWindow() {
	rows, cols, err := windowSize()
	if err != nil {
		panic(err)
	}

	editor.termRows = rows - 2
	editor.termCols = cols
}

/*-----------------------------------------------------------------------------
 * Draw operations
 */

func drawRows(scrBuf *bytes.Buffer) {

	for y := 0; y < editor.termRows; y++ {
		fileLine := y + editor.fileY

		if fileLine >= len(editor.lines) {
			if len(editor.lines) == 0 && y == editor.termRows/3 {
				msg := fmt.Sprintf("Simple editor. Version %s", version)
				msglen := len(msg)

				if msglen > editor.termCols {
					msg = msg[:editor.termCols]
					msglen = editor.termCols
				}
				padding := (editor.termCols - msglen) / 2

				if padding > 0 {
					fmt.Fprint(scrBuf, "~")
					padding--
				}
				for i := 0; i < padding; i++ {
					fmt.Fprint(scrBuf, " ")
				}
				fmt.Fprint(scrBuf, msg)
			} else {
				fmt.Fprintf(scrBuf, "~")
			}
		} else {
			lineLen := len(editor.lines[fileLine].render) - editor.fileX
			if lineLen < 0 {
				lineLen = 0
			}

			if lineLen > editor.termCols { // truncate if lines go past the end of screen
				lineLen = editor.termCols
			}

			if lineLen > 0 {
				fmt.Fprint(scrBuf, string(editor.lines[fileLine].render[editor.fileX:editor.fileX+lineLen]))
			}
		}

		fmt.Fprintf(scrBuf, "\x1b[K") // clear to end of line
		fmt.Fprint(scrBuf, "\r\n")

	}
}

func drawStatusBar(scrBuf *bytes.Buffer) {
	var leftStatusString string

	fileName := editor.fileName
	if fileName == "" {
		fileName = "No Name"
	}

	if editor.dirty {
		dirtyChar := '*'
		leftStatusString = fmt.Sprintf("[%c%.20s] - %d lines", dirtyChar, fileName, len(editor.lines))
	} else {
		leftStatusString = fmt.Sprintf("[%.20s] - %d lines", fileName, len(editor.lines))
	}

	rightStatusString := fmt.Sprintf("L%d,C%d", editor.cursor.y+1, editor.cursor.x+1)

	numSpaces := editor.termCols - len(leftStatusString) - len(rightStatusString)

	fmt.Fprint(scrBuf, "\x1b[7m") // invert colour

	if numSpaces >= 0 {
		fmt.Fprint(scrBuf, leftStatusString+strings.Repeat(" ", numSpaces)+rightStatusString)
	} else {
		fmt.Fprint(scrBuf, (leftStatusString + rightStatusString)[:editor.termCols])
	}

	fmt.Fprint(scrBuf, "\x1b[m") // normal colour
	fmt.Fprint(scrBuf, "\r\n")
}

func drawStatusMsg(scrBuf *bytes.Buffer) {
	fmt.Fprint(scrBuf, "\x1b[K") // clear the line

	if time.Since(editor.statusMsgTime).Seconds() < editor.statusMsgTimeout {
		if len(editor.statusMsg) < editor.termCols {
			fmt.Fprint(scrBuf, editor.statusMsg)
		} else {
			fmt.Fprint(scrBuf, editor.statusMsg[:editor.termCols])
		}
	}
}

func setStatusMsg(format string, a ...interface{}) {
	editor.statusMsg = fmt.Sprintf(format, a...)
	editor.statusMsgTime = time.Now()
}

/*-----------------------------------------------------------------------------
 * Prompt
 */

func prompt(prompt string) string {
	var input []byte

	for {
		setStatusMsg(prompt, input)
		refreshScreen()
		k, err := readKey()
		if err != nil {
			return fmt.Sprintf("%v", err)
		}

		if k == kDelete || k == ctrlKey('h') || k == kBackSpace {
			if len(input) > 0 {
				input = input[:len(input)-1]
			}
		} else if k == '\x1b' {
			setStatusMsg("")
			return ""
		} else if k == '\r' {
			setStatusMsg("")
			break
		} else if unicode.IsPrint(rune(k)) {
			input = append(input, byte(k))
		}
	}

	return string(input)
}

/*-----------------------------------------------------------------------------
 * Open File
 */

func open_file() {
	file := prompt("Open file: %s")
	err := openFile(file)
	if err != nil {
		setStatusMsg("Failed to open file %s", file)
	}
}

/*-----------------------------------------------------------------------------
 * Find
 */

func find() {

	query := prompt("Search: %s")

	if query == "" {
		return
	}

	editor.searchPoints = []point{}

	for row, line := range editor.lines {
		points := searchPoints(row+1, string(line.chars), query)

		if len(points) != 0 {
			editor.searchPoints = append(editor.searchPoints, points...)
		}
	}

	if len(editor.searchPoints) == 0 {
		setStatusMsg("No match found.")
		return
	}

	/* Save the current position in the file. */
	editor.searchCursor.x = editor.cursor.x
	editor.searchCursor.y = editor.cursor.y

	setCursor(editor.searchPoints[0])
	setStatusMsg("Use arrow keys to move, ESC or ENTER to exit.")

	point := 0
findLoop:
	for {
		refreshScreen()
		k, err := readKey()
		if err != nil {
			break findLoop
		}
		switch k {
		case kArrowDown, kArrowRight:
			point++
			if point > len(editor.searchPoints)-1 {
				point = 0
			}
			setCursor(editor.searchPoints[point])
		case kArrowUp, kArrowLeft:
			point--
			if point < 0 {
				point = len(editor.searchPoints) - 1
			}
			setCursor(editor.searchPoints[point])

		case '\x1b':
			setStatusMsg("Esc")
			setCursor(editor.searchCursor)
			break findLoop

		case '\r':
			setStatusMsg("")
			break findLoop
		}
	}
}

func searchPoints(row int, str string, substr string) []point {
	points := []point{}
	s := str

	if substr == "" {
		return points
	}

	for {
		i := strings.Index(s, substr)
		if i == -1 {
			break
		}

		s = s[i+len(substr):]
		points = append(points, point{y: row - 1, x: len(str) - len(s) - len(substr)})
	}
	return points
}

/*-----------------------------------------------------------------------------
 * Screen Operations
 */

func computeRx(row []rune, x int) int {
	rx := 0
	for i := 0; i < x; i++ {
		if row[i] == '\t' {
			rx = rx + editor.tabStop - 1
		}
		rx++
	}

	return rx
}

func scroll() {

	editor.rx = 0

	if editor.cursor.y < len(editor.lines) {
		editor.rx = computeRx(editor.lines[editor.cursor.y].chars, editor.cursor.x)
	}

	/* check if the cursor is above the visible window */
	if editor.cursor.y < editor.fileY {
		editor.fileY = editor.cursor.y
	}

	/* check if the cursor is past the bottom of the visible window */
	if editor.cursor.y >= editor.fileY+editor.termRows {
		editor.fileY = editor.cursor.y - editor.termRows + 1
	}

	/* check if the cursor is to the left of the visible window */
	if editor.rx < editor.fileX {
		editor.fileX = editor.rx
	}

	/* check if the cursor is to the right of the visible window */
	if editor.rx >= editor.fileX+editor.termCols {
		editor.fileX = editor.rx - editor.termCols + 1
	}
}

func refreshScreen() {
	scrBuf := bytes.Buffer{} // screen buffer

	scroll()

	fmt.Fprint(&scrBuf, "\x1b[?25l") // hide cursor
	fmt.Fprint(&scrBuf, "\x1b[H")    // cursor top-left corner

	drawRows(&scrBuf)
	drawStatusBar(&scrBuf)
	drawStatusMsg(&scrBuf)

	// reposition cursor
	fmt.Fprintf(&scrBuf, "\x1b[%d;%dH",
		editor.cursor.y-editor.fileY+1,
		editor.rx-editor.fileX+1)

	fmt.Fprint(&scrBuf, "\x1b[?25h") // show cursor

	os.Stdout.Write(scrBuf.Bytes()) // write screen buffer to stdout
}

func updateRow(src []rune) []rune {
	tabSpaces := []rune(strings.Repeat(" ", editor.tabStop))
	dest := []rune{}

	for _, r := range src {
		switch r {
		case '\t':
			dest = append(dest, tabSpaces...)
		default:
			dest = append(dest, r)
		}
	}
	return dest
}

func moveCursor(key int) {

	endOfFile := editor.cursor.y >= len(editor.lines)

	switch key {
	case kArrowLeft:
		if editor.cursor.x > 0 {
			editor.cursor.x--
		} else if editor.cursor.y > 0 {
			/* if we are at the beginning of a line then move to the end of the previous line */
			editor.cursor.y--
			editor.cursor.x = len(editor.lines[editor.cursor.y].chars)
		}
	case kArrowRight:
		if !endOfFile {
			if editor.cursor.x < len(editor.lines[editor.cursor.y].chars) {
				editor.cursor.x++
			} else if editor.cursor.x == len(editor.lines[editor.cursor.y].chars) {
				/* if we are at the end of a line then move to the start of the next line */
				editor.cursor.y++
				editor.cursor.x = 0
			}
		}
	case kArrowDown:
		if editor.cursor.y < len(editor.lines) {
			editor.cursor.y++
		}
	case kArrowUp:
		if editor.cursor.y > 0 {
			editor.cursor.y--
		}
	}

	/* snap cursor to end of line */
	endOfFile = editor.cursor.y >= len(editor.lines)
	rowLen := 0
	if !endOfFile {
		rowLen = len(editor.lines[editor.cursor.y].chars)
	}
	if editor.cursor.x > rowLen {
		editor.cursor.x = rowLen
	}
}

func setCursor(p point) {
	editor.cursor.x = p.x
	editor.cursor.y = p.y
}

/*-----------------------------------------------------------------------------
 * Match operations
 */

func paren(left rune, right rune) (point, error) {
	var depth = 0
	p := point{}
	x := 0
	startFromCursor := true

	for y := editor.cursor.y; y >= 0; y-- {
		line := editor.lines[y]

		if startFromCursor {
			// start search from the position befor the cursor
			x = editor.cursor.x - 1
			startFromCursor = false

		} else {
			// otherwise start search from the last character in the line
			x = len(line.chars) - 1
		}

		if len(line.chars) == 0 {
			// skip empty lines
			continue
		}

		for i := x; i >= 0; i-- {
			if line.chars[i] == right {
				depth++
			} else if line.chars[i] == left {
				depth--
			}
			if depth == 0 {
				p.x = i
				p.y = y
				return p, nil
			}
		}
	}

	return p, fmt.Errorf("no matching parenthesis found")
}

func matchParenthesis(left rune, right rune) {
	c := editor.cursor

	p, err := paren(left, right)

	if err != nil {
		setStatusMsg("No matching parenthesis found")
	} else {
		editor.cursor = p
		refreshScreen()
		time.Sleep(300000 * time.Microsecond)
		editor.cursor = c

	}
}

/*-----------------------------------------------------------------------------
 * Insert operations
 */

func rowInsertChar(row []rune, col int, c int) []rune {
	if col < 0 || col > len(row) {
		return row
	}

	row = append(row, 0)
	copy(row[col+1:], row[col:])
	row[col] = rune(c)
	return row
}

func insertChar(key int) {
	if editor.cursor.y == len(editor.lines) {
		insertRow(len(editor.lines), "")
	}
	editor.lines[editor.cursor.y].chars = rowInsertChar(editor.lines[editor.cursor.y].chars, editor.cursor.x, key)
	editor.lines[editor.cursor.y].render = updateRow(editor.lines[editor.cursor.y].chars)
	editor.cursor.x++
	editor.dirty = true
}

func insertRow(row int, s string) {
	if row < 0 || row > len(editor.lines) {
		return
	}

	rns := []rune(s)
	nrow := line{chars: rns, render: updateRow(rns)}

	editor.lines = append(editor.lines, line{})
	copy(editor.lines[row+1:], editor.lines[row:])
	editor.lines[row] = nrow
	editor.dirty = true
}

func insertNewLine() {
	if editor.cursor.x == 0 {
		insertRow(editor.cursor.y, "")

	} else {

		moveChars := string(editor.lines[editor.cursor.y].chars[editor.cursor.x:])

		editor.lines[editor.cursor.y].chars = editor.lines[editor.cursor.y].chars[:editor.cursor.x]
		editor.lines[editor.cursor.y].render = updateRow(editor.lines[editor.cursor.y].chars)

		insertRow(editor.cursor.y+1, moveChars)
	}
	editor.cursor.y++
	editor.cursor.x = 0
}

/*-----------------------------------------------------------------------------
 * Delete operations
 */

func deleteRow(row int) {
	if row < 0 || row >= len(editor.lines) {
		return
	}

	copy(editor.lines[row:], editor.lines[row+1:])
	editor.lines = editor.lines[:len(editor.lines)-1]
	editor.dirty = true
}

func rowDeleteChar(row []rune, col int) []rune {
	if col < 0 || col >= len(row) {
		return row
	}

	copy(row[col:], row[col+1:])
	row = row[:len(row)-1]
	return row
}

func deleteChar() {
	if editor.cursor.y == len(editor.lines) {
		return
	}

	if editor.cursor.x == 0 && editor.cursor.y == 0 {
		return
	}

	if editor.cursor.x > 0 {
		editor.lines[editor.cursor.y].chars = rowDeleteChar(editor.lines[editor.cursor.y].chars, editor.cursor.x-1)
		editor.lines[editor.cursor.y].render = updateRow(editor.lines[editor.cursor.y].chars)
		editor.cursor.x--
	} else {
		editor.cursor.x = len(editor.lines[editor.cursor.y-1].chars)
		editor.lines[editor.cursor.y-1].chars = append(editor.lines[editor.cursor.y-1].chars, editor.lines[editor.cursor.y].chars...)
		editor.lines[editor.cursor.y-1].render = updateRow(editor.lines[editor.cursor.y-1].chars)
		deleteRow(editor.cursor.y)
		editor.cursor.y--
	}

	editor.dirty = true
}

/*-----------------------------------------------------------------------------
 * Handle user input & key map
 */

func rawReadKey() (byte, error) {
	k := []byte{0}
	n, err := os.Stdin.Read(k)
	switch {
	case err == io.EOF:
		return 0, errNoInput
	case err != nil:
		return 0, err
	case n == 0:
		return 0, errNoInput
	default:
		return k[0], nil
	}
}

func readKey() (int, error) {

	for {
		key, err := rawReadKey()
		switch {
		case err == errNoInput:
			continue
		case err == io.EOF:
			return 0, err
		case err != nil:
			return 0, fmt.Errorf("reading key %s", err)
		case key == '\x1b': // escape character 27
			esc0, err := rawReadKey()
			if err == errNoInput {
				return '\x1b', nil
			}
			if err != nil {
				return 0, err
			}
			esc1, err := rawReadKey()
			if err == errNoInput {
				return '\x1b', err
			}
			if err != nil {
				return 0, err
			}

			if esc0 == '[' {
				if esc1 >= '0' && esc1 <= '9' {
					esc2, err := rawReadKey()
					if err == errNoInput {
						return '\x1b', err
					}
					if esc2 == '~' {
						switch esc1 {
						case '5':
							return kPageUp, nil // fn+kArrowUp
						case '6':
							return kPageDown, nil // fn+kArrowDown
						case '3':
							return kDelete, nil
						}
					}
					if esc2 == ';' {
						esc3, err1 := rawReadKey()
						esc4, err2 := rawReadKey()
						if err1 == errNoInput {
							return '\x1b', err1
						}
						if err2 == errNoInput {
							return '\x1b', err2
						}
						if esc3 == '2' {
							switch esc4 { // shift + arrow keys
							case 'A':
								return kArrowUp, nil
							case 'B':
								return kArrowDown, nil
							case 'D':
								return kArrowLeft, nil
							case 'C':
								return kArrowRight, nil
							}
						}
					}

				} else {
					switch {
					case esc1 == 'A':
						return kArrowUp, nil
					case esc1 == 'B':
						return kArrowDown, nil
					case esc1 == 'C':
						return kArrowRight, nil
					case esc1 == 'D':
						return kArrowLeft, nil
					case esc1 == 'H':
						return kHome, nil // fn+kArrowLeft
					case esc1 == 'F':
						return kEnd, nil // fn+kArrowRight
					}
				}
			}

		case key == 195: // swedish characters
			esc1, err := rawReadKey()
			if err == errNoInput {
				return '\x1b', err
			}
			if err != nil {
				return 0, err
			}

			switch {
			case esc1 == 165:
				return 'å', nil
			case esc1 == 164:
				return 'ä', nil
			case esc1 == 182:
				return 'ö', nil
			case esc1 == 133:
				return 'Å', nil
			case esc1 == 132:
				return 'Ä', nil
			case esc1 == 150:
				return 'Ö', nil
			}

		default:
			return int(key), nil
		}
	}
}

func parseKeyCombo(s string) (KeyCombo, error) {
	var kc KeyCombo
	parts := strings.Split(strings.ToLower(s), "+")
	for _, p := range parts {
		switch p {
		case "ctrl":
			kc.Ctrl = true
		case "alt":
			kc.Alt = true
		case "shift":
			kc.Shift = true
		case "up":
			kc.Key = kArrowUp
		case "down":
			kc.Key = kArrowDown
		case "left":
			kc.Key = kArrowLeft
		case "right":
			kc.Key = kArrowRight
		case "pageup":
			kc.Key = kPageUp
		case "pagedown":
			kc.Key = kPageDown
		case "home":
			kc.Key = kHome
		case "end":
			kc.Key = kEnd
		case "backspace":
			kc.Key = kBackSpace
		case "delete":
			kc.Key = kDelete
		default:
			if len(p) == 1 {
				kc.Key = rune(p[0])
			} else {
				return kc, fmt.Errorf("unknown key part: %s", p)
			}
		}
	}
	return kc, nil
}

func keyComboToInt(kc KeyCombo) int {
	if kc.Ctrl && kc.Key >= 'a' && kc.Key <= 'z' {
		return ctrlKey(byte(kc.Key))
	}
	// Extend for Alt, Shift, special keys as needed
	return int(kc.Key)
}

func loadKeymap(filename string) error {
	var rawmap map[string]string
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &rawmap); err != nil {
		return err
	}
	keymap = make(map[int]string)
	for keystr, cmd := range rawmap {
		kc, err := parseKeyCombo(keystr)
		if err != nil {
			return fmt.Errorf("error parsing key %s: %v", keystr, err)
		}
		kint := keyComboToInt(kc)
		keymap[kint] = cmd
	}
	return nil
}

var actionDispatch = map[string]func(readonly bool){
	"quit": func(_ bool) { editor.quitComfirm = true },
	"help": func(_ bool) { help() },
	"save": func(readonly bool) {
		if !readonly {
			save()
		}
	},
	"find":       func(_ bool) { find() },
	"line_start": func(_ bool) { editor.cursor.x = 0 },
	"line_end": func(_ bool) {
		if editor.cursor.y < len(editor.lines) {
			editor.cursor.x = len(editor.lines[editor.cursor.y].chars)
		}
	},
	"delete_forward": func(readonly bool) {
		if !readonly {
			moveCursor(kArrowRight)
			deleteChar()
		}
	},
	"kill_line": func(readonly bool) {
		if !readonly {
			for {
				if editor.cursor.x >= len(editor.lines[editor.cursor.y].chars) {
					break
				}
				moveCursor(kArrowRight)
				deleteChar()
			}
		}
	},
	"open_file":    func(_ bool) { open_file() },
	"cursor_up":    func(_ bool) { moveCursor(kArrowUp) },
	"cursor_down":  func(_ bool) { moveCursor(kArrowDown) },
	"cursor_left":  func(_ bool) { moveCursor(kArrowLeft) },
	"cursor_right": func(_ bool) { moveCursor(kArrowRight) },
	"page_up": func(_ bool) {
		editor.cursor.y = editor.fileY
		for i := 0; i < editor.termRows; i++ {
			moveCursor(kArrowUp)
		}
	},
	"page_down": func(_ bool) {
		editor.cursor.y = editor.fileY + editor.termRows - 1
		if editor.cursor.y > len(editor.lines) {
			editor.cursor.y = len(editor.lines)
		}
		for i := 0; i < editor.termRows; i++ {
			moveCursor(kArrowDown)
		}
	},
}

func processKey(readonly bool) (bool, error) {
	k, err := readKey()
	if err != nil {
		return true, err
	}

	action, exists := keymap[k]

	switch {
	case !exists || action == "":
		// fallback behavior for unbound key
		if unicode.IsPrint(rune(k)) && !readonly {
			insertChar(k)
		} else if k == '\r' && !readonly {
			insertNewLine()
		} else if k == ')' && !readonly {
			insertChar(k)
			matchParenthesis('(', ')')
		} else if k == '}' && !readonly {
			insertChar(k)
			matchParenthesis('{', '}')
		} else if k == ']' && !readonly {
			insertChar(k)
			matchParenthesis('[', ']')
		} else if k == '\t' && !readonly {
			insertChar(k)
		} else if k == kBackSpace && !readonly {
			deleteChar()
		}
	case action == "quit":
		if editor.dirty && !editor.quitComfirm {
			setStatusMsg("There are unsaved changes. Press ctrl-q to quit or ctrl-s to save.")
			editor.quitComfirm = true
			return false, nil
		}
		return true, nil
	default:
		if f, ok := actionDispatch[action]; ok {
			f(readonly)
		}
	}

	return false, nil
}

/*-----------------------------------------------------------------------------
 * Help
 */

func help() {
	// Todo
}

/*-----------------------------------------------------------------------------
 * Save to file
 */

func linesToString() string {
	var sb strings.Builder

	for _, rows := range editor.lines {
		sb.WriteString(string(rows.chars))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func save() {

	if editor.fileName == "" {
		editor.fileName = prompt("Save as: %s")
		if editor.fileName == "" {
			setStatusMsg("Save cancelled")
			return
		}
	}

	f, err := os.Create(editor.fileName)
	if err != nil {
		setStatusMsg("error creating file: %s: %s", err, editor.fileName)
		return
	}
	defer f.Close()

	n, err := fmt.Fprint(f, linesToString())
	if err != nil {
		setStatusMsg("error writing to file: %s: %s", err, editor.fileName)
		return
	}
	setStatusMsg("%d bytes written to disk", n)
	editor.dirty = false
}

/*-----------------------------------------------------------------------------
 * Open file
 */

func openFile(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()

	editor.lines = []line{}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		insertRow(len(editor.lines), scanner.Text())
	}
	editor.fileName = name
	editor.dirty = false

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

/*-----------------------------------------------------------------------------
 * Open Data
 */

func openData(data []byte) error {
	editor.lines = []line{}
	reader := bytes.NewReader(data)

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		insertRow(len(editor.lines), scanner.Text())
	}
	editor.fileName = "memory" // or set to something meaningful
	editor.dirty = false

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

/*-----------------------------------------------------------------------------
 * Initialize editor
 */

func initialize(readonly bool, keymapPath string) error {

	resizeWindow()
	editor.cursor.x = 0
	editor.cursor.y = 0
	editor.tabStop = 4
	editor.statusMsgTimeout = 3

	if err := loadKeymap(keymapPath); err != nil {
		return fmt.Errorf("failed to load keymap: %w", err)
	}

	if readonly {
		setStatusMsg("Press ctrl+q to exit.")
	} else {
		setStatusMsg("Press ctrl+q to exit. Press ctrl+s to save.")
	}

	/* Handle resize window signals */
	editor.signals = make(chan os.Signal, 1)
	signal.Notify(editor.signals, syscall.SIGWINCH)

	go func() {
		for s := range editor.signals {
			switch s {
			case syscall.SIGABRT:
				return
			case syscall.SIGWINCH:
				resizeWindow()
				refreshScreen()
			}
		}
	}()

	return nil
}

/*-----------------------------------------------------------------------------
 * Editor API
 */

func Editor(source interface{}, readonly bool, keymapPath string) error {

	if err := enableRawMode(); err != nil {
		fmt.Fprintf(os.Stderr, "can not enable raw mode %s", err)
		return err
	}

	if err := initialize(readonly, keymapPath); err != nil {
		return err
	}

	switch src := source.(type) {
	case string: // File source
		if src != "" {
			if err := openFile(src); err != nil {
				cleanupBeforeExit()
				return err
			}
		}
	case []byte: // Data source
		if err := openData(src); err != nil {
			cleanupBeforeExit()
			return err
		}
	default:
		return fmt.Errorf("unsupported source type")
	}

	for {
		refreshScreen()
		exit_editor, err := processKey(readonly)
		if err != nil {
			cleanupBeforeExit()
			return err
		}
		if exit_editor {
			cleanupBeforeExit()
			return nil
		}
	}
}
