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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func positionalDoc() *types.Document {
	return must.NotFail(types.NewDocument(
		"_id", "d1",
		"items", must.NotFail(types.NewArray(
			must.NotFail(types.NewDocument("k", "a", "n", int32(1))),
			must.NotFail(types.NewDocument("k", "b", "n", int32(2))),
			must.NotFail(types.NewDocument("k", "c", "n", int32(3))),
		)),
		"nested", must.NotFail(types.NewDocument(
			"list", must.NotFail(types.NewArray(int32(5), int32(7))),
		)),
	))
}

func TestResolvePositional(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		filter *types.Document
		update *types.Document
		want   string // the resolved key of the first operator
		err    bool
	}{
		"dotted equality": {
			filter: must.NotFail(types.NewDocument("items.k", "b")),
			update: must.NotFail(types.NewDocument("$set", must.NotFail(types.NewDocument("items.$.n", int32(9))))),
			want:   "items.1.n",
		},
		"the first of several matches": {
			filter: must.NotFail(types.NewDocument("items.n", must.NotFail(types.NewDocument("$gte", int32(2))))),
			update: must.NotFail(types.NewDocument("$set", must.NotFail(types.NewDocument("items.$.n", int32(9))))),
			want:   "items.1.n",
		},
		"elemMatch": {
			filter: must.NotFail(types.NewDocument("items", must.NotFail(types.NewDocument("$elemMatch",
				must.NotFail(types.NewDocument("k", "c", "n", int32(3))))))),
			update: must.NotFail(types.NewDocument("$inc", must.NotFail(types.NewDocument("items.$.n", int32(1))))),
			want:   "items.2.n",
		},
		"a nested array, a scalar element": {
			filter: must.NotFail(types.NewDocument("nested.list", int32(7))),
			update: must.NotFail(types.NewDocument("$set", must.NotFail(types.NewDocument("nested.list.$", int32(8))))),
			want:   "nested.list.1",
		},
		"unset": {
			filter: must.NotFail(types.NewDocument("_id", "d1", "items.k", "a")),
			update: must.NotFail(types.NewDocument("$unset", must.NotFail(types.NewDocument("items.$.n", "")))),
			want:   "items.0.n",
		},
		"no positional": {
			filter: must.NotFail(types.NewDocument("_id", "d1")),
			update: must.NotFail(types.NewDocument("$set", must.NotFail(types.NewDocument("items.0.n", int32(0))))),
			want:   "items.0.n",
		},
		"negative: no element matches": {
			filter: must.NotFail(types.NewDocument("items.k", "z")),
			update: must.NotFail(types.NewDocument("$set", must.NotFail(types.NewDocument("items.$.n", int32(9))))),
			err:    true,
		},
		"negative: the filter is on another array": {
			filter: must.NotFail(types.NewDocument("nested.list", int32(5))),
			update: must.NotFail(types.NewDocument("$set", must.NotFail(types.NewDocument("items.$.n", int32(9))))),
			err:    true,
		},
		"negative: two positional segments": {
			filter: must.NotFail(types.NewDocument("items.k", "a")),
			update: must.NotFail(types.NewDocument("$set", must.NotFail(types.NewDocument("items.$.x.$", int32(9))))),
			err:    true,
		},
		"negative: a positional first segment": {
			filter: must.NotFail(types.NewDocument("items.k", "a")),
			update: must.NotFail(types.NewDocument("$set", must.NotFail(types.NewDocument("$.n", int32(9))))),
			err:    true,
		},
		"negative: a dynamic rename": {
			filter: must.NotFail(types.NewDocument("items.k", "a")),
			update: must.NotFail(types.NewDocument("$rename", must.NotFail(types.NewDocument("x", "items.$.n")))),
			err:    true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := resolvePositional(positionalDoc(), tc.filter, tc.update)
			if tc.err {
				var ce *handlererrors.CommandError
				require.True(t, errors.As(err, &ce), "%v", err)
				assert.Equal(t, handlererrors.ErrBadValue, ce.Code())

				return
			}

			require.NoError(t, err)

			op := got.Keys()[0]
			fields := must.NotFail(got.Get(op)).(*types.Document)
			assert.Equal(t, []string{tc.want}, fields.Keys())
		})
	}
}

// The resolved update is applied to the matched element only.
func TestPositionalUpdateApplied(t *testing.T) {
	t.Parallel()

	doc := positionalDoc()
	filter := must.NotFail(types.NewDocument("items.k", "b"))
	update := must.NotFail(types.NewDocument("$set", must.NotFail(types.NewDocument("items.$.n", int32(20)))))

	resolved, err := resolvePositional(doc, filter, update)
	require.NoError(t, err)

	_, err = processUpdateOperator("update", doc, resolved, false)
	require.NoError(t, err)

	items := must.NotFail(doc.Get("items")).(*types.Array)
	for i, want := range []int32{1, 20, 3} {
		el := must.NotFail(items.Get(i)).(*types.Document)
		assert.Equal(t, want, must.NotFail(el.Get("n")), "element %d", i)
	}
}
