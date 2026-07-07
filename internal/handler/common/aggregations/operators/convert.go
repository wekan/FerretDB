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
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
)

// isoDateLayout is the layout used to format dates converted to strings and is
// the primary layout accepted when parsing a string into a date. It matches the
// MongoDB `%Y-%m-%dT%H:%M:%S.%LZ` representation.
const isoDateLayout = "2006-01-02T15:04:05.000Z07:00"

// dateParseLayouts lists the layouts accepted when converting a string to a date.
var dateParseLayouts = []string{
	isoDateLayout,
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// convertError returns a MongoDB-style conversion failure error.
func convertError(msg string) error {
	return handlererrors.NewCommandErrorMsgWithArgument(
		handlererrors.ErrTypeMismatch,
		msg,
		"$convert",
	)
}

// toStringValueConv returns the string form of a BSON value, matching the
// MongoDB `$toString` conversion rules. The caller handles null and missing.
func toStringValueConv(v any) (any, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case int32:
		return strconv.FormatInt(int64(v), 10), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case bool:
		return strconv.FormatBool(v), nil
	case time.Time:
		return v.UTC().Format(isoDateLayout), nil
	case types.ObjectID:
		return hex.EncodeToString(v[:]), nil
	default:
		return nil, convertError(fmt.Sprintf("Unsupported conversion from %s to string", handlerparams.AliasFromType(v)))
	}
}

// toIntValueConv converts a BSON value to a 32-bit integer.
func toIntValueConv(v any) (any, error) {
	switch v := v.(type) {
	case int32:
		return v, nil
	case int64:
		if v > math.MaxInt32 || v < math.MinInt32 {
			return nil, convertError(fmt.Sprintf("Conversion would overflow target type: %d", v))
		}

		return int32(v), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, convertError(fmt.Sprintf("Attempt to convert %v to integer", v))
		}

		t := math.Trunc(v)
		if t > math.MaxInt32 || t < math.MinInt32 {
			return nil, convertError(fmt.Sprintf("Conversion would overflow target type: %v", v))
		}

		return int32(t), nil
	case bool:
		if v {
			return int32(1), nil
		}

		return int32(0), nil
	case string:
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return nil, convertError(fmt.Sprintf("Failed to parse number '%s' in $convert with no onError value: %v", v, err))
		}

		return int32(n), nil
	default:
		return nil, convertError(fmt.Sprintf("Unsupported conversion from %s to int", handlerparams.AliasFromType(v)))
	}
}

// toLongValueConv converts a BSON value to a 64-bit integer.
func toLongValueConv(v any) (any, error) {
	switch v := v.(type) {
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, convertError(fmt.Sprintf("Attempt to convert %v to long", v))
		}

		t := math.Trunc(v)
		if t >= math.MaxInt64 || t < math.MinInt64 {
			return nil, convertError(fmt.Sprintf("Conversion would overflow target type: %v", v))
		}

		return int64(t), nil
	case bool:
		if v {
			return int64(1), nil
		}

		return int64(0), nil
	case time.Time:
		return v.UnixMilli(), nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, convertError(fmt.Sprintf("Failed to parse number '%s' in $convert with no onError value: %v", v, err))
		}

		return n, nil
	default:
		return nil, convertError(fmt.Sprintf("Unsupported conversion from %s to long", handlerparams.AliasFromType(v)))
	}
}

// toDoubleValueConv converts a BSON value to a double.
func toDoubleValueConv(v any) (any, error) {
	switch v := v.(type) {
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case float64:
		return v, nil
	case bool:
		if v {
			return float64(1), nil
		}

		return float64(0), nil
	case time.Time:
		return float64(v.UnixMilli()), nil
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, convertError(fmt.Sprintf("Failed to parse number '%s' in $convert with no onError value: %v", v, err))
		}

		return f, nil
	default:
		return nil, convertError(fmt.Sprintf("Unsupported conversion from %s to double", handlerparams.AliasFromType(v)))
	}
}

// toBoolValueConv converts a BSON value to a boolean, matching MongoDB rules:
// numbers are true when non-zero, every other non-null value is true.
func toBoolValueConv(v any) (any, error) {
	switch v := v.(type) {
	case bool:
		return v, nil
	case int32:
		return v != 0, nil
	case int64:
		return v != 0, nil
	case float64:
		return v != 0, nil
	default:
		return true, nil
	}
}

// toObjectIDValueConv converts a BSON value to an ObjectID.
func toObjectIDValueConv(v any) (any, error) {
	switch v := v.(type) {
	case types.ObjectID:
		return v, nil
	case string:
		b, err := hex.DecodeString(v)
		if err != nil || len(b) != types.ObjectIDLen {
			return nil, convertError(fmt.Sprintf(
				"Failed to parse objectId '%s' in $convert with no onError value: "+
					"Invalid string length for parsing to ObjectId, expected 24 but found %d", v, len(v),
			))
		}

		var oid types.ObjectID
		copy(oid[:], b)

		return oid, nil
	default:
		return nil, convertError(fmt.Sprintf("Unsupported conversion from %s to objectId", handlerparams.AliasFromType(v)))
	}
}

// toDateValueConv converts a BSON value to a date.
func toDateValueConv(v any) (any, error) {
	switch v := v.(type) {
	case time.Time:
		return v, nil
	case int32:
		return time.UnixMilli(int64(v)).UTC(), nil
	case int64:
		return time.UnixMilli(v).UTC(), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, convertError(fmt.Sprintf("Attempt to convert %v to date", v))
		}

		return time.UnixMilli(int64(math.Trunc(v))).UTC(), nil
	case types.ObjectID:
		secs := int64(uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3]))
		return time.Unix(secs, 0).UTC(), nil
	case string:
		for _, layout := range dateParseLayouts {
			if t, err := time.Parse(layout, v); err == nil {
				return t.UTC(), nil
			}
		}

		return nil, convertError(fmt.Sprintf("Error parsing date string '%s'; unsupported format", v))
	default:
		return nil, convertError(fmt.Sprintf("Unsupported conversion from %s to date", handlerparams.AliasFromType(v)))
	}
}

// convertValue converts v to the type identified by code, returning a conversion
// error on failure. Null and missing values are the caller's responsibility.
func convertValue(v any, code handlerparams.TypeCode) (any, error) {
	switch code {
	case handlerparams.TypeCodeString:
		return toStringValueConv(v)
	case handlerparams.TypeCodeInt:
		return toIntValueConv(v)
	case handlerparams.TypeCodeLong:
		return toLongValueConv(v)
	case handlerparams.TypeCodeDouble:
		return toDoubleValueConv(v)
	case handlerparams.TypeCodeBool:
		return toBoolValueConv(v)
	case handlerparams.TypeCodeObjectID:
		return toObjectIDValueConv(v)
	case handlerparams.TypeCodeDate:
		return toDateValueConv(v)
	default:
		return nil, convertError(fmt.Sprintf("%s is not a supported target type for $convert", code.String()))
	}
}

// toOp represents the family of single-argument type conversion operators such
// as `$toString`, `$toInt`, `$toLong`, `$toDouble`, `$toBool`, `$toObjectId`
// and `$toDate`.
type toOp struct {
	name string
	arg  any
	code handlerparams.TypeCode
}

// newToOp returns a constructor for the conversion operator with the given name
// and target type code.
func newToOp(name string, code handlerparams.TypeCode) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) != 1 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes exactly 1 arguments. %d were passed in.", name, len(args)),
			)
		}

		return &toOp{name: name, arg: args[0], code: code}, nil
	}
}

// newToString returns `$toString` operator.
func newToString(args ...any) (Operator, error) {
	return newToOp("$toString", handlerparams.TypeCodeString)(args...)
}

// newToInt returns `$toInt` operator.
func newToInt(args ...any) (Operator, error) {
	return newToOp("$toInt", handlerparams.TypeCodeInt)(args...)
}

// newToLong returns `$toLong` operator.
func newToLong(args ...any) (Operator, error) {
	return newToOp("$toLong", handlerparams.TypeCodeLong)(args...)
}

// newToDouble returns `$toDouble` operator.
func newToDouble(args ...any) (Operator, error) {
	return newToOp("$toDouble", handlerparams.TypeCodeDouble)(args...)
}

// newToBool returns `$toBool` operator.
func newToBool(args ...any) (Operator, error) {
	return newToOp("$toBool", handlerparams.TypeCodeBool)(args...)
}

// newToObjectID returns `$toObjectId` operator.
func newToObjectID(args ...any) (Operator, error) {
	return newToOp("$toObjectId", handlerparams.TypeCodeObjectID)(args...)
}

// newToDate returns `$toDate` operator.
func newToDate(args ...any) (Operator, error) {
	return newToOp("$toDate", handlerparams.TypeCodeDate)(args...)
}

// Process implements Operator interface.
func (o *toOp) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	// null and missing convert to null, which also tolerates the $project dry-run.
	if isNullValue(v) {
		return types.Null, nil
	}

	return convertValue(v, o.code)
}

// convert represents `$convert` operator.
type convert struct {
	input   any
	to      any
	onError any
	onNull  any
}

// newConvert returns `$convert` operator.
//
// The specification document has the shape
// `{input: <expr>, to: <type>, onError?: <expr>, onNull?: <expr>}`.
func newConvert(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$convert",
			fmt.Sprintf("Expression $convert takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("$convert expects an object of named arguments but found: %s", handlerparams.AliasFromType(args[0])),
			"$convert",
		)
	}

	input, err := spec.Get("input")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"Missing 'input' parameter to $convert",
			"$convert",
		)
	}

	to, err := spec.Get("to")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"Missing 'to' parameter to $convert",
			"$convert",
		)
	}

	op := &convert{input: input, to: to}

	if spec.Has("onError") {
		op.onError, _ = spec.Get("onError")
	}

	if spec.Has("onNull") {
		op.onNull, _ = spec.Get("onNull")
	}

	return op, nil
}

// parseConvertTo resolves the `to` value (a type alias string or a numeric BSON
// type code) into a supported TypeCode.
func parseConvertTo(v any) (handlerparams.TypeCode, error) {
	switch v := v.(type) {
	case string:
		switch v {
		case "string":
			return handlerparams.TypeCodeString, nil
		case "int":
			return handlerparams.TypeCodeInt, nil
		case "long":
			return handlerparams.TypeCodeLong, nil
		case "double":
			return handlerparams.TypeCodeDouble, nil
		case "bool":
			return handlerparams.TypeCodeBool, nil
		case "objectId":
			return handlerparams.TypeCodeObjectID, nil
		case "date":
			return handlerparams.TypeCodeDate, nil
		default:
			return 0, convertError(fmt.Sprintf("Unknown type name alias: %s", v))
		}
	case int32:
		return convertToCodeFromNumber(int64(v))
	case int64:
		return convertToCodeFromNumber(v)
	case float64:
		return convertToCodeFromNumber(int64(v))
	default:
		return 0, convertError(fmt.Sprintf("$convert's 'to' argument must be a string or number, but is %s", handlerparams.AliasFromType(v)))
	}
}

// convertToCodeFromNumber resolves a numeric BSON type code into a supported
// TypeCode for `$convert`.
func convertToCodeFromNumber(code int64) (handlerparams.TypeCode, error) {
	switch handlerparams.TypeCode(code) {
	case handlerparams.TypeCodeString:
		return handlerparams.TypeCodeString, nil
	case handlerparams.TypeCodeInt:
		return handlerparams.TypeCodeInt, nil
	case handlerparams.TypeCodeLong:
		return handlerparams.TypeCodeLong, nil
	case handlerparams.TypeCodeDouble:
		return handlerparams.TypeCodeDouble, nil
	case handlerparams.TypeCodeBool:
		return handlerparams.TypeCodeBool, nil
	case handlerparams.TypeCodeObjectID:
		return handlerparams.TypeCodeObjectID, nil
	case handlerparams.TypeCodeDate:
		return handlerparams.TypeCodeDate, nil
	default:
		return 0, convertError(fmt.Sprintf("In $convert, numeric 'to' argument is not a supported type: %d", code))
	}
}

// Process implements Operator interface.
func (o *convert) Process(doc *types.Document) (any, error) {
	toVal, err := evaluateExpression(o.to, doc)
	if err != nil {
		return nil, err
	}

	// a null target type yields null.
	if isNullValue(toVal) {
		return types.Null, nil
	}

	code, err := parseConvertTo(toVal)
	if err != nil {
		return nil, err
	}

	input, err := evaluateExpression(o.input, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(input) {
		if o.onNull != nil {
			return evaluateExpression(o.onNull, doc)
		}

		return types.Null, nil
	}

	res, err := convertValue(input, code)
	if err != nil {
		if o.onError != nil {
			return evaluateExpression(o.onError, doc)
		}

		return nil, err
	}

	return res, nil
}

// isNumber represents `$isNumber` operator.
type isNumber struct {
	arg any
}

// newIsNumber returns `$isNumber` operator that reports whether its argument
// evaluates to a numeric value (int, long or double).
func newIsNumber(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$isNumber",
			fmt.Sprintf("Expression $isNumber takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	return &isNumber{arg: args[0]}, nil
}

// Process implements Operator interface.
func (o *isNumber) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	return numberValue(v), nil
}

// check interfaces
var (
	_ Operator = (*toOp)(nil)
	_ Operator = (*convert)(nil)
	_ Operator = (*isNumber)(nil)
)
