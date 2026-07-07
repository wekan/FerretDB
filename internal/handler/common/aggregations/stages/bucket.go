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
	"errors"
	gosort "sort"

	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations/operators/accumulators"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// $bucket and $bucketAuto are registered here rather than in the Stages map
// literal to keep the grouping-related stages grouped together; there is no
// initialization cycle because neither stage calls NewStage.
func init() {
	Stages["$bucket"] = newBucket
	Stages["$bucketAuto"] = newBucketAuto
}

// bucket represents the $bucket stage.
//
//	{ $bucket: {
//		groupBy: <expression>,
//		boundaries: [ <lowerbound1>, <lowerbound2>, ... ],
//		default: <literal>,
//		output: { <output1>: { <accumulator>: <expression> }, ... }
//	}}
//
// It categorizes incoming documents into groups (buckets) based on the value of
// groupBy and the half-open ranges defined by boundaries.
type bucket struct {
	groupBy    any
	defaultVal any
	output     []groupBy
	boundaries []any
	hasDefault bool
}

// newBucket creates a new $bucket stage.
func newBucket(stage *types.Document) (aggregations.Stage, error) {
	fields, err := common.GetRequiredParam[*types.Document](stage, "$bucket")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrStageInvalid,
			"$bucket specification stage must be an object",
			"$bucket (stage)",
		)
	}

	groupByVal, err := fields.Get("groupBy")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$bucket requires 'groupBy' and 'boundaries' to be specified.",
			"$bucket (stage)",
		)
	}

	boundariesVal, err := fields.Get("boundaries")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$bucket requires 'groupBy' and 'boundaries' to be specified.",
			"$bucket (stage)",
		)
	}

	boundariesArr, ok := boundariesVal.(*types.Array)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"The $bucket 'boundaries' field must be an array, but found a non-array value.",
			"$bucket (stage)",
		)
	}

	if boundariesArr.Len() < 2 {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"The $bucket 'boundaries' field must have at least 2 values, but found fewer.",
			"$bucket (stage)",
		)
	}

	boundaries := make([]any, boundariesArr.Len())
	for i := 0; i < boundariesArr.Len(); i++ {
		boundaries[i] = must.NotFail(boundariesArr.Get(i))

		if i > 0 && types.Compare(boundaries[i-1], boundaries[i]) != types.Less {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrFailedToParse,
				"The 'boundaries' option to $bucket must be sorted in ascending order.",
				"$bucket (stage)",
			)
		}
	}

	b := &bucket{
		groupBy:    groupByVal,
		boundaries: boundaries,
	}

	if defaultVal, err := fields.Get("default"); err == nil {
		b.hasDefault = true
		b.defaultVal = defaultVal
	}

	outputVal, err := fields.Get("output")
	if err != nil {
		// default output counts documents in each bucket.
		acc, err := accumulators.NewAccumulator("$bucket", "count", must.NotFail(types.NewDocument("$sum", int32(1))))
		if err != nil {
			return nil, err
		}

		b.output = []groupBy{{outputField: "count", accumulator: acc}}

		return b, nil
	}

	outputDoc, ok := outputVal.(*types.Document)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"The $bucket 'output' field must be an object, but found a non-object value.",
			"$bucket (stage)",
		)
	}

	output, err := parseBucketOutput("$bucket", outputDoc)
	if err != nil {
		return nil, err
	}

	b.output = output

	return b, nil
}

// Process implements Stage interface.
func (b *bucket) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) { //nolint:lll // for readability
	docs, err := iterator.ConsumeValues(iter)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	buckets := make([][]*types.Document, len(b.boundaries)-1)

	var defaultDocs []*types.Document

	for _, doc := range docs {
		v, err := evalGroupBy(b.groupBy, doc)
		if err != nil {
			return nil, processGroupStageError(err)
		}

		idx := b.findBucket(v)
		if idx < 0 {
			if !b.hasDefault {
				return nil, handlererrors.NewCommandErrorMsgWithArgument(
					handlererrors.ErrFailedToParse,
					"$bucket could not find a matching branch for an input, and no default was specified.",
					"$bucket (stage)",
				)
			}

			defaultDocs = append(defaultDocs, doc)

			continue
		}

		buckets[idx] = append(buckets[idx], doc)
	}

	var res []*types.Document

	for i, bucketDocs := range buckets {
		if len(bucketDocs) == 0 {
			continue
		}

		doc, err := buildBucketOutput(b.boundaries[i], bucketDocs, b.output)
		if err != nil {
			return nil, err
		}

		res = append(res, doc)
	}

	// the default bucket is always sorted last.
	if len(defaultDocs) > 0 {
		doc, err := buildBucketOutput(b.defaultVal, defaultDocs, b.output)
		if err != nil {
			return nil, err
		}

		res = append(res, doc)
	}

	resIter := iterator.Values(iterator.ForSlice(res))
	closer.Add(resIter)

	return resIter, nil
}

// findBucket returns the index of the bucket whose half-open range [lower, upper)
// contains v, or -1 if v is outside all boundaries.
func (b *bucket) findBucket(v any) int {
	// v must be within [boundaries[0], boundaries[last]).
	if types.Compare(v, b.boundaries[0]) == types.Less {
		return -1
	}

	if types.Compare(v, b.boundaries[len(b.boundaries)-1]) != types.Less {
		return -1
	}

	for i := 0; i < len(b.boundaries)-1; i++ {
		if types.Compare(v, b.boundaries[i]) != types.Less && types.Compare(v, b.boundaries[i+1]) == types.Less {
			return i
		}
	}

	return -1
}

// bucketAuto represents the $bucketAuto stage.
//
//	{ $bucketAuto: {
//		groupBy: <expression>,
//		buckets: <number>,
//		output: { <output1>: { <accumulator>: <expression> }, ... },
//		granularity: <string>
//	}}
//
// It categorizes documents into a specified number of buckets, each with roughly
// equal numbers of documents.
type bucketAuto struct {
	groupBy any
	output  []groupBy
	buckets int64
}

// newBucketAuto creates a new $bucketAuto stage.
func newBucketAuto(stage *types.Document) (aggregations.Stage, error) {
	fields, err := common.GetRequiredParam[*types.Document](stage, "$bucketAuto")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrStageInvalid,
			"$bucketAuto specification stage must be an object",
			"$bucketAuto (stage)",
		)
	}

	groupByVal, err := fields.Get("groupBy")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$bucketAuto requires 'groupBy' and 'buckets' to be specified.",
			"$bucketAuto (stage)",
		)
	}

	bucketsVal, err := fields.Get("buckets")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$bucketAuto requires 'groupBy' and 'buckets' to be specified.",
			"$bucketAuto (stage)",
		)
	}

	buckets, err := handlerparams.GetWholeNumberParam(bucketsVal)
	if err != nil || buckets <= 0 {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"The $bucketAuto 'buckets' field must be a positive integer, but found a non-positive value.",
			"$bucketAuto (stage)",
		)
	}

	if _, err := fields.Get("granularity"); err == nil {
		// TODO granularity (e.g. "R5", "POWERSOF2") is not implemented yet.
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrNotImplemented,
			"$bucketAuto 'granularity' option is not implemented yet",
			"$bucketAuto (stage)",
		)
	}

	ba := &bucketAuto{
		groupBy: groupByVal,
		buckets: buckets,
	}

	outputVal, err := fields.Get("output")
	if err != nil {
		acc, err := accumulators.NewAccumulator("$bucketAuto", "count", must.NotFail(types.NewDocument("$sum", int32(1))))
		if err != nil {
			return nil, err
		}

		ba.output = []groupBy{{outputField: "count", accumulator: acc}}

		return ba, nil
	}

	outputDoc, ok := outputVal.(*types.Document)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"The $bucketAuto 'output' field must be an object, but found a non-object value.",
			"$bucketAuto (stage)",
		)
	}

	output, err := parseBucketOutput("$bucketAuto", outputDoc)
	if err != nil {
		return nil, err
	}

	ba.output = output

	return ba, nil
}

// bucketValue pairs the evaluated groupBy value with its document.
type bucketValue struct {
	value any
	doc   *types.Document
}

// Process implements Stage interface.
func (b *bucketAuto) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) { //nolint:lll // for readability
	docs, err := iterator.ConsumeValues(iter)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	pairs := make([]bucketValue, len(docs))

	for i, doc := range docs {
		v, err := evalGroupBy(b.groupBy, doc)
		if err != nil {
			return nil, processGroupStageError(err)
		}

		pairs[i] = bucketValue{value: v, doc: doc}
	}

	gosort.SliceStable(pairs, func(i, j int) bool {
		return types.Compare(pairs[i].value, pairs[j].value) == types.Less
	})

	var res []*types.Document

	n := int(b.buckets)
	remaining := len(pairs)
	idx := 0

	for bkt := 0; bkt < n && idx < len(pairs); bkt++ {
		// approximate even split, giving any remainder to the earlier buckets.
		size := remaining / (n - bkt)
		if remaining%(n-bkt) != 0 {
			size++
		}

		end := idx + size
		if end > len(pairs) {
			end = len(pairs)
		}

		// documents with the same groupBy value must stay in the same bucket, so
		// extend the boundary while the next value equals the last included value.
		for end < len(pairs) && types.Compare(pairs[end].value, pairs[end-1].value) == types.Equal {
			end++
		}

		min := pairs[idx].value

		var max any
		if end < len(pairs) {
			max = pairs[end].value
		} else {
			max = pairs[len(pairs)-1].value
		}

		bucketDocs := make([]*types.Document, end-idx)
		for i := idx; i < end; i++ {
			bucketDocs[i-idx] = pairs[i].doc
		}

		id := must.NotFail(types.NewDocument("min", min, "max", max))

		doc, err := buildBucketOutput(id, bucketDocs, b.output)
		if err != nil {
			return nil, err
		}

		res = append(res, doc)

		remaining -= end - idx
		idx = end
	}

	resIter := iterator.Values(iterator.ForSlice(res))
	closer.Add(resIter)

	return resIter, nil
}

// parseBucketOutput parses the output specification of $bucket / $bucketAuto into
// a slice of accumulators, reusing the $group accumulator machinery.
func parseBucketOutput(stage string, output *types.Document) ([]groupBy, error) {
	res := make([]groupBy, 0, output.Len())

	iter := output.Iterator()
	defer iter.Close()

	for {
		field, v, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		accumulator, err := accumulators.NewAccumulator(stage, field, v)
		if err != nil {
			return nil, processGroupStageError(err)
		}

		res = append(res, groupBy{
			outputField: field,
			accumulator: accumulator,
		})
	}

	return res, nil
}

// buildBucketOutput builds a single bucket output document with the given _id,
// applying every accumulator over the bucket's documents.
func buildBucketOutput(id any, docs []*types.Document, output []groupBy) (*types.Document, error) {
	doc := must.NotFail(types.NewDocument("_id", id))

	for _, acc := range output {
		// each accumulator consumes its own single-pass iterator.
		groupIter := iterator.Values(iterator.ForSlice(docs))

		out, err := acc.accumulator.Accumulate(groupIter)
		groupIter.Close()

		if err != nil {
			return nil, processGroupStageError(err)
		}

		doc.Set(acc.outputField, out)
	}

	return doc, nil
}

// evalGroupBy evaluates a groupBy expression (as used by $bucket and $bucketAuto)
// against a document, mirroring how $group evaluates its _id expression.
func evalGroupBy(groupBy any, doc *types.Document) (any, error) {
	switch groupBy := groupBy.(type) {
	case *types.Document:
		return evaluateDocument(groupBy, doc, false)
	case string:
		expression, err := aggregations.NewExpression(groupBy, nil)
		if err != nil {
			var exprErr *aggregations.ExpressionError
			if errors.As(err, &exprErr) && exprErr.Code() == aggregations.ErrNotExpression {
				return groupBy, nil
			}

			return nil, err
		}

		val, err := expression.Evaluate(doc)
		if err != nil {
			// treat non-existent fields as null, like $group.
			return types.Null, nil
		}

		return val, nil
	default:
		return groupBy, nil
	}
}

// check interfaces
var (
	_ aggregations.Stage = (*bucket)(nil)
	_ aggregations.Stage = (*bucketAuto)(nil)
)
