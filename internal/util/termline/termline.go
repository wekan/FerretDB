// Copyright 2021 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package termline reads lines from a terminal with line editing and
// in-memory history, for the built-in interactive shells.
//
// The terminal is put into raw mode only while a line is being read, so
// whatever runs between two prompts writes to an ordinary terminal.
package termline

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrInterrupt is returned by ReadLine when the user pressed Ctrl-C.
var ErrInterrupt = errors.New("interrupted")

// IsTerminal reports whether r is a terminal.
func IsTerminal(r any) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Reader reads lines, editing them on a terminal and plainly elsewhere.
type Reader struct {
	in    io.Reader
	out   io.Writer
	fd    int
	tty   bool
	t     *term.Terminal
	ctrlC *ctrlCReader
	plain *bufio.Reader
}

// ctrlCReader turns Ctrl-C into "go to the end of the line, erase it,
// Enter" and notes that it saw one. golang.org/x/term itself reports Ctrl-C
// as io.EOF, the same as Ctrl-D, and leaves it in its buffer, so every
// later ReadLine would end at once.
type ctrlCReader struct {
	r       io.Reader
	pending []byte
	saw     bool
}

// Read implements io.Reader.
func (c *ctrlCReader) Read(p []byte) (int, error) {
	if len(c.pending) == 0 {
		buf := make([]byte, len(p))

		n, err := c.r.Read(buf)
		if n == 0 {
			return 0, err
		}

		for _, b := range buf[:n] {
			if b == 3 {
				c.saw = true
				c.pending = append(c.pending, 5, 21, '\r')

				continue
			}

			c.pending = append(c.pending, b)
		}
	}

	n := copy(p, c.pending)
	c.pending = c.pending[n:]

	return n, nil
}

// New returns a Reader for in, writing prompts to out.
func New(in io.Reader, out io.Writer) *Reader {
	r := &Reader{in: in, out: out}

	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		r.tty = true
		r.fd = int(f.Fd())
		r.ctrlC = &ctrlCReader{r: f}
		r.t = term.NewTerminal(struct {
			io.Reader
			io.Writer
		}{r.ctrlC, out}, "")

		if w, h, err := term.GetSize(r.fd); err == nil && w > 0 && h > 0 {
			_ = r.t.SetSize(w, h)
		}

		return r
	}

	r.plain = bufio.NewReader(in)

	return r
}

// Terminal reports whether lines are read from a terminal.
func (r *Reader) Terminal() bool {
	return r.tty
}

// ReadLine prints prompt (on a terminal only) and returns the next line
// without its line ending. It returns io.EOF at the end of the input, and
// ErrInterrupt when Ctrl-C was pressed on a terminal.
func (r *Reader) ReadLine(prompt string) (string, error) {
	if !r.tty {
		line, err := r.plain.ReadString('\n')
		if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
			return "", err
		}

		return strings.TrimRight(line, "\r\n"), nil
	}

	state, err := term.MakeRaw(r.fd)
	if err != nil {
		return "", err
	}

	r.t.SetPrompt(prompt)
	r.ctrlC.saw = false
	line, err := r.t.ReadLine()

	_ = term.Restore(r.fd, state)

	if errors.Is(err, term.ErrPasteIndicator) {
		err = nil
	}

	if r.ctrlC.saw {
		return "", ErrInterrupt
	}

	if errors.Is(err, io.EOF) {
		_, _ = io.WriteString(r.out, "\n")
	}

	return line, err
}

// ReadPassword prints prompt and reads a line without echo on a terminal.
func (r *Reader) ReadPassword(prompt string) (string, error) {
	if !r.tty {
		return r.ReadLine("")
	}

	_, _ = io.WriteString(r.out, prompt)
	b, err := term.ReadPassword(r.fd)
	_, _ = io.WriteString(r.out, "\n")

	return string(b), err
}
