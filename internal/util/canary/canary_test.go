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

package canary

import (
	"errors"
	"strings"
	"testing"
)

// The operations that must trip, and the id each must report.
func TestCheckTrips(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		op string
		id string
	}{
		{"eval", IDJavaScript},
		{"$where", IDJavaScript},
		{"$function", IDJavaScript},
		{"$accumulator", IDJavaScript},
		{"mapReduce", IDJavaScript},
		{"$out", IDResultToCollection},
		{"$merge", IDResultToCollection},
		{"dropDatabase", IDDropDatabase},
		{"dropAllDatabases", IDDropDatabase},
		{"shutdown", IDServerAdmin},
		{"setParameter", IDServerAdmin},
		{"logRotate", IDServerAdmin},
	} {
		tc := tc
		t.Run(tc.op, func(t *testing.T) {
			t.Parallel()

			err := Check(tc.op)
			if err == nil {
				t.Fatalf("%q must trip a canary", tc.op)
			}

			if got := IDOf(err); got != tc.id {
				t.Errorf("id = %q, want %q", got, tc.id)
			}

			// A caller that only wants "was this refused" must not have to know
			// about canaries.
			if !errors.Is(err, ErrTripped) {
				t.Errorf("errors.Is(err, ErrTripped) = false")
			}
		})
	}
}

// The ordinary operations the client DOES issue must not trip. A canary that
// fires on normal traffic is worse than no canary: it buries the real ones.
func TestCheckDoesNotTripOnOrdinaryOperations(t *testing.T) {
	t.Parallel()

	for _, op := range []string{
		"find", "insert", "update", "delete", "aggregate", "count", "distinct",
		"createIndexes", "listCollections", "listIndexes", "drop", "getMore",
		"$match", "$group", "$sort", "$limit", "$skip", "$lookup", "$unwind",
		"$in", "$or", "$and", "$elemMatch", "$set", "$push", "$pull",
		"", "   ",
	} {
		op := op
		t.Run(op, func(t *testing.T) {
			t.Parallel()

			if err := Check(op); err != nil {
				t.Errorf("%q must NOT trip a canary, got %v", op, err)
			}
		})
	}
}

// Command names arrive as the caller spelled them.
func TestCheckIsCaseInsensitiveAndTrims(t *testing.T) {
	t.Parallel()

	for _, op := range []string{"EVAL", "Eval", "  eval  ", "$WHERE", "DropDatabase"} {
		if err := Check(op); err == nil {
			t.Errorf("%q must trip a canary", op)
		}
	}
}

// The refusal must read as an ordinary "not supported", so a probe cannot tell a
// watched operation from an unimplemented one and route around the watched ones.
func TestTrippedErrorLooksOrdinary(t *testing.T) {
	t.Parallel()

	err := Check("eval")
	if err == nil {
		t.Fatal("expected an error")
	}

	if !strings.HasPrefix(err.Error(), ErrTripped.Error()) {
		t.Errorf("the refusal must begin with the ordinary message, got %q", err.Error())
	}

	// It must NOT name the application, its features, or say that anything was
	// detected or recorded.
	for _, leak := range []string{"detect", "record", "log", "canary token", "alert"} {
		if strings.Contains(strings.ToLower(err.Error()), leak) {
			t.Errorf("the refusal must not reveal %q: %q", leak, err.Error())
		}
	}
}

// IDOf is the client-side classifier's algorithm; a round trip that breaks makes
// every canary an unrecognised error.
func TestIDOfRoundTrip(t *testing.T) {
	t.Parallel()

	if got := IDOf(nil); got != "" {
		t.Errorf("IDOf(nil) = %q, want empty", got)
	}

	if got := IDOf(errors.New("some unrelated failure")); got != "" {
		t.Errorf("IDOf(unrelated) = %q, want empty", got)
	}

	// Wrapped, as it will be by the time it crosses the wire.
	wrapped := errors.New("write failed: " + Check("$out").Error())
	if got := IDOf(wrapped); got != IDResultToCollection {
		t.Errorf("IDOf(wrapped) = %q, want %q", got, IDResultToCollection)
	}
}

// The SQL guard marks its own refusal with this package's marker rather than
// importing it, so that package keeps its standard-library-only shape. The two
// must agree, or a refused statement arrives at the client as an unrecognised
// error and nobody is told an injection was refused.
func TestSQLGuardMarkerAgrees(t *testing.T) {
	t.Parallel()

	// The id the SQL guard writes, and the shape the client parses.
	const sqlID = "db.sql-injection"

	marked := errors.New("statement rejected by the SQL guard (" + Marker + sqlID + ")")
	if got := IDOf(marked); got != sqlID {
		t.Errorf("IDOf(sql guard error) = %q, want %q", got, sqlID)
	}

	if !strings.Contains(sqlguardMessage, Marker+sqlID) {
		t.Errorf("the SQL guard's message %q must carry %q", sqlguardMessage, Marker+sqlID)
	}
}

// Kept beside the assertion above: the exact text internal/util/sqlguard uses.
// A change to either side without the other fails the test.
const sqlguardMessage = "statement rejected by the SQL guard (canary:db.sql-injection)"

// Nothing in this package may accumulate state: it is on a path a caller can hit
// in a loop, and a counter here would be memory an attacker chooses the size of.
func TestCheckIsStateless(t *testing.T) {
	t.Parallel()

	first := Check("eval").Error()

	for i := 0; i < 1000; i++ {
		Check("eval")
		Check("find")
	}

	if got := Check("eval").Error(); got != first {
		t.Errorf("the refusal changed after repeated calls: %q then %q", first, got)
	}
}
