// Copyright 2021 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package common

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func TestSortAndDeduplicateDistinctValues(t *testing.T) {
	t.Parallel()

	doc := must.NotFail(types.NewDocument("x", int32(1)))
	array := must.NotFail(types.NewArray("nested"))
	values := must.NotFail(types.NewArray(
		"b", int32(1), doc, "a", types.Null, array,
		"a", int64(1), doc.DeepCopy(), types.Null, array.DeepCopy(),
	))

	actual := sortAndDeduplicateDistinctValues(values)
	require.Equal(t, 6, actual.Len())

	expected := []any{types.Null, int32(1), "a", "b", doc, array}
	for _, value := range expected {
		matches := 0
		for i := 0; i < actual.Len(); i++ {
			if types.Compare(must.NotFail(actual.Get(i)), value) == types.Equal {
				matches++
			}
		}
		assert.Equal(t, 1, matches, "value %v must occur exactly once", value)
	}

	for i := 1; i < actual.Len(); i++ {
		assert.NotEqual(t, types.Greater, types.CompareOrderForSort(
			must.NotFail(actual.Get(i-1)), must.NotFail(actual.Get(i)), types.Ascending,
		))
	}
}

func BenchmarkSortAndDeduplicateDistinctValues(b *testing.B) {
	const values = 45640
	input := make([]string, values)
	for i := range input {
		input[i] = fmt.Sprintf("%024d", values-i)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		array := types.MakeArray(values)
		for _, value := range input {
			array.Append(value)
		}
		if got := sortAndDeduplicateDistinctValues(array); got.Len() != values {
			b.Fatalf("got %d values", got.Len())
		}
	}
}
