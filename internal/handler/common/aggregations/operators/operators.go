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

// Package operators provides aggregation operators.
// Operators are used in aggregation stages to filter and model data.
// This package contains all operators apart from the accumulation operators,
// which are stored and described in accumulators package.
//
// Accumulators that can be used outside of accumulation with different behaviour (like `$sum`),
// should be stored in both operators and accumulators packages.
package operators

import (
	"errors"
	"fmt"
	"strings"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// newOperatorFunc is a type for a function that creates a standard aggregation operator.
//
// By standard aggregation operator we mean any operator that is not accumulator.
// While accumulators perform operations on multiple documents
// (for example `$count` can count documents in each `$group` group),
// standard operators perform operations on a single document.
// It takes the arguments extracted from the document, and not the
// whole array/document.
type newOperatorFunc func(args ...any) (Operator, error)

// Operator is a common interface for standard aggregation operators.
type Operator interface {
	// Process document and returns the result of applying operator.
	Process(in *types.Document) (any, error)
}

// IsOperator returns true if provided document should be
// treated as operator document.
func IsOperator(doc *types.Document) bool {
	iter := doc.Iterator()
	defer iter.Close()

	for {
		key, _, err := iter.Next()
		if err != nil {
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			return false
		}

		if strings.HasPrefix(key, "$") {
			return true
		}
	}

	return false
}

// NewOperator returns operator from provided document.
// The document should look like: `{<$operator>: <operator-value>}`.
//
// Before calling NewOperator on document it's recommended to validate
// document before by using IsOperator on it.
func NewOperator(doc *types.Document) (Operator, error) {
	if doc.Len() == 0 {
		return nil, lazyerrors.New(
			"The operator field is empty",
		)
	}

	if doc.Len() > 1 {
		return nil, newOperatorError(
			ErrTooManyFields,
			doc.Command(),
			"The operator field specifies more than one operator",
		)
	}

	operator := doc.Command()

	newOperator, supported := Operators[operator]
	_, unsupported := unsupportedOperators[operator]

	expr := must.NotFail(doc.Get(operator))

	var args []any

	if arr, ok := expr.(*types.Array); ok {
		iter := arr.Iterator()
		defer iter.Close()

		for {
			_, v, err := iter.Next()

			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			args = append(args, v)
		}
	} else {
		args = append(args, expr)
	}

	switch {
	case supported:
		return newOperator(args...)
	case unsupported:
		return nil, newOperatorError(
			ErrNotImplemented,
			operator,
			fmt.Sprintf("The operator %s is not implemented yet", operator),
		)
	default:
		return nil, newOperatorError(
			ErrInvalidExpression,
			operator,
			fmt.Sprintf("Unrecognized expression '%s'", operator),
		)
	}
}

// Operators maps all standard aggregation operators.
var Operators = map[string]newOperatorFunc{
	// sorted alphabetically
	"$abs":              newAbs,
	"$acos":             newAcos,
	"$acosh":            newAcosh,
	"$add":              newAdd,
	"$allElementsTrue":  newAllElementsTrue,
	"$and":              newAnd,
	"$anyElementTrue":   newAnyElementTrue,
	"$arrayElemAt":      newArrayElemAt,
	"$arrayToObject":    newArrayToObject,
	"$asin":             newAsin,
	"$asinh":            newAsinh,
	"$atan":             newAtan,
	"$atan2":            newAtan2,
	"$atanh":            newAtanh,
	"$avg":              newAvg,
	"$binarySize":       newBinarySize,
	"$bsonSize":         newBsonSize,
	"$ceil":             newCeil,
	"$cmp":              newCmp,
	"$concat":           newConcat,
	"$concatArrays":     newConcatArrays,
	"$cond":             newCond,
	"$convert":          newConvert,
	"$cos":              newCos,
	"$cosh":             newCosh,
	"$dateAdd":          newDateAdd,
	"$dateDiff":         newDateDiff,
	"$dateFromParts":    newDateFromParts,
	"$dateFromString":   newDateFromString,
	"$dateSubtract":     newDateSubtract,
	"$dateToParts":      newDateToParts,
	"$dateToString":     newDateToString,
	"$dateTrunc":        newDateTrunc,
	"$dayOfMonth":       newDayOfMonth,
	"$dayOfWeek":        newDayOfWeek,
	"$dayOfYear":        newDayOfYear,
	"$degreesToRadians": newDegreesToRadians,
	"$divide":           newDivide,
	"$eq":               newEq,
	"$exp":              newExp,
	"$filter":           newFilter,
	"$floor":            newFloor,
	"$getField":         newGetField,
	"$gt":               newGt,
	"$gte":              newGte,
	"$hour":             newHour,
	"$ifNull":           newIfNull,
	"$in":               newIn,
	"$indexOfArray":     newIndexOfArray,
	"$indexOfBytes":     newIndexOfBytes,
	"$indexOfCP":        newIndexOfCP,
	"$isArray":          newIsArray,
	"$isNumber":         newIsNumber,
	"$isoDayOfWeek":     newIsoDayOfWeek,
	"$isoWeek":          newIsoWeek,
	"$isoWeekYear":      newIsoWeekYear,
	"$let":              newLet,
	"$literal":          newLiteral,
	"$ln":               newLn,
	"$log":              newLog,
	"$log10":            newLog10,
	"$lt":               newLt,
	"$lte":              newLte,
	"$ltrim":            newLtrim,
	"$map":              newMap,
	"$max":              newMax,
	"$millisecond":      newMillisecond,
	"$min":              newMin,
	"$minute":           newMinute,
	"$mod":              newMod,
	"$month":            newMonth,
	"$multiply":         newMultiply,
	"$ne":               newNe,
	"$not":              newNot,
	"$objectToArray":    newObjectToArray,
	"$or":               newOr,
	"$pow":              newPow,
	"$radiansToDegrees": newRadiansToDegrees,
	"$rand":             newRand,
	"$range":            newRange,
	"$reduce":           newReduce,
	"$regexMatch":       newRegexMatch,
	"$replaceAll":       newReplaceAll,
	"$replaceOne":       newReplaceOne,
	"$reverseArray":     newReverseArray,
	"$round":            newRound,
	"$rtrim":            newRtrim,
	"$second":           newSecond,
	"$setDifference":    newSetDifference,
	"$setEquals":        newSetEquals,
	"$setIntersection":  newSetIntersection,
	"$setIsSubset":      newSetIsSubset,
	"$setField":         newSetField,
	"$setUnion":         newSetUnion,
	"$sin":              newSin,
	"$sinh":             newSinh,
	"$size":             newSize,
	"$slice":            newSlice,
	"$sortArray":        newSortArray,
	"$split":            newSplit,
	"$sqrt":             newSqrt,
	"$strcasecmp":       newStrcasecmp,
	"$strLenBytes":      newStrLenBytes,
	"$strLenCP":         newStrLenCP,
	"$substr":           newSubstr,
	"$substrBytes":      newSubstrBytesOp,
	"$substrCP":         newSubstrCP,
	"$subtract":         newSubtract,
	"$sum":              newSum,
	"$switch":           newSwitch,
	"$tan":              newTan,
	"$tanh":             newTanh,
	"$toBool":           newToBool,
	"$toDate":           newToDate,
	"$toDouble":         newToDouble,
	"$toInt":            newToInt,
	"$toLong":           newToLong,
	"$toLower":          newToLower,
	"$toObjectId":       newToObjectID,
	"$toString":         newToString,
	"$toUpper":          newToUpper,
	"$trim":             newTrim,
	"$trunc":            newTrunc,
	"$type":             newType,
	"$unsetField":       newUnsetField,
	"$week":             newWeek,
	"$year":             newYear,
	"$zip":              newZip,
	// please keep sorted alphabetically
}

// unsupportedOperators maps all unsupported yet operators.
var unsupportedOperators = map[string]struct{}{
	// sorted alphabetically
	"$covariancePop":  {},
	"$covarianceSamp": {},
	"$denseRank":      {},
	"$derivative":     {},
	"$documentNumber": {},
	"$expMovingAvg":   {},
	"$function":       {},
	"$integral":       {},
	"$linearFill":     {},
	"$locf":           {},
	"$meta":           {},
	"$minN":           {},
	"$rank":           {},
	"$regexFind":      {},
	"$regexFindAll":   {},
	"$sampleRate":     {},
	"$shift":          {},
	"$stdDevPop":      {},
	"$stdDevSamp":     {},
	"$toDecimal":      {},
	"$tsIncrement":    {},
	"$tsSecond":       {},
	// please keep sorted alphabetically
}
