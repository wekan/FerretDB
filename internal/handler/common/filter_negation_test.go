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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// TestFilterDocumentNegationOverArrayPath: a dot-notation path through an array
// of documents gives one value per element. A negation ($ne, $nin, $not) holds
// only when it holds for EVERY value - {"a.b": {$ne: x}} matches when no element
// has b equal to x, as MongoDB does. It used to hold when it held for ANY value,
// so an "add once" update guarded by {"list.id": {$ne: x}} added duplicates.
func TestFilterDocumentNegationOverArrayPath(t *testing.T) {
	t.Parallel()

	arr := func(ids ...string) *types.Document {
		a := types.MakeArray(len(ids))
		for _, id := range ids {
			a.Append(must.NotFail(types.NewDocument("id", id)))
		}
		return must.NotFail(types.NewDocument("_id", "d", "list", a))
	}
	expr := func(kv ...any) *types.Document {
		return must.NotFail(types.NewDocument("list.id", must.NotFail(types.NewDocument(kv...))))
	}

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		doc      *types.Document
		filter   *types.Document
		expected bool
	}{
		"NeOneOfSeveralEqual":    {arr("m", "x"), expr("$ne", "x"), false},
		"NeOnlyElementEqual":     {arr("x"), expr("$ne", "x"), false},
		"NeNoneEqual":            {arr("m", "n"), expr("$ne", "x"), true},
		"NeEmptyArray":           {arr(), expr("$ne", "x"), true},
		"NinOneOfSeveralEqual":   {arr("m", "x"), expr("$nin", must.NotFail(types.NewArray("x", "y"))), false},
		"NinNoneEqual":           {arr("m", "n"), expr("$nin", must.NotFail(types.NewArray("x", "y"))), true},
		"NotEqOneOfSeveralEqual": {arr("m", "x"), expr("$not", must.NotFail(types.NewDocument("$eq", "x"))), false},
		"NotEqNoneEqual":         {arr("m", "n"), expr("$not", must.NotFail(types.NewDocument("$eq", "x"))), true},
		// Unchanged: a positive operator holds when one value does.
		"EqOneOfSeveral": {arr("m", "x"), expr("$eq", "x"), true},
		"InOneOfSeveral": {arr("m", "x"), expr("$in", must.NotFail(types.NewArray("x"))), true},
		// Mixed: each operator with its own quantifier, ANDed.
		"NeAndGtBothHold": {arr("m", "n"), expr("$ne", "x", "$gt", "a"), true},
		"NeFailsGtHolds":  {arr("m", "x"), expr("$ne", "x", "$gt", "a"), false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			actual, err := FilterDocument(tc.doc, tc.filter)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
