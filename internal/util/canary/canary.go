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

// Package canary marks operations the client never issues.
//
// This database is reached over a LOCAL socket by one application, whose driver
// is a Meteor 3 one. That makes a whole class of operations interesting by their
// mere presence: server-side JavaScript evaluation, writing a query's result into
// another collection, dropping a database. The driver does not send them, so a
// request that does is either a bug or somebody who has reached the socket and is
// looking around - and either is worth telling the operator about.
//
// A canary is not a defence. The operations below are refused because they are
// refused; the canary is the ATTRIBUTION, so the refusal is not silent and the
// operator sees what was tried rather than a generic error. That is the same
// division of labour as internal/util/sqlguard: refuse, and make the refusal
// visible.
//
// Three properties, matching the client-side design this pairs with:
//
//  1. SILENT to the caller. A tripped canary produces the SAME error the
//     operation would have produced anyway - a command this build does not
//     support. The marker below is in the error's text, which the client parses
//     and the operator reads; a caller learns nothing from it that the plain
//     refusal did not already tell them, so a probe cannot map which operations
//     are watched and avoid them.
//  2. BOUNDED. Nothing is written here: no file, no table, no counter that grows.
//     The error goes back on the wire, and the client - which already rate-limits
//     and aggregates what it records - decides what to store. A caller hammering
//     a canary in a loop therefore costs this process one string comparison per
//     request and this package no memory at all.
//  3. ATTRIBUTED. The marker names WHICH canary was tripped, so the operator's
//     report says "tried to run server-side JavaScript" rather than "an error".
//
// Nothing here logs the operation's arguments. They are caller-controlled and a
// log line is not the place for them.
package canary

import (
	"errors"
	"fmt"
	"strings"
)

// Marker prefixes the id inside a tripped canary's error text. The client
// matches on it to classify the event, so it is a STABLE STRING: changing it
// silently turns every canary into an unrecognised error.
const Marker = "canary:"

// The canary ids. One per operation the client never issues, named for what was
// attempted rather than for the internal that refuses it.
const (
	// IDJavaScript is server-side JavaScript evaluation - $where, eval,
	// mapReduce with functions. The driver has no feature that sends it.
	IDJavaScript = "db.javascript"

	// IDResultToCollection is an aggregation writing its result into a
	// collection ($out, $merge). The driver reads aggregation results; it never
	// asks the database to persist them.
	IDResultToCollection = "db.result-to-collection"

	// IDDropDatabase is dropping a whole database. The application drops
	// collections it owns; dropping the database is an operator action taken
	// with the database's own tools, not over this socket.
	IDDropDatabase = "db.drop-database"

	// IDServerAdmin is an administrative command that manages the SERVER rather
	// than the data - shutting it down, changing its parameters, reading its
	// internal profile.
	IDServerAdmin = "db.server-admin"
)

// ErrTripped is returned for an operation that trips a canary. It is refused,
// never carried out.
var ErrTripped = errors.New("operation not supported by this build")

// Tripped is the error a tripped canary returns: the ordinary "not supported"
// refusal, with the marker and the id appended so the client can classify it.
type Tripped struct {
	// ID is one of the canary ids above.
	ID string

	// Op is the command or stage name that tripped it, for the operator's
	// report. A short, already-known token - never an argument value.
	Op string
}

// Error implements error.
func (t *Tripped) Error() string {
	return fmt.Sprintf("%s (%s%s %s)", ErrTripped.Error(), Marker, t.ID, t.Op)
}

// Unwrap lets errors.Is(err, ErrTripped) work, so a caller that only wants to
// know "was this refused" does not have to know about canaries at all.
func (t *Tripped) Unwrap() error {
	return ErrTripped
}

// javaScriptOps are the command and operator names that mean "evaluate
// JavaScript on the server". Compared lower-cased.
var javaScriptOps = map[string]string{
	"eval":         IDJavaScript,
	"$where":       IDJavaScript,
	"$function":    IDJavaScript,
	"$accumulator": IDJavaScript,
	"mapreduce":    IDJavaScript,
}

// resultToCollectionOps write an aggregation's result into a collection.
var resultToCollectionOps = map[string]string{
	"$out":   IDResultToCollection,
	"$merge": IDResultToCollection,
}

// serverAdminOps manage the server rather than the data.
var serverAdminOps = map[string]string{
	"shutdown":         IDServerAdmin,
	"setparameter":     IDServerAdmin,
	"getparameter":     IDServerAdmin,
	"profile":          IDServerAdmin,
	"logrotate":        IDServerAdmin,
	"dropdatabase":     IDDropDatabase,
	"dropalldatabases": IDDropDatabase,
}

// Check reports whether op trips a canary, and returns the error to refuse it
// with. A nil error means the operation is ordinary and must be handled as
// before - Check is a lookup, never a policy of its own.
//
// op is a command name (`eval`), an aggregation stage (`$out`) or a query
// operator (`$where`). Matching is case-insensitive because command names arrive
// as the caller spelled them.
func Check(op string) error {
	name := strings.ToLower(strings.TrimSpace(op))
	if name == "" {
		return nil
	}

	for _, table := range []map[string]string{javaScriptOps, resultToCollectionOps, serverAdminOps} {
		if id, ok := table[name]; ok {
			return &Tripped{ID: id, Op: name}
		}
	}

	return nil
}

// IDOf returns the canary id inside an error's text, or "" when there is none.
// It is what the client's classifier does, kept here so the two cannot drift and
// so the tests can assert the round trip.
func IDOf(err error) string {
	if err == nil {
		return ""
	}

	text := err.Error()

	at := strings.Index(text, Marker)
	if at < 0 {
		return ""
	}

	rest := text[at+len(Marker):]
	if end := strings.IndexAny(rest, " )"); end >= 0 {
		rest = rest[:end]
	}

	return rest
}
