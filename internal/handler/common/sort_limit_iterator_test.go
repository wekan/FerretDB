// Copyright 2026 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package common

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func TestSortLimitIterator(t *testing.T) {
	t.Parallel()

	docs := []*types.Document{
		must.NotFail(types.NewDocument("_id", "five", "sort", int32(5))),
		must.NotFail(types.NewDocument("_id", "one", "sort", int32(1))),
		must.NotFail(types.NewDocument("_id", "four", "sort", int32(4))),
		must.NotFail(types.NewDocument("_id", "two", "sort", int32(2))),
		must.NotFail(types.NewDocument("_id", "three", "sort", int32(3))),
	}
	sortDoc := must.NotFail(types.NewDocument("sort", int32(1)))
	closer := iterator.NewMultiCloser()
	defer closer.Close()
	source := iterator.Values(iterator.ForSlice(docs))
	closer.Add(source)

	limited, err := SortLimitIterator(source, closer, sortDoc, 3)
	require.NoError(t, err)
	actual, err := iterator.ConsumeValues(limited)
	require.NoError(t, err)
	require.Len(t, actual, 3)
	require.Equal(t, []string{"one", "two", "three"}, []string{
		must.NotFail(actual[0].Get("_id")).(string),
		must.NotFail(actual[1].Get("_id")).(string),
		must.NotFail(actual[2].Get("_id")).(string),
	})
}
