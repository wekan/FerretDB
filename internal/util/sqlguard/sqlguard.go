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

// Package sqlguard is the last look at a statement before it is executed.
//
// The backends already do the right things: values are bound as parameters, never
// formatted into SQL; names are sanitised and quoted where they are made. This
// package assumes all of that can be got wrong again - a new statement written
// with fmt.Sprintf, a helper that forgets to quote - and checks the finished
// string for the shapes that only injection produces.
//
// It is deliberately NOT a SQL parser. It answers one question: does this
// statement contain something that could only have come from data escaping its
// quotes? A statement that trips it is REFUSED, not repaired, and the reason is
// marked so it is visible: the client reads the marker off the error and shows
// the attempt to its operator (internal/util/canary).
//
// False positives matter here, so the checks are narrow: they look at what is
// OUTSIDE quoted literals and quoted identifiers, where nothing user-provided
// should ever appear.
package sqlguard

import (
	"errors"
	"fmt"
	"strings"
)

// ErrSuspicious is returned for a statement that looks like it carries injected
// SQL. It is a bug in FerretDB (or an attack that reached one), never something a
// client should be able to cause.
//
// The message carries the canary marker (internal/util/canary), so the refusal
// does not stop at this process's log: the client reads the marker off the error
// and records the attempt, with the account and the address, where an operator
// will see it. Refusing without telling anybody is how an attack that reached
// here goes unnoticed for a month.
//
// The marker is written out rather than composed from canary.Marker so this
// package keeps its "no dependencies but the standard library" shape; the test
// asserts the two agree.
var ErrSuspicious = errors.New("statement rejected by the SQL guard (canary:db.sql-injection)")

// Dialect selects the quoting rules to read the statement with.
type Dialect int

const (
	// PostgreSQL quotes identifiers with " and literals with '.
	PostgreSQL Dialect = iota

	// MySQL quotes identifiers with ` and literals with ', and escapes with \.
	MySQL

	// SQLite quotes identifiers with " and literals with '.
	SQLite
)

// Check reports whether the finished statement is safe to execute.
//
// The rules, all about what is left once quoted parts are removed:
//
//  1. every quote is closed - an unterminated literal or identifier means data
//     ended one early, which is exactly what an injection does;
//  2. no statement separator - one call executes one statement, so a `;` outside
//     a literal is either a second statement or the start of one;
//  3. no comment introducer that FerretDB did not write - `--`, `#` and `/*`
//     outside a literal end the rest of the statement, which is how an injected
//     fragment hides the syntax it broke;
//  4. balanced parentheses, because an injected expression rarely closes what it
//     opened.
//
// Statements FerretDB builds legitimately contain none of these outside quotes -
// with one exception, comments carried in a `/* ... */` block for the client's
// $comment, which is why writeComment below is the only way to add one.
func Check(dialect Dialect, statement string) error {
	identQuote := byte('"')
	if dialect == MySQL {
		identQuote = '`'
	}

	var (
		inLiteral bool
		inIdent   bool
		depth     int
	)

	for i := 0; i < len(statement); i++ {
		c := statement[i]

		switch {
		case inLiteral:
			switch {
			case dialect == MySQL && c == '\\' && i+1 < len(statement):
				i++ // an escaped character, whatever it is
			case c == '\'' && i+1 < len(statement) && statement[i+1] == '\'':
				i++ // a doubled quote is one quote, not the end
			case c == '\'':
				inLiteral = false
			}

		case inIdent:
			switch {
			case c == identQuote && i+1 < len(statement) && statement[i+1] == identQuote:
				i++ // doubled - part of the name
			case c == identQuote:
				inIdent = false
			}

		default:
			switch c {
			case '\'':
				inLiteral = true
			case identQuote:
				inIdent = true
			case '(':
				depth++
			case ')':
				depth--
				if depth < 0 {
					return fmt.Errorf("%w: unbalanced ')' at %d", ErrSuspicious, i)
				}
			case ';':
				// Trailing semicolons are not written by FerretDB, so any `;` is
				// a separator - the classic "; DROP TABLE".
				return fmt.Errorf("%w: statement separator ';' at %d", ErrSuspicious, i)
			case '-':
				if i+1 < len(statement) && statement[i+1] == '-' {
					return fmt.Errorf("%w: comment '--' at %d", ErrSuspicious, i)
				}
			case '#':
				if dialect == MySQL {
					return fmt.Errorf("%w: comment '#' at %d", ErrSuspicious, i)
				}
			case '/':
				if i+1 < len(statement) && statement[i+1] == '*' {
					// A comment block IS written by FerretDB for $comment - but
					// only through writeComment, which strips any way out of it.
					// What matters is that it is closed and contains no further
					// opener.
					end := strings.Index(statement[i+2:], "*/")
					if end < 0 {
						return fmt.Errorf("%w: unterminated comment at %d", ErrSuspicious, i)
					}

					if strings.Contains(statement[i+2:i+2+end], "/*") {
						return fmt.Errorf("%w: nested comment at %d", ErrSuspicious, i)
					}

					i += 2 + end + 1
				}
			}
		}
	}

	switch {
	case inLiteral:
		return fmt.Errorf("%w: unterminated string literal", ErrSuspicious)
	case inIdent:
		return fmt.Errorf("%w: unterminated quoted identifier", ErrSuspicious)
	case depth != 0:
		return fmt.Errorf("%w: unbalanced parentheses (%+d)", ErrSuspicious, depth)
	}

	return nil
}

// SafeComment makes a client-supplied comment safe to put in a `/* ... */` block.
//
// A comment is the one piece of client text that is written into SQL rather than
// bound, so it is the one that has to be made harmless: anything that could end
// the block early - or open a nested one, or start a line comment after it - is
// neutralised, and the result is bounded in length so a comment cannot be used to
// push a statement past a server's limit.
func SafeComment(comment string) string {
	const maxLen = 1024

	if len(comment) > maxLen {
		comment = comment[:maxLen]
	}

	r := strings.NewReplacer(
		"*/", "* /",
		"/*", "/ *",
		"--", "- -",
		"\x00", "",
		"\n", " ",
		"\r", " ",
	)

	return r.Replace(comment)
}

// DialectByName maps the name a backend gives its connection pool to the quoting
// rules of that database. An unknown name reads as PostgreSQL, whose rules are
// the strictest of the three (no backslash escapes inside literals), so an
// unrecognised backend is guarded more, not less.
func DialectByName(name string) Dialect {
	switch {
	case strings.Contains(name, "mysql"), strings.Contains(name, "mariadb"):
		return MySQL
	case strings.Contains(name, "sqlite"):
		return SQLite
	default:
		return PostgreSQL
	}
}
