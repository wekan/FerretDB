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

package operators

import (
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/types"
)

func TestConversionBounds(t *testing.T) {
	t.Parallel()

	doc := types.MakeDocument(0)

	t.Run("SubstringLargeBounds", func(t *testing.T) {
		op, err := newSubstrBytesOp("abc", int64(math.MaxInt64), int64(math.MaxInt64))
		require.NoError(t, err)

		actual, err := op.Process(doc)
		require.NoError(t, err)
		assert.Equal(t, "", actual)
	})

	t.Run("SubstringFractionRejected", func(t *testing.T) {
		op, err := newSubstrBytesOp("abc", 1.5, int32(1))
		require.NoError(t, err)

		_, err = op.Process(doc)
		require.Error(t, err)
	})

	t.Run("IndexLargeBounds", func(t *testing.T) {
		op, err := newIndexOfBytes("abc", "a", int64(math.MaxInt64), int64(math.MaxInt64))
		require.NoError(t, err)

		actual, err := op.Process(doc)
		require.NoError(t, err)
		assert.Equal(t, int32(-1), actual)
	})

	t.Run("ArrayElementLargeIndex", func(t *testing.T) {
		arr, err := types.NewArray(int32(1), int32(2))
		require.NoError(t, err)
		op, err := newArrayElemAt(arr, int64(math.MaxInt64))
		require.NoError(t, err)

		actual, err := op.Process(doc)
		require.NoError(t, err)
		assert.Equal(t, types.Null, actual)
	})

	t.Run("ArraySliceLargeCount", func(t *testing.T) {
		arr, err := types.NewArray(int32(1), int32(2))
		require.NoError(t, err)
		op, err := newSlice(arr, int64(math.MaxInt64))
		require.NoError(t, err)

		actual, err := op.Process(doc)
		require.NoError(t, err)
		assert.Equal(t, 2, actual.(*types.Array).Len())
	})

	t.Run("RangeRejectsLargeArgument", func(t *testing.T) {
		op, err := newRange(int64(math.MaxInt64), int64(math.MaxInt64))
		require.NoError(t, err)

		_, err = op.Process(doc)
		require.Error(t, err)
	})

	t.Run("ArrayIndexLargeBounds", func(t *testing.T) {
		arr, err := types.NewArray(int32(1), int32(2))
		require.NoError(t, err)
		op, err := newIndexOfArray(arr, int32(1), int64(math.MaxInt64), int64(math.MaxInt64))
		require.NoError(t, err)

		actual, err := op.Process(doc)
		require.NoError(t, err)
		assert.Equal(t, int32(-1), actual)
	})

	t.Run("ConvertRejectsNonIntegralTypeCode", func(t *testing.T) {
		_, err := parseConvertTo(2.5)
		require.Error(t, err)
	})

	t.Run("ConvertRejectsOutOfRangeTypeCode", func(t *testing.T) {
		_, err := convertToCodeFromNumber(math.MaxInt64)
		require.Error(t, err)
	})

	t.Run("CheckedInt32Boundaries", func(t *testing.T) {
		min, err := checkedInt32(math.MinInt32)
		require.NoError(t, err)
		assert.Equal(t, int32(math.MinInt32), min)

		max, err := checkedInt32(math.MaxInt32)
		require.NoError(t, err)
		assert.Equal(t, int32(math.MaxInt32), max)

		_, err = checkedInt32(int64(math.MaxInt32) + 1)
		require.Error(t, err)
		_, err = checkedInt32(int64(math.MinInt32) - 1)
		require.Error(t, err)
	})

	t.Run("AggregationIndexDoesNotNarrow", func(t *testing.T) {
		assert.Equal(t, int32(math.MaxInt32), aggregationIndex(math.MaxInt32))
		if strconv.IntSize == 64 {
			large64 := int64(math.MaxInt32)
			large64++
			large := int(large64)
			assert.Equal(t, int64(math.MaxInt32)+1, aggregationIndex(large))
		}
	})

	t.Run("FilterLargeLimit", func(t *testing.T) {
		arr, err := types.NewArray(int32(1), int32(2))
		require.NoError(t, err)
		spec, err := types.NewDocument("input", arr, "cond", true, "limit", int64(math.MaxInt64))
		require.NoError(t, err)
		op, err := newFilter(spec)
		require.NoError(t, err)

		actual, err := op.Process(doc)
		require.NoError(t, err)
		assert.Equal(t, 2, actual.(*types.Array).Len())
	})

	t.Run("DateDoubleOverflowRejected", func(t *testing.T) {
		_, err := asInt("$dateFromParts", "year", float64(math.MaxInt64))
		require.Error(t, err)
	})

	t.Run("NativeIntOverflow", func(t *testing.T) {
		_, err := nativeInt("$dateFromParts", "year", math.MaxInt64)
		if strconv.IntSize == 32 {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	})
}
