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

// TestFilterDocumentElemMatch covers the document/field form of $elemMatch
// ({arr: {$elemMatch: {field: value, ...}}}) added by this fork: match
// array elements that are documents satisfying the WHOLE sub-query on the SAME
// element. The previous behavior rejected it with "unknown operator: <field>",
// which broke a nested-document member access check
// {members: {$elemMatch: {userId: X, isActive: true}}} on the SQLite backend —
// the board list returned no boards and private boards could not be opened.
// The operator form ({arr: {$elemMatch: {$gt: value}}}) must stay unchanged.
func TestFilterDocumentElemMatch(t *testing.T) {
	t.Parallel()

	// A boards-like document shape: members whose elements are documents.
	boardDoc := must.NotFail(types.NewDocument(
		"_id", "board1",
		"members", must.NotFail(types.NewArray(
			must.NotFail(types.NewDocument("userId", "u1", "isActive", false)),
			must.NotFail(types.NewDocument("userId", "u2", "isActive", true)),
		)),
	))

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		doc      *types.Document
		filter   *types.Document
		expected bool
		wantErr  bool
	}{
		"NestedDocumentMemberMatches": {
			// the fix's motivating query: BOTH fields on the SAME element
			doc: boardDoc,
			filter: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("userId", "u2", "isActive", true)),
				)),
			)),
			expected: true,
		},
		"SingleFieldForm": {
			doc: boardDoc,
			filter: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("userId", "u1")),
				)),
			)),
			expected: true,
		},
		"FieldFormWithNestedOperator": {
			// {qty: {$gt: int32(5)}} inside the field form
			doc: must.NotFail(types.NewDocument(
				"items", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("qty", int32(2))),
					must.NotFail(types.NewDocument("qty", int32(10))),
				)),
			)),
			filter: must.NotFail(types.NewDocument(
				"items", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument(
						"qty", must.NotFail(types.NewDocument("$gt", int32(5))),
					)),
				)),
			)),
			expected: true,
		},
		"NegativeSameElementRequired": {
			// userId matches one element, isActive:true a DIFFERENT one — the
			// whole sub-query must hold on a single element, so NO match.
			doc: boardDoc,
			filter: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("userId", "u1", "isActive", true)),
				)),
			)),
			expected: false,
		},
		"NegativeNoSuchMember": {
			doc: boardDoc,
			filter: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("userId", "nobody")),
				)),
			)),
			expected: false,
		},
		"NegativeFieldIsNotAnArray": {
			doc: must.NotFail(types.NewDocument("members", "not-an-array")),
			filter: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("userId", "u1")),
				)),
			)),
			expected: false,
		},
		"NegativeFieldMissing": {
			doc: must.NotFail(types.NewDocument("_id", "x")),
			filter: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("userId", "u1")),
				)),
			)),
			expected: false,
		},
		"NegativeNonDocumentElementsSkipped": {
			// scalar elements cannot satisfy a field-form sub-query
			doc: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewArray("u1", int32(2))),
			)),
			filter: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("userId", "u1")),
				)),
			)),
			expected: false,
		},
		"OperatorFormStillMatches": {
			doc: must.NotFail(types.NewDocument(
				"arr", must.NotFail(types.NewArray(int32(1), int32(5), int32(10))),
			)),
			filter: must.NotFail(types.NewDocument(
				"arr", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("$gt", int32(7))),
				)),
			)),
			expected: true,
		},
		"OperatorFormNegative": {
			doc: must.NotFail(types.NewDocument(
				"arr", must.NotFail(types.NewArray(int32(1), int32(5), int32(10))),
			)),
			filter: must.NotFail(types.NewDocument(
				"arr", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("$gt", int32(100))),
				)),
			)),
			expected: false,
		},
		"ErrorNotAnObject": {
			doc: boardDoc,
			filter: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewDocument("$elemMatch", "u1")),
			)),
			wantErr: true,
		},
		"ErrorTopLevelOnlyOperator": {
			doc: boardDoc,
			filter: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument("$text", "x")),
				)),
			)),
			wantErr: true,
		},
		"ErrorUnimplementedLogicalOperator": {
			doc: boardDoc,
			filter: must.NotFail(types.NewDocument(
				"members", must.NotFail(types.NewDocument(
					"$elemMatch", must.NotFail(types.NewDocument(
						"$or", must.NotFail(types.NewArray()),
					)),
				)),
			)),
			wantErr: true,
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			matches, err := FilterDocument(tc.doc, tc.filter)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expected, matches)
		})
	}
}
