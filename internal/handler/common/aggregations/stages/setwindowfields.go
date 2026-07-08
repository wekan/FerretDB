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
	"fmt"
	"math"

	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations/operators"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// setWindowFields represents $setWindowFields stage.
//
//	{ $setWindowFields: {
//		partitionBy: <expression>,
//		sortBy: { <field>: 1|-1, ... },
//		output: {
//			<outputField>: { <windowOperator>: <expression>, window: { documents: [<lower>, <upper>] } },
//			...
//		}
//	}}
//
// It partitions the input documents by the evaluated partitionBy value, sorts each
// partition by sortBy, and for each output field computes a window operator over a
// window of documents relative to the current document.
type setWindowFields struct {
	partitionBy any
	sortBy      *types.Document
	output      []windowOutput
}

// windowOutput represents a single field computed by a window operator.
type windowOutput struct {
	field  string
	op     string
	input  any         // operand expression for accumulators
	shift  *shiftSpec  // set only when op is $shift
	window *windowSpec // nil means the default (full partition) window
	// needsSort is true for operators that require a sortBy.
	needsSort bool
	// isRankKind is true for $rank/$denseRank/$documentNumber.
	isRankKind bool
}

// shiftSpec holds parsed arguments of the $shift window operator.
type shiftSpec struct {
	output     any
	by         int
	hasDefault bool
	def        any
}

// windowBound represents one edge of a documents window.
type windowBound struct {
	unbounded bool
	offset    int
}

// windowSpec represents a documents-based window { documents: [lower, upper] }.
type windowSpec struct {
	lower windowBound
	upper windowBound
}

// rankOperators are the window operators that assign a position within the sorted partition.
var rankOperators = map[string]struct{}{
	"$rank":           {},
	"$denseRank":      {},
	"$documentNumber": {},
}

// accumulatorWindowOperators are the supported window accumulators.
var accumulatorWindowOperators = map[string]struct{}{
	"$sum":        {},
	"$avg":        {},
	"$min":        {},
	"$max":        {},
	"$count":      {},
	"$push":       {},
	"$first":      {},
	"$last":       {},
	"$stdDevPop":  {},
	"$stdDevSamp": {},
}

// deferredWindowOperators lists window operators that are recognized by MongoDB but
// not yet implemented by this fork. They return a clear not-implemented error.
var deferredWindowOperators = map[string]struct{}{
	"$derivative":     {},
	"$integral":       {},
	"$expMovingAvg":   {},
	"$covariancePop":  {},
	"$covarianceSamp": {},
	"$linearFill":     {},
	"$locf":           {},
	"$minN":           {},
	"$maxN":           {},
	"$median":         {},
	"$percentile":     {},
	"$top":            {},
	"$topN":           {},
	"$bottom":         {},
	"$bottomN":        {},
	"$addToSet":       {},
	"$mergeObjects":   {},
}

// newSetWindowFields creates a new $setWindowFields stage.
func newSetWindowFields(stage *types.Document) (aggregations.Stage, error) {
	fields, err := common.GetRequiredParam[*types.Document](stage, "$setWindowFields")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"the $setWindowFields stage specification must be an object",
			"$setWindowFields (stage)",
		)
	}

	swf := new(setWindowFields)

	if partitionBy, err := fields.Get("partitionBy"); err == nil {
		swf.partitionBy = partitionBy
	}

	if sortByVal, err := fields.Get("sortBy"); err == nil {
		sortBy, ok := sortByVal.(*types.Document)
		if !ok {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrTypeMismatch,
				"'$setWindowFields.sortBy' must be an object",
				"$setWindowFields (stage)",
			)
		}

		swf.sortBy = sortBy
	}

	output, err := common.GetRequiredParam[*types.Document](fields, "output")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"'$setWindowFields.output' missing or must be an object",
			"$setWindowFields (stage)",
		)
	}

	outIter := output.Iterator()
	defer outIter.Close()

	for {
		field, v, err := outIter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		out, err := parseWindowOutput(field, v)
		if err != nil {
			return nil, err
		}

		if out.needsSort && swf.sortBy == nil {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrFailedToParse,
				fmt.Sprintf("'%s' requires a sortBy", out.op),
				"$setWindowFields (stage)",
			)
		}

		swf.output = append(swf.output, *out)
	}

	return swf, nil
}

// parseWindowOutput parses a single output field specification.
func parseWindowOutput(field string, v any) (*windowOutput, error) {
	spec, ok := v.(*types.Document)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("The field '%s' must be an object", field),
			"$setWindowFields (stage)",
		)
	}

	var opName string

	var opValue any

	var windowDoc *types.Document

	specIter := spec.Iterator()
	defer specIter.Close()

	opCount := 0

	for {
		k, val, err := specIter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		if k == "window" {
			wd, ok := val.(*types.Document)
			if !ok {
				return nil, handlererrors.NewCommandErrorMsgWithArgument(
					handlererrors.ErrFailedToParse,
					"'window' must be an object",
					"$setWindowFields (stage)",
				)
			}

			windowDoc = wd

			continue
		}

		opName = k
		opValue = val
		opCount++
	}

	if opCount != 1 {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("The field '%s' must specify exactly one window operator", field),
			"$setWindowFields (stage)",
		)
	}

	if _, deferred := deferredWindowOperators[opName]; deferred {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrNotImplemented,
			fmt.Sprintf("window operator %s is not implemented yet", opName),
			"$setWindowFields (stage)",
		)
	}

	out := &windowOutput{
		field: field,
		op:    opName,
	}

	switch {
	case opName == "$shift":
		out.needsSort = true

		shift, err := parseShift(opValue)
		if err != nil {
			return nil, err
		}

		out.shift = shift

	case isRankOperator(opName):
		out.needsSort = true
		out.isRankKind = true

	case isAccumulatorWindowOperator(opName):
		out.input = opValue

		window, err := parseWindow(windowDoc)
		if err != nil {
			return nil, err
		}

		out.window = window

	default:
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("Unrecognized window operator: %s", opName),
			"$setWindowFields (stage)",
		)
	}

	return out, nil
}

// isRankOperator reports whether op is a rank/position window operator.
func isRankOperator(op string) bool {
	_, ok := rankOperators[op]
	return ok
}

// isAccumulatorWindowOperator reports whether op is a supported window accumulator.
func isAccumulatorWindowOperator(op string) bool {
	_, ok := accumulatorWindowOperators[op]
	return ok
}

// parseShift parses the $shift operator arguments.
func parseShift(v any) (*shiftSpec, error) {
	doc, ok := v.(*types.Document)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$shift requires an object argument",
			"$setWindowFields (stage)",
		)
	}

	shift := new(shiftSpec)

	outputVal, err := doc.Get("output")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$shift requires an 'output' expression",
			"$setWindowFields (stage)",
		)
	}

	shift.output = outputVal

	byVal, err := doc.Get("by")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$shift requires a 'by' integer",
			"$setWindowFields (stage)",
		)
	}

	by, err := handlerparams.GetWholeNumberParam(byVal)
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$shift 'by' must be an integer",
			"$setWindowFields (stage)",
		)
	}

	shift.by = int(by)

	if def, err := doc.Get("default"); err == nil {
		shift.hasDefault = true
		shift.def = def
	}

	return shift, nil
}

// parseWindow parses a documents-based window specification. A nil windowDoc means the
// default window covering the whole partition.
func parseWindow(windowDoc *types.Document) (*windowSpec, error) {
	if windowDoc == nil {
		return nil, nil
	}

	if _, err := windowDoc.Get("range"); err == nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrNotImplemented,
			"range windows are not implemented yet",
			"$setWindowFields (stage)",
		)
	}

	documentsVal, err := windowDoc.Get("documents")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"window must contain a 'documents' array",
			"$setWindowFields (stage)",
		)
	}

	arr, ok := documentsVal.(*types.Array)
	if !ok || arr.Len() != 2 {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"'documents' window must be an array of two elements",
			"$setWindowFields (stage)",
		)
	}

	lower, err := parseWindowBound(must.NotFail(arr.Get(0)))
	if err != nil {
		return nil, err
	}

	upper, err := parseWindowBound(must.NotFail(arr.Get(1)))
	if err != nil {
		return nil, err
	}

	return &windowSpec{lower: lower, upper: upper}, nil
}

// parseWindowBound parses one window boundary value.
func parseWindowBound(v any) (windowBound, error) {
	if s, ok := v.(string); ok {
		switch s {
		case "unbounded":
			return windowBound{unbounded: true}, nil
		case "current":
			return windowBound{offset: 0}, nil
		default:
			return windowBound{}, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrFailedToParse,
				fmt.Sprintf("Window bounds must be 'unbounded', 'current', or a number, got %q", s),
				"$setWindowFields (stage)",
			)
		}
	}

	n, err := handlerparams.GetWholeNumberParam(v)
	if err != nil {
		return windowBound{}, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"Numeric window bounds must be integers",
			"$setWindowFields (stage)",
		)
	}

	return windowBound{offset: int(n)}, nil
}

// Process implements Stage interface.
func (s *setWindowFields) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) { //nolint:lll // for readability
	var docs []*types.Document

	for {
		_, doc, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		docs = append(docs, doc.DeepCopy())
	}

	partitions, err := s.partition(docs)
	if err != nil {
		return nil, err
	}

	var res []*types.Document

	for _, part := range partitions {
		if s.sortBy != nil {
			if err = common.SortDocuments(part, s.sortBy); err != nil {
				return nil, lazyerrors.Error(err)
			}
		}

		if err = s.computePartition(part); err != nil {
			return nil, err
		}

		res = append(res, part...)
	}

	resIter := iterator.Values(iterator.ForSlice(res))
	closer.Add(resIter)

	return resIter, nil
}

// partition splits documents into partitions keyed by the evaluated partitionBy value,
// preserving the first-seen order of partitions.
func (s *setWindowFields) partition(docs []*types.Document) ([][]*types.Document, error) {
	if s.partitionBy == nil {
		return [][]*types.Document{docs}, nil
	}

	var keys []any

	var parts [][]*types.Document

	for _, doc := range docs {
		key, _ := evaluateOperand(s.partitionBy, doc)

		idx := -1

		for i, k := range keys {
			if types.CompareForAggregation(key, k) == types.Equal {
				idx = i
				break
			}
		}

		if idx == -1 {
			keys = append(keys, key)
			parts = append(parts, []*types.Document{doc})

			continue
		}

		parts[idx] = append(parts[idx], doc)
	}

	return parts, nil
}

// computePartition computes all output fields for a single sorted partition and sets
// them on the documents.
func (s *setWindowFields) computePartition(part []*types.Document) error {
	for _, out := range s.output {
		switch {
		case out.isRankKind:
			s.computeRank(part, out)

		case out.op == "$shift":
			if err := s.computeShift(part, out); err != nil {
				return err
			}

		default:
			if err := s.computeAccumulator(part, out); err != nil {
				return err
			}
		}
	}

	return nil
}

// computeRank computes $rank, $denseRank and $documentNumber for a sorted partition.
func (s *setWindowFields) computeRank(part []*types.Document, out windowOutput) {
	sortKeys := s.sortBy.Keys()

	var rank, dense int32

	for i, doc := range part {
		tie := false

		if i > 0 {
			tie = sameSortKey(part[i-1], doc, sortKeys)
		}

		switch out.op {
		case "$documentNumber":
			doc.Set(out.field, int32(i+1))

		case "$rank":
			if i == 0 || !tie {
				rank = int32(i + 1)
			}

			doc.Set(out.field, rank)

		case "$denseRank":
			if i == 0 {
				dense = 1
			} else if !tie {
				dense++
			}

			doc.Set(out.field, dense)
		}
	}
}

// computeShift computes the $shift window operator for a sorted partition.
func (s *setWindowFields) computeShift(part []*types.Document, out windowOutput) error {
	spec := out.shift

	for i, doc := range part {
		target := i + spec.by

		var value any

		if target >= 0 && target < len(part) {
			v, found := evaluateOperand(spec.output, part[target])
			if found {
				value = v
			} else {
				value = types.Null
			}
		} else if spec.hasDefault {
			v, found := evaluateOperand(spec.def, doc)
			if found {
				value = v
			} else {
				value = types.Null
			}
		} else {
			value = types.Null
		}

		doc.Set(out.field, value)
	}

	return nil
}

// computeAccumulator computes a window accumulator (over the default or documents window)
// for a sorted partition.
func (s *setWindowFields) computeAccumulator(part []*types.Document, out windowOutput) error {
	for i := range part {
		lo, hi := windowRange(i, len(part), out.window)

		var windowDocs []*types.Document
		if lo <= hi {
			windowDocs = part[lo : hi+1]
		}

		value := computeWindowValue(out.op, out.input, windowDocs)

		part[i].Set(out.field, value)
	}

	return nil
}

// windowRange returns the inclusive [lo, hi] index range of the window for the document
// at index i in a partition of the given length. A nil spec means the whole partition.
func windowRange(i, length int, spec *windowSpec) (int, int) {
	if spec == nil {
		return 0, length - 1
	}

	lo := 0
	if !spec.lower.unbounded {
		lo = i + spec.lower.offset
	}

	hi := length - 1
	if !spec.upper.unbounded {
		hi = i + spec.upper.offset
	}

	if lo < 0 {
		lo = 0
	}

	if hi > length-1 {
		hi = length - 1
	}

	return lo, hi
}

// computeWindowValue applies a window accumulator over the documents in the window.
func computeWindowValue(op string, input any, windowDocs []*types.Document) any {
	switch op {
	case "$count":
		return int32(len(windowDocs))

	case "$first":
		if len(windowDocs) == 0 {
			return types.Null
		}

		v, found := evaluateOperand(input, windowDocs[0])
		if !found {
			return types.Null
		}

		return v

	case "$last":
		if len(windowDocs) == 0 {
			return types.Null
		}

		v, found := evaluateOperand(input, windowDocs[len(windowDocs)-1])
		if !found {
			return types.Null
		}

		return v

	case "$push":
		arr := types.MakeArray(len(windowDocs))

		for _, doc := range windowDocs {
			v, found := evaluateOperand(input, doc)
			if found {
				arr.Append(v)
			}
		}

		return arr
	}

	values := collectOperandValues(input, windowDocs)

	switch op {
	case "$sum":
		return aggregations.SumNumbers(values...)

	case "$avg":
		return avgValues(values)

	case "$min":
		return minMaxValues(values, true)

	case "$max":
		return minMaxValues(values, false)

	case "$stdDevPop":
		return stdDev(values, false)

	case "$stdDevSamp":
		return stdDev(values, true)
	}

	return types.Null
}

// collectOperandValues evaluates the input expression for each document in the window,
// skipping documents where the value does not exist.
func collectOperandValues(input any, windowDocs []*types.Document) []any {
	values := make([]any, 0, len(windowDocs))

	for _, doc := range windowDocs {
		v, found := evaluateOperand(input, doc)
		if found {
			values = append(values, v)
		}
	}

	return values
}

// avgValues returns the average of the numeric values, or null if there are none.
func avgValues(values []any) any {
	var sum float64

	var count int

	for _, v := range values {
		f, ok := toFloat64(v)
		if !ok {
			continue
		}

		sum += f
		count++
	}

	if count == 0 {
		return types.Null
	}

	return sum / float64(count)
}

// minMaxValues returns the minimum or maximum value, or null if there are none.
// Null and missing values are ignored, matching MongoDB $min/$max.
func minMaxValues(values []any, min bool) any {
	var result any

	for _, v := range values {
		if _, isNull := v.(types.NullType); isNull {
			continue
		}

		if result == nil {
			result = v
			continue
		}

		cmp := types.CompareForAggregation(v, result)

		if min && cmp == types.Less {
			result = v
		}

		if !min && cmp == types.Greater {
			result = v
		}
	}

	if result == nil {
		return types.Null
	}

	return result
}

// stdDev returns the population or sample standard deviation of the numeric values.
// It returns null if there are not enough values.
func stdDev(values []any, sample bool) any {
	nums := make([]float64, 0, len(values))

	for _, v := range values {
		if f, ok := toFloat64(v); ok {
			nums = append(nums, f)
		}
	}

	n := len(nums)

	if n == 0 || (sample && n < 2) {
		return types.Null
	}

	var mean float64
	for _, f := range nums {
		mean += f
	}

	mean /= float64(n)

	var sumSq float64
	for _, f := range nums {
		d := f - mean
		sumSq += d * d
	}

	denom := float64(n)
	if sample {
		denom = float64(n - 1)
	}

	return math.Sqrt(sumSq / denom)
}

// toFloat64 converts a numeric value to float64.
func toFloat64(v any) (float64, bool) {
	switch v := v.(type) {
	case float64:
		return v, true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

// sameSortKey reports whether two documents have equal values for all sort keys.
func sameSortKey(a, b *types.Document, sortKeys []string) bool {
	for _, key := range sortKeys {
		path, err := types.NewPathFromString(key)
		if err != nil {
			return false
		}

		var av, bv any = types.Null, types.Null

		if v, err := a.GetByPath(path); err == nil {
			av = v
		}

		if v, err := b.GetByPath(path); err == nil {
			bv = v
		}

		if types.CompareForAggregation(av, bv) != types.Equal {
			return false
		}
	}

	return true
}

// evaluateOperand evaluates an operand expression against a document.
// It returns the evaluated value and whether the value exists.
func evaluateOperand(expr any, doc *types.Document) (any, bool) {
	switch expr := expr.(type) {
	case *types.Document:
		if operators.IsOperator(expr) {
			op, err := operators.NewOperator(expr)
			if err != nil {
				return nil, false
			}

			v, err := op.Process(doc)
			if err != nil {
				return nil, false
			}

			return v, true
		}

		return expr, true
	case string:
		expression, err := aggregations.NewExpression(expr, nil)
		if err != nil {
			var exprErr *aggregations.ExpressionError
			if errors.As(err, &exprErr) && exprErr.Code() == aggregations.ErrNotExpression {
				// plain string literal
				return expr, true
			}

			return nil, false
		}

		v, err := expression.Evaluate(doc)
		if err != nil {
			return nil, false
		}

		return v, true
	default:
		return expr, true
	}
}

// check interfaces
var (
	_ aggregations.Stage = (*setWindowFields)(nil)
)
