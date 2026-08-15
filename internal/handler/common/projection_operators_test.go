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
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

// doc is the shape both operators are exercised against: one array of scalars
// and one array of documents, beside fields neither operator touches.
func projectionOperatorsDoc() *types.Document {
	return must.NotFail(types.NewDocument(
		"_id", int32(1),
		"name", "alpha",
		"tags", must.NotFail(types.NewArray("red", "green", "blue")),
		"items", must.NotFail(types.NewArray(
			must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
			must.NotFail(types.NewDocument("k", "b", "v", int32(5))),
			must.NotFail(types.NewDocument("k", "a", "v", int32(9))),
		)),
	))
}

// TestProjectionSlice covers `{field: {$slice: ...}}`, which this fork used to
// refuse with "projection expression ... is not supported" on every backend.
//
// The case that matters most is the FIRST one: `$slice` on its own decides
// neither inclusion nor exclusion, so the result is the whole document with one
// array limited. Treating it as an inclusion would have returned `_id` and the
// sliced field and dropped everything else.
func TestProjectionSlice(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		projection *types.Document
		expected   *types.Document
		err        string
	}{
		"alone keeps every other field": {
			projection: must.NotFail(types.NewDocument(
				"tags", must.NotFail(types.NewDocument("$slice", int32(1))),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"name", "alpha",
				"tags", must.NotFail(types.NewArray("red")),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
					must.NotFail(types.NewDocument("k", "b", "v", int32(5))),
					must.NotFail(types.NewDocument("k", "a", "v", int32(9))),
				)),
			)),
		},
		"negative takes the last elements": {
			projection: must.NotFail(types.NewDocument(
				"tags", must.NotFail(types.NewDocument("$slice", int32(-2))),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"name", "alpha",
				"tags", must.NotFail(types.NewArray("green", "blue")),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
					must.NotFail(types.NewDocument("k", "b", "v", int32(5))),
					must.NotFail(types.NewDocument("k", "a", "v", int32(9))),
				)),
			)),
		},
		"longer than the array is the whole array": {
			projection: must.NotFail(types.NewDocument(
				"tags", must.NotFail(types.NewDocument("$slice", int32(99))),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"name", "alpha",
				"tags", must.NotFail(types.NewArray("red", "green", "blue")),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
					must.NotFail(types.NewDocument("k", "b", "v", int32(5))),
					must.NotFail(types.NewDocument("k", "a", "v", int32(9))),
				)),
			)),
		},
		"skip and limit": {
			projection: must.NotFail(types.NewDocument(
				"tags", must.NotFail(types.NewDocument(
					"$slice", must.NotFail(types.NewArray(int32(1), int32(1))),
				)),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"name", "alpha",
				"tags", must.NotFail(types.NewArray("green")),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
					must.NotFail(types.NewDocument("k", "b", "v", int32(5))),
					must.NotFail(types.NewDocument("k", "a", "v", int32(9))),
				)),
			)),
		},
		"negative skip counts from the end": {
			projection: must.NotFail(types.NewDocument(
				"tags", must.NotFail(types.NewDocument(
					"$slice", must.NotFail(types.NewArray(int32(-2), int32(1))),
				)),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"name", "alpha",
				"tags", must.NotFail(types.NewArray("green")),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
					must.NotFail(types.NewDocument("k", "b", "v", int32(5))),
					must.NotFail(types.NewDocument("k", "a", "v", int32(9))),
				)),
			)),
		},
		"beside an inclusion keeps only the named fields": {
			projection: must.NotFail(types.NewDocument(
				"name", true,
				"tags", must.NotFail(types.NewDocument("$slice", int32(2))),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"name", "alpha",
				"tags", must.NotFail(types.NewArray("red", "green")),
			)),
		},
		"beside an exclusion drops the excluded field": {
			projection: must.NotFail(types.NewDocument(
				"items", false,
				"tags", must.NotFail(types.NewDocument("$slice", int32(2))),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"name", "alpha",
				"tags", must.NotFail(types.NewArray("red", "green")),
			)),
		},
		"a field that is not an array is returned as it is": {
			projection: must.NotFail(types.NewDocument(
				"name", must.NotFail(types.NewDocument("$slice", int32(1))),
			)),
			expected: projectionOperatorsDoc(),
		},
		"a field that is not there adds nothing": {
			projection: must.NotFail(types.NewDocument(
				"nope", must.NotFail(types.NewDocument("$slice", int32(1))),
			)),
			expected: projectionOperatorsDoc(),
		},
		"a count of zero is an empty array": {
			projection: must.NotFail(types.NewDocument(
				"tags", must.NotFail(types.NewDocument("$slice", int32(0))),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"name", "alpha",
				"tags", types.MakeArray(0),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
					must.NotFail(types.NewDocument("k", "b", "v", int32(5))),
					must.NotFail(types.NewDocument("k", "a", "v", int32(9))),
				)),
			)),
		},
		"a string is refused (negative)": {
			projection: must.NotFail(types.NewDocument(
				"tags", must.NotFail(types.NewDocument("$slice", "two")),
			)),
			err: "Invalid $slice value in projection; must be an integer or array of two integers",
		},
		"an array of one is refused (negative)": {
			projection: must.NotFail(types.NewDocument(
				"tags", must.NotFail(types.NewDocument(
					"$slice", must.NotFail(types.NewArray(int32(1))),
				)),
			)),
			err: "Invalid $slice value in projection; must be an integer or array of two integers",
		},
		"a non-positive count with a skip is refused (negative)": {
			projection: must.NotFail(types.NewDocument(
				"tags", must.NotFail(types.NewDocument(
					"$slice", must.NotFail(types.NewArray(int32(1), int32(0))),
				)),
			)),
			err: "Invalid $slice value in projection; must be positive",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			validated, inclusion, err := ValidateProjection(tc.projection)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)

				return
			}

			require.NoError(t, err)

			actual, err := ProjectDocument(projectionOperatorsDoc(), validated, new(types.Document), inclusion)
			require.NoError(t, err)
			testutil.AssertEqual(t, tc.expected, actual)
		})
	}
}

// TestProjectionElemMatch covers `{field: {$elemMatch: {...}}}`, refused the
// same way before this change. It keeps the FIRST matching element only, and a
// field whose array has no match is left out of the result entirely rather than
// returned empty.
func TestProjectionElemMatch(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		projection *types.Document
		expected   *types.Document
		err        string
	}{
		"keeps the first matching element": {
			projection: must.NotFail(types.NewDocument(
				"items", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("k", "a")),
				)),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
				)),
			)),
		},
		"the condition is the whole query on one element": {
			projection: must.NotFail(types.NewDocument(
				"items", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument(
						"k", "a",
						"v", must.NotFail(types.NewDocument("$gt", int32(3))),
					)),
				)),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "a", "v", int32(9))),
				)),
			)),
		},
		"nothing matches, so the field is not there": {
			projection: must.NotFail(types.NewDocument(
				"items", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("k", "zzz")),
				)),
			)),
			expected: must.NotFail(types.NewDocument("_id", int32(1))),
		},
		"an array of scalars has no element to match (negative)": {
			projection: must.NotFail(types.NewDocument(
				"tags", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("k", "a")),
				)),
			)),
			expected: must.NotFail(types.NewDocument("_id", int32(1))),
		},
		"a field that is not an array is not there (negative)": {
			projection: must.NotFail(types.NewDocument(
				"name", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("k", "a")),
				)),
			)),
			expected: must.NotFail(types.NewDocument("_id", int32(1))),
		},
		"it is an inclusion, so another field beside it is kept": {
			projection: must.NotFail(types.NewDocument(
				"name", true,
				"items", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("k", "b")),
				)),
			)),
			expected: must.NotFail(types.NewDocument(
				"_id", int32(1),
				"name", "alpha",
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "b", "v", int32(5))),
				)),
			)),
		},
		"and cannot be mixed with an exclusion (negative)": {
			projection: must.NotFail(types.NewDocument(
				"name", false,
				"items", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("k", "a")),
				)),
			)),
			err: "Cannot do inclusion on field items in exclusion projection",
		},
		"a condition that is not a document is refused (negative)": {
			projection: must.NotFail(types.NewDocument(
				"items", must.NotFail(types.NewDocument("$elemMatch", int32(1))),
			)),
			err: "elemMatch: Invalid argument, object required, but got int",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			validated, inclusion, err := ValidateProjection(tc.projection)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)

				return
			}

			require.NoError(t, err)

			actual, err := ProjectDocument(projectionOperatorsDoc(), validated, new(types.Document), inclusion)
			require.NoError(t, err)
			testutil.AssertEqual(t, tc.expected, actual)
		})
	}
}

// TestProjectionOperatorsOnID pins that validation and application agree about
// `_id`. Validation accepts the two operators wherever they appear, so applying
// them must not refuse `_id` - a projection that parsed would otherwise fail on
// the first document it reached. `_id` cannot hold an array, so `$slice` leaves
// it and `$elemMatch` takes it away.
func TestProjectionOperatorsOnID(t *testing.T) {
	t.Parallel()

	t.Run("$slice leaves a non-array _id as it is", func(t *testing.T) {
		t.Parallel()

		projection := must.NotFail(types.NewDocument(
			"_id", must.NotFail(types.NewDocument("$slice", int32(1))),
			"name", true,
		))

		validated, inclusion, err := ValidateProjection(projection)
		require.NoError(t, err)

		actual, err := ProjectDocument(projectionOperatorsDoc(), validated, new(types.Document), inclusion)
		require.NoError(t, err)
		testutil.AssertEqual(t, must.NotFail(types.NewDocument("_id", int32(1), "name", "alpha")), actual)
	})

	t.Run("$elemMatch on a non-array _id takes it away", func(t *testing.T) {
		t.Parallel()

		projection := must.NotFail(types.NewDocument(
			"_id", must.NotFail(types.NewDocument(
				"$elemMatch", must.NotFail(types.NewDocument("k", "a")),
			)),
			"name", true,
		))

		validated, inclusion, err := ValidateProjection(projection)
		require.NoError(t, err)

		actual, err := ProjectDocument(projectionOperatorsDoc(), validated, new(types.Document), inclusion)
		require.NoError(t, err)
		testutil.AssertEqual(t, must.NotFail(types.NewDocument("name", "alpha")), actual)
	})
}

// TestProjectionOperatorsConformanceCases runs the two cases the database
// conformance report listed as "errored somewhere" against the documents that
// report seeds, so the fix is pinned by the exact queries that found it rather
// than only by cases written to suit it.
func TestProjectionOperatorsConformanceCases(t *testing.T) {
	t.Parallel()

	docs := []*types.Document{
		must.NotFail(types.NewDocument("_id", int32(1), "name", "alpha",
			"tags", must.NotFail(types.NewArray("red", "green")),
			"items", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
				must.NotFail(types.NewDocument("k", "b", "v", int32(2))),
			)))),
		must.NotFail(types.NewDocument("_id", int32(3), "name", "gamma",
			"tags", types.MakeArray(0),
			"items", types.MakeArray(0))),
		must.NotFail(types.NewDocument("_id", int32(5), "name", "epsilon",
			"tags", must.NotFail(types.NewArray("blue")),
			"items", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("k", "b", "v", int32(0))),
			)))),
	}

	t.Run("projection/$slice", func(t *testing.T) {
		t.Parallel()

		// {tags: {$slice: 1}} - every field kept, tags limited to its first.
		projection := must.NotFail(types.NewDocument(
			"tags", must.NotFail(types.NewDocument("$slice", int32(1))),
		))

		validated, inclusion, err := ValidateProjection(projection)
		require.NoError(t, err)
		assert.False(t, inclusion, "$slice alone must not turn the projection into an inclusion")

		expected := []*types.Document{
			must.NotFail(types.NewDocument("_id", int32(1), "name", "alpha",
				"tags", must.NotFail(types.NewArray("red")),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
					must.NotFail(types.NewDocument("k", "b", "v", int32(2))),
				)))),
			must.NotFail(types.NewDocument("_id", int32(3), "name", "gamma",
				"tags", types.MakeArray(0),
				"items", types.MakeArray(0))),
			must.NotFail(types.NewDocument("_id", int32(5), "name", "epsilon",
				"tags", must.NotFail(types.NewArray("blue")),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "b", "v", int32(0))),
				)))),
		}

		for i, doc := range docs {
			actual, err := ProjectDocument(doc, validated, new(types.Document), inclusion)
			require.NoError(t, err)
			testutil.AssertEqual(t, expected[i], actual)
		}
	})

	t.Run("projection/elemMatch projection", func(t *testing.T) {
		t.Parallel()

		// {items: {$elemMatch: {k: 'a'}}} - _id and the first matching element,
		// and no `items` at all on a document where nothing matches.
		projection := must.NotFail(types.NewDocument(
			"items", must.NotFail(types.NewDocument(
				"$elemMatch", must.NotFail(types.NewDocument("k", "a")),
			)),
		))

		validated, inclusion, err := ValidateProjection(projection)
		require.NoError(t, err)
		assert.True(t, inclusion, "$elemMatch names a field to keep")

		expected := []*types.Document{
			must.NotFail(types.NewDocument("_id", int32(1),
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
				)))),
			must.NotFail(types.NewDocument("_id", int32(3))),
			must.NotFail(types.NewDocument("_id", int32(5))),
		}

		for i, doc := range docs {
			actual, err := ProjectDocument(doc, validated, new(types.Document), inclusion)
			require.NoError(t, err)
			testutil.AssertEqual(t, expected[i], actual)
		}
	})
}

// TestProjectionMeta covers `{field: {$meta: <keyword>}}`, the last of the three
// projection operators. Unlike the other two it does not project a field that is
// there - it ADDS one - so, like `$slice`, it decides neither inclusion nor
// exclusion and a projection made only of it returns the whole document with the
// value beside it.
func TestProjectionMeta(t *testing.T) {
	t.Parallel()

	textFilter := must.NotFail(types.NewDocument(
		"$text", must.NotFail(types.NewDocument("$search", "alpha")),
	))

	t.Run("recordId adds the document's storage identity", func(t *testing.T) {
		t.Parallel()

		doc := projectionOperatorsDoc()
		doc.SetRecordID(4242)

		projection := must.NotFail(types.NewDocument(
			"name", true,
			"rid", must.NotFail(types.NewDocument("$meta", metaRecordID)),
		))

		validated, inclusion, err := ValidateProjection(projection)
		require.NoError(t, err)

		actual, err := ProjectDocument(doc, validated, new(types.Document), inclusion)
		require.NoError(t, err)
		testutil.AssertEqual(t, must.NotFail(types.NewDocument(
			"_id", int32(1), "name", "alpha", "rid", int64(4242),
		)), actual)
	})

	t.Run("alone it keeps every other field", func(t *testing.T) {
		t.Parallel()

		doc := projectionOperatorsDoc()
		doc.SetRecordID(7)

		projection := must.NotFail(types.NewDocument(
			"rid", must.NotFail(types.NewDocument("$meta", metaRecordID)),
		))

		validated, inclusion, err := ValidateProjection(projection)
		require.NoError(t, err)
		assert.False(t, inclusion, "$meta must not turn the projection into an inclusion")

		actual, err := ProjectDocument(doc, validated, new(types.Document), inclusion)
		require.NoError(t, err)
		testutil.AssertEqual(t, must.NotFail(types.NewDocument(
			"_id", int32(1),
			"name", "alpha",
			"tags", must.NotFail(types.NewArray("red", "green", "blue")),
			"items", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("k", "a", "v", int32(1))),
				must.NotFail(types.NewDocument("k", "b", "v", int32(5))),
				must.NotFail(types.NewDocument("k", "a", "v", int32(9))),
			)),
			"rid", int64(7),
		)), actual)
	})

	t.Run("textScore counts what the $text query matched", func(t *testing.T) {
		t.Parallel()

		projection := must.NotFail(types.NewDocument(
			"name", true,
			"score", must.NotFail(types.NewDocument("$meta", metaTextScore)),
		))

		validated, inclusion, err := ValidateProjection(projection)
		require.NoError(t, err)

		actual, err := ProjectDocument(projectionOperatorsDoc(), validated, textFilter, inclusion)
		require.NoError(t, err)
		testutil.AssertEqual(t, must.NotFail(types.NewDocument(
			"_id", int32(1), "name", "alpha", "score", float64(1),
		)), actual)
	})

	t.Run("a term matched more often scores higher", func(t *testing.T) {
		t.Parallel()

		once := must.NotFail(types.NewDocument("_id", int32(1), "a", "alpha", "b", "beta"))
		twice := must.NotFail(types.NewDocument("_id", int32(2), "a", "alpha", "b", "alpha"))

		projection := must.NotFail(types.NewDocument(
			"score", must.NotFail(types.NewDocument("$meta", metaTextScore)),
		))

		validated, inclusion, err := ValidateProjection(projection)
		require.NoError(t, err)

		one, err := ProjectDocument(once, validated, textFilter, inclusion)
		require.NoError(t, err)

		two, err := ProjectDocument(twice, validated, textFilter, inclusion)
		require.NoError(t, err)

		// The VALUES are this implementation's own; the ORDER is the part a
		// client relies on, and the part this pins.
		assert.Equal(t, float64(1), must.NotFail(one.Get("score")))
		assert.Equal(t, float64(2), must.NotFail(two.Get("score")))
	})

	t.Run("textScore without a $text query is refused (negative)", func(t *testing.T) {
		t.Parallel()

		projection := must.NotFail(types.NewDocument(
			"score", must.NotFail(types.NewDocument("$meta", metaTextScore)),
		))

		validated, inclusion, err := ValidateProjection(projection)
		require.NoError(t, err)

		_, err = ProjectDocument(projectionOperatorsDoc(), validated, new(types.Document), inclusion)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "query requires text score metadata, but it is not available")
	})

	t.Run("$text inside a top-level $and is found", func(t *testing.T) {
		t.Parallel()

		filter := must.NotFail(types.NewDocument("$and", must.NotFail(types.NewArray(
			must.NotFail(types.NewDocument("name", "alpha")),
			textFilter,
		))))

		projection := must.NotFail(types.NewDocument(
			"score", must.NotFail(types.NewDocument("$meta", metaTextScore)),
		))

		validated, inclusion, err := ValidateProjection(projection)
		require.NoError(t, err)

		actual, err := ProjectDocument(projectionOperatorsDoc(), validated, filter, inclusion)
		require.NoError(t, err)
		assert.Equal(t, float64(1), must.NotFail(actual.Get("score")))
	})

	t.Run("keywords this handler cannot answer say so (negative)", func(t *testing.T) {
		t.Parallel()

		for keyword, why := range metaNotImplemented {
			projection := must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$meta", keyword)),
			))

			_, _, err := ValidateProjection(projection)
			require.Error(t, err, keyword)
			assert.Contains(t, err.Error(), "$meta "+keyword+" is not supported")
			assert.Contains(t, err.Error(), why, "and says why")
		}
	})

	t.Run("a keyword that does not exist is a different error (negative)", func(t *testing.T) {
		t.Parallel()

		projection := must.NotFail(types.NewDocument(
			"v", must.NotFail(types.NewDocument("$meta", "textscore")),
		))

		_, _, err := ValidateProjection(projection)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported $meta keyword: textscore")
	})

	t.Run("a non-string argument is refused (negative)", func(t *testing.T) {
		t.Parallel()

		projection := must.NotFail(types.NewDocument(
			"v", must.NotFail(types.NewDocument("$meta", int32(1))),
		))

		_, _, err := ValidateProjection(projection)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "$meta expects a string argument")
	})
}

// TestProjectionOperatorStillUnsupported is the other half of the change: an
// operator this handler does NOT implement must keep failing exactly as it did,
// rather than being quietly ignored because the value happens to be a document.
func TestProjectionOperatorStillUnsupported(t *testing.T) {
	t.Parallel()

	for name, projection := range map[string]*types.Document{
		"an operator that does not exist": must.NotFail(types.NewDocument(
			"v", must.NotFail(types.NewDocument("$nope", int32(1))),
		)),
		"a plain sub-document": must.NotFail(types.NewDocument(
			"v", must.NotFail(types.NewDocument("foo", int32(1))),
		)),
		"two operators in one value": must.NotFail(types.NewDocument(
			"v", must.NotFail(types.NewDocument("$slice", int32(1), "$meta", "textScore")),
		)),
		"an empty document": must.NotFail(types.NewDocument(
			"v", new(types.Document),
		)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, err := ValidateProjection(projection)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is not supported")
		})
	}
}
