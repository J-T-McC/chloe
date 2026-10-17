package sqlsplit

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// Scanner splits a script read from r one statement at a time, holding only
// the statement being read. Offsets and lines are as Split gives them for the
// whole script.
type Scanner struct {
	r       io.Reader
	sp      splitter
	buf     []byte // unsplit text; buf[:from] is context before it
	from    int
	base    int // offset of buf[0] in the script
	pending []Statement
	eof     bool
	err     error
	chunk   int
}

// scanChunk is the least read at a time; a statement longer than the buffer
// doubles the read, so rescanning it stays linear.
const scanChunk = 64 << 10

// NewScanner reads the script from r, skipping a UTF-8 byte order mark.
func NewScanner(r io.Reader, d Dialect) *Scanner {
	br := bufio.NewReader(r)
	if b, _ := br.Peek(3); string(b) == "\ufeff" {
		_, _ = br.Discard(3)
	}
	return &Scanner{r: br, sp: splitter{d: d, delim: ";", line: 1}, chunk: scanChunk}
}

// Next returns the next statement; false at the end of the script or on a
// read error (see Err).
func (s *Scanner) Next() (Statement, bool) {
	for len(s.pending) == 0 {
		if s.eof || s.err != nil {
			return Statement{}, false
		}
		s.fill()
	}
	st := s.pending[0]
	s.pending = s.pending[1:]
	return st, true
}

func (s *Scanner) Err() error { return s.err }

func (s *Scanner) fill() {
	grow := max(s.chunk, len(s.buf)-s.from)
	n := len(s.buf)
	s.buf = append(s.buf, make([]byte, grow)...)
	m, err := io.ReadFull(s.r, s.buf[n:])
	s.buf = s.buf[:n+m]
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		s.eof = true
	case err != nil:
		s.err = err
		return
	}
	text := string(s.buf)
	s.pending = s.pending[:0]
	rest := s.sp.scan(text, s.base, s.from, s.eof, func(st Statement) { s.pending = append(s.pending, st) })
	if s.eof {
		s.buf, s.from = nil, 0
		return
	}
	// Keep the unsplit text and, before it, back to the last byte that isn't a
	// space: enough to tell a line start or a word boundary.
	cut := rest
	for cut > 0 {
		cut--
		if c := text[cut]; c != ' ' && c != '\t' && c != '\r' {
			break
		}
	}
	if cut > 0 {
		if lp := s.sp.linePos - s.base; lp < cut {
			s.sp.line += strings.Count(text[lp:cut], "\n")
			s.sp.linePos = s.base + cut
		}
		s.buf = append(s.buf[:0:0], s.buf[cut:]...)
		s.base += cut
		s.from = rest - cut
	} else {
		s.from = rest
	}
}
