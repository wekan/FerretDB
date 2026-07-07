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

	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// $unionWith is registered here (via init) rather than in the Stages map literal for the
// same reason as $facet: its sub-pipeline form calls NewStage, and referencing NewStage
// (which reads the Stages map) from a Stages map initializer would create a static
// initialization cycle: newUnionWith -> NewStage -> Stages -> newUnionWith. init runs after
// package-level variables are initialized, breaking the cycle.
func init() {
	Stages["$unionWith"] = newUnionWith
}

// unionWithDisallowedStages contains the aggregation stages that must not appear inside a
// $unionWith sub-pipeline (MongoDB disallows writing stages there).
var unionWithDisallowedStages = map[string]struct{}{
	"$out":   {},
	"$merge": {},
}

// UnionWith represents the $unionWith stage:
//
//	{ $unionWith: "<collName>" }
//	{ $unionWith: { coll: "<collName>", pipeline: [ <stage>, ... ] } }
//
// It first yields every input document unchanged (pass-through), then yields the documents of
// the `coll` collection; if a `pipeline` is present, those documents are first run through the
// sub-pipeline.
//
// Because an aggregation stage only receives the incoming document iterator and has no access to
// the database/backend handle, the documents of the `coll` collection cannot be fetched by the
// stage itself. They are pre-fetched in msg_aggregate.go (which does have the database handle)
// and injected via SetFromDocuments before Process runs. If injection did not happen (e.g. a
// $unionWith nested inside a $facet sub-pipeline, or on the non-documents/$collStats path),
// fromDocs stays nil and the unioned side contributes no documents.
type UnionWith struct {
	coll     string
	pipeline []aggregations.Stage

	// fromDocs holds all documents of the `coll` collection, injected via SetFromDocuments.
	fromDocs []*types.Document
}

// newUnionWith creates a new $unionWith stage.
func newUnionWith(stage *types.Document) (aggregations.Stage, error) {
	spec, err := stage.Get("$unionWith")
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	switch spec := spec.(type) {
	case string:
		// Short form: { $unionWith: "<collName>" }.
		return &UnionWith{coll: spec}, nil

	case *types.Document:
		// Long form: { $unionWith: { coll: "<collName>", pipeline: [ ... ] } }.
		return newUnionWithObject(spec)

	default:
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf(
				"the $unionWith stage specification must be a string or an object, got %s",
				types.FormatAnyValue(spec),
			),
			"$unionWith (stage)",
		)
	}
}

// newUnionWithObject creates a $unionWith stage from its object form.
func newUnionWithObject(specDoc *types.Document) (aggregations.Stage, error) {
	collV, err := specDoc.Get("coll")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"missing 'coll' option to $unionWith stage specification",
			"$unionWith (stage)",
		)
	}

	coll, ok := collV.(string)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("'coll' option to $unionWith stage must be a string, is type %s", types.FormatAnyValue(collV)),
			"$unionWith (stage)",
		)
	}

	res := &UnionWith{coll: coll}

	pipelineV, err := specDoc.Get("pipeline")
	if err != nil {
		// pipeline is optional.
		return res, nil
	}

	pipeline, ok := pipelineV.(*types.Array)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("'pipeline' option to $unionWith stage must be an array, is type %s", types.FormatAnyValue(pipelineV)),
			"$unionWith (stage)",
		)
	}

	subStages := make([]aggregations.Stage, 0, pipeline.Len())

	for i := 0; i < pipeline.Len(); i++ {
		stageDoc, ok := must.NotFail(pipeline.Get(i)).(*types.Document)
		if !ok {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrTypeMismatch,
				"$unionWith pipeline elements must be objects",
				"$unionWith (stage)",
			)
		}

		if _, disallowed := unionWithDisallowedStages[stageDoc.Command()]; disallowed {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrStageInvalid,
				fmt.Sprintf("%s is not allowed within a $unionWith's sub-pipeline", stageDoc.Command()),
				"$unionWith (stage)",
			)
		}

		// Note: a nested stage that itself needs database-handle injection (such as a $lookup or a
		// further $unionWith) does NOT receive that injection here, since msg_aggregate.go only
		// injects into top-level pipeline stages. Such nested stages are effectively unsupported and
		// contribute empty results, mirroring the same limitation in $facet.
		subStage, err := NewStage(stageDoc)
		if err != nil {
			return nil, err
		}

		subStages = append(subStages, subStage)
	}

	res.pipeline = subStages

	return res, nil
}

// Coll returns the name of the collection whose documents are unioned into the pipeline.
func (u *UnionWith) Coll() string {
	return u.coll
}

// SetFromDocuments injects the pre-fetched documents of the `coll` collection.
func (u *UnionWith) SetFromDocuments(docs []*types.Document) {
	u.fromDocs = docs
}

// Process implements Stage interface.
func (u *UnionWith) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) { //nolint:lll // for readability
	// Yield every input document unchanged first (pass-through side of the union).
	docs, err := iterator.ConsumeValues(iter)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	// Build the unioned side from the injected `coll` documents. Copy them so the sub-pipeline
	// cannot mutate the shared injected slice.
	unionDocs := make([]*types.Document, len(u.fromDocs))
	for i, doc := range u.fromDocs {
		unionDocs[i] = doc.DeepCopy()
	}

	if len(u.pipeline) > 0 {
		subIter := iterator.Values(iterator.ForSlice(unionDocs))
		closer.Add(subIter)

		for _, s := range u.pipeline {
			if subIter, err = s.Process(ctx, subIter, closer); err != nil {
				return nil, err
			}
		}

		if unionDocs, err = iterator.ConsumeValues(subIter); err != nil {
			return nil, lazyerrors.Error(err)
		}
	}

	out := make([]*types.Document, 0, len(docs)+len(unionDocs))
	out = append(out, docs...)
	out = append(out, unionDocs...)

	resIter := iterator.Values(iterator.ForSlice(out))
	closer.Add(resIter)

	return resIter, nil
}

// check interfaces
var (
	_ aggregations.Stage = (*UnionWith)(nil)
)
