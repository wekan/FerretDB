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
	"fmt"

	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// $facet is registered here rather than in the Stages map literal to avoid a
// static initialization cycle: newFacet -> NewStage -> Stages -> newFacet.
// init runs after package-level variables are initialized, breaking the cycle.
func init() {
	Stages["$facet"] = newFacet
}

// facetDisallowedStages contains the aggregation stages that must not appear
// inside a $facet sub-pipeline.
var facetDisallowedStages = map[string]struct{}{
	"$facet":        {},
	"$out":          {},
	"$merge":        {},
	"$collStats":    {},
	"$geoNear":      {},
	"$changeStream": {},
}

// facetField represents a single named sub-pipeline of a $facet stage.
type facetField struct {
	name   string
	stages []aggregations.Stage
}

// facet represents the $facet stage.
//
//	{ $facet: { <outputField1>: [ <stage>, ... ], <outputField2>: [ <stage>, ... ], ... } }
//
// It processes multiple sub-pipelines over the same set of input documents and
// produces a single output document mapping each output field to the array of
// documents produced by its sub-pipeline.
type facet struct {
	fields []facetField
}

// newFacet creates a new $facet stage.
func newFacet(stage *types.Document) (aggregations.Stage, error) {
	spec, err := common.GetRequiredParam[*types.Document](stage, "$facet")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrStageInvalid,
			"$facet specification stage must be an object",
			"$facet (stage)",
		)
	}

	if spec.Len() == 0 {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrStageInvalid,
			"$facet specification must contain at least one field",
			"$facet (stage)",
		)
	}

	fields := make([]facetField, 0, spec.Len())

	// iterate in field order to preserve output order.
	for _, name := range spec.Keys() {
		v := must.NotFail(spec.Get(name))

		pipeline, ok := v.(*types.Array)
		if !ok {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrStageInvalid,
				fmt.Sprintf("arguments to $facet must be arrays, %s is not an array", name),
				"$facet (stage)",
			)
		}

		subStages := make([]aggregations.Stage, 0, pipeline.Len())

		for i := 0; i < pipeline.Len(); i++ {
			stageDoc, ok := must.NotFail(pipeline.Get(i)).(*types.Document)
			if !ok {
				return nil, handlererrors.NewCommandErrorMsgWithArgument(
					handlererrors.ErrStageInvalid,
					"$facet pipeline elements must be objects",
					"$facet (stage)",
				)
			}

			if _, disallowed := facetDisallowedStages[stageDoc.Command()]; disallowed {
				return nil, handlererrors.NewCommandErrorMsgWithArgument(
					handlererrors.ErrStageInvalid,
					fmt.Sprintf("%s is not allowed to be used within a $facet stage", stageDoc.Command()),
					"$facet (stage)",
				)
			}

			// Note: a nested $lookup does NOT receive the "from" collection injection
			// that msg_aggregate.go performs at the top level of the pipeline, so a
			// nested $lookup is effectively unsupported here and will produce empty
			// results. We deliberately do not attempt to wire that injection into $facet.
			subStage, err := NewStage(stageDoc)
			if err != nil {
				return nil, err
			}

			subStages = append(subStages, subStage)
		}

		fields = append(fields, facetField{
			name:   name,
			stages: subStages,
		})
	}

	return &facet{
		fields: fields,
	}, nil
}

// Process implements Stage interface.
func (f *facet) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) { //nolint:lll // for readability
	// Buffer the full input once; each sub-pipeline runs over the same set.
	docs, err := iterator.ConsumeValues(iter)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	res := types.MakeDocument(len(f.fields))

	for _, field := range f.fields {
		// Build a fresh in-memory iterator from copies of the buffered input
		// documents so that mutating stages in one sub-pipeline cannot affect
		// another sub-pipeline (or the shared buffer).
		input := make([]*types.Document, len(docs))
		for i, doc := range docs {
			input[i] = doc.DeepCopy()
		}

		subIter := iterator.Values(iterator.ForSlice(input))
		closer.Add(subIter)

		for _, s := range field.stages {
			if subIter, err = s.Process(ctx, subIter, closer); err != nil {
				return nil, err
			}
		}

		out, err := iterator.ConsumeValues(subIter)
		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		arr := types.MakeArray(len(out))
		for _, doc := range out {
			arr.Append(doc)
		}

		res.Set(field.name, arr)
	}

	resIter := iterator.Values(iterator.ForSlice([]*types.Document{res}))
	closer.Add(resIter)

	return resIter, nil
}

// check interfaces
var (
	_ aggregations.Stage = (*facet)(nil)
)
