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

package common

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func TestMultiplyLongSafely(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err              error
		v1, v2, expected int64
	}{
		"Zero": {
			v1:       0,
			v2:       1000,
			expected: 0,
		},
		"One": {
			v1:       42,
			v2:       1,
			expected: 42,
		},
		"DoubleMaxPrecision": {
			v1:       1 << 53,
			v2:       42,
			expected: 378302368699121664,
		},
		"DoubleMaxPrecisionPlus": {
			v1:       (1 << 53) + 1,
			v2:       42,
			expected: 378302368699121706,
		},
		"OverflowLarge": {
			v1:  1 << 60,
			v2:  42,
			err: handlerparams.ErrLongExceededPositive,
		},
		"OverflowMax": {
			v1:  math.MaxInt64,
			v2:  2,
			err: handlerparams.ErrLongExceededPositive,
		},
		"MaxMinusOne": {
			v1:       math.MaxInt64,
			v2:       -1,
			expected: -math.MaxInt64,
		},
		"OverflowMaxMinusTwo": {
			v1:  math.MaxInt64,
			v2:  -2,
			err: handlerparams.ErrLongExceededPositive,
		},
		"OverflowMin": {
			v1:  math.MinInt64,
			v2:  2,
			err: handlerparams.ErrLongExceededNegative,
		},
		"OverflowMinMinusOne": {
			v1:  math.MinInt64,
			v2:  -1,
			err: handlerparams.ErrLongExceededNegative,
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			actualRes, err := multiplyLongSafely(tc.v1, tc.v2)
			assert.Equal(t, tc.err, err)
			assert.Equal(t, tc.expected, actualRes)
		})
	}
}

// TestGetRequiredParamRejectsNonString is about a whole class of bug rather than
// one input: a collection name that is NOT a string must be an ERROR, never a
// quietly narrower namespace.
//
// MongoDB's CVE-2026-18690 is what that costs. Its CommandHelpers::parseNsFromCommand
// read the first element of a command and, when it was not a String, returned
// the DATABASE namespace instead of the collection one:
//
//	if (first.type() != mongo::String)
//	    return NamespaceString(dbName);
//
// A BSON symbol (tag 0x0E, deprecated, and a string in everything but its tag)
// therefore authorized against the database - which ordinary users may hold -
// while execution called valueStringData(), which reads symbols and strings
// alike, and operated on the real collection. Protected system collections
// became reachable.
//
// Neither half of that exists here, and this pins both:
//
//   - a non-string parameter is an error, so there is no narrower namespace to
//     fall back to and nothing that authorizes against one;
//   - the wire decoder (wirebson) refuses tag 0x0E outright ("unsupported tag"),
//     so a symbol never reaches a command handler at all.
//
// FerretDB also has no per-namespace authorization phase to disagree with
// execution - `authorizedCollections` is an ignored parameter - so there are
// three independent reasons this cannot happen. The first two are code, and
// code changes; hence the test.
func TestGetRequiredParamRejectsNonString(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]any{
		"int32":    int32(42),
		"int64":    int64(42),
		"float64":  42.0,
		"bool":     true,
		"null":     types.Null,
		"document": must.NotFail(types.NewDocument("collection", "system.users")),
		"array":    must.NotFail(types.NewArray("system.users")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := must.NotFail(types.NewDocument("find", value))

			res, err := GetRequiredParam[string](doc, "find")
			assert.Empty(t, res, "a rejected parameter must not yield a usable name")
			assert.Error(t, err, "a non-string collection name is an error, not a database-wide fallback")
			assert.Contains(t, err.Error(), `required parameter "find" has type`)
		})
	}

	// The one that is accepted, so the test above is not passing vacuously.
	doc := must.NotFail(types.NewDocument("find", "system.users"))
	res, err := GetRequiredParam[string](doc, "find")
	assert.NoError(t, err)
	assert.Equal(t, "system.users", res)
}
