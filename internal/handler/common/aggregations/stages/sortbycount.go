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

package stages

import (
	"context"

	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations/operators"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// sortByCount represents the $sortByCount stage.
//
//	{ $sortByCount: <expression> }
//
// It is equivalent to grouping documents by the given expression and counting
// each group, then sorting the groups by count descending (ties broken by _id
// ascending):
//
//	{ $group: { _id: <expression>, count: { $sum: 1 } } }
//	{ $sort:  { count: -1, _id: 1 } }
type sortByCount struct {
	group aggregations.Stage
	sort  aggregations.Stage
}

// newSortByCount creates a new $sortByCount stage.
func newSortByCount(stage *types.Document) (aggregations.Stage, error) {
	expression, err := stage.Get("$sortByCount")
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	// $sortByCount accepts either a $-prefixed field path (or expression string)
	// or an object holding a single expression operator.
	switch expr := expression.(type) {
	case *types.Document:
		if !operators.IsOperator(expr) {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrTypeMismatch,
				"the $sortByCount field must be defined as a $-prefixed path or an expression object",
				"$sortByCount (stage)",
			)
		}
	case string:
		if _, err = aggregations.NewExpression(expr, nil); err != nil {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrTypeMismatch,
				"the $sortByCount field must be defined as a $-prefixed path or an expression object",
				"$sortByCount (stage)",
			)
		}
	default:
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			"the $sortByCount field must be defined as a $-prefixed path or an expression object",
			"$sortByCount (stage)",
		)
	}

	groupStage, err := newGroup(must.NotFail(types.NewDocument(
		"$group", must.NotFail(types.NewDocument(
			"_id", expression,
			"count", must.NotFail(types.NewDocument("$sum", int32(1))),
		)),
	)))
	if err != nil {
		return nil, err
	}

	sortStage, err := newSort(must.NotFail(types.NewDocument(
		"$sort", must.NotFail(types.NewDocument(
			"count", int32(-1),
			"_id", int32(1),
		)),
	)))
	if err != nil {
		return nil, err
	}

	return &sortByCount{
		group: groupStage,
		sort:  sortStage,
	}, nil
}

// Process implements Stage interface.
func (s *sortByCount) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) { //nolint:lll // for readability
	iter, err := s.group.Process(ctx, iter, closer)
	if err != nil {
		return nil, err
	}

	iter, err = s.sort.Process(ctx, iter, closer)
	if err != nil {
		return nil, err
	}

	return iter, nil
}

// check interfaces
var (
	_ aggregations.Stage = (*sortByCount)(nil)
)
