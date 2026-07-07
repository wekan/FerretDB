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
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// defaultDateFormat is the default `$dateToString` format specifier string.
const defaultDateFormat = "%Y-%m-%dT%H:%M:%S.%LZ"

// dateError returns a MongoDB-style type mismatch error for the given operator.
func dateError(name, msg string) error {
	return handlererrors.NewCommandErrorMsgWithArgument(
		handlererrors.ErrTypeMismatch,
		msg,
		name,
	)
}

// dateParseError returns a MongoDB-style parse failure error for the given operator.
func dateParseError(name, msg string) error {
	return handlererrors.NewCommandErrorMsgWithArgument(
		handlererrors.ErrFailedToParse,
		msg,
		name,
	)
}

// loadLocation resolves a MongoDB timezone identifier to a time.Location.
//
// It accepts IANA names (resolved via time.LoadLocation) and fixed UTC offsets
// such as "+05:30", "-0800" or "+05" (parsed manually to a time.FixedZone).
func loadLocation(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}

	if loc, ok := parseFixedOffset(name); ok {
		return loc, nil
	}

	if loc, err := time.LoadLocation(name); err == nil {
		return loc, nil
	}

	return nil, fmt.Errorf("unrecognized time zone identifier: %q", name)
}

// parseFixedOffset parses a fixed UTC offset such as "+05:30", "-0800", "+05"
// into a time.Location, returning false when the string is not an offset.
func parseFixedOffset(s string) (*time.Location, bool) {
	if len(s) < 2 || (s[0] != '+' && s[0] != '-') {
		return nil, false
	}

	sign := 1
	if s[0] == '-' {
		sign = -1
	}

	body := strings.Replace(s[1:], ":", "", 1)

	var hh, mm int

	var err error

	switch len(body) {
	case 2:
		hh, err = strconv.Atoi(body)
	case 4:
		hh, err = strconv.Atoi(body[:2])
		if err == nil {
			mm, err = strconv.Atoi(body[2:])
		}
	default:
		return nil, false
	}

	if err != nil || hh > 23 || mm > 59 {
		return nil, false
	}

	return time.FixedZone(s, sign*(hh*3600+mm*60)), true
}

// asDate coerces an already-evaluated BSON value to a time.Time, returning a
// MongoDB-style error for the given operator when the value is not a date.
func asDate(name string, v any) (time.Time, error) {
	if t, ok := v.(time.Time); ok {
		return t, nil
	}

	return time.Time{}, dateError(name, fmt.Sprintf(
		"%s requires a date, but got %s", name, handlerparams.AliasFromType(v),
	))
}

// evalString evaluates an expression that is expected to resolve to a string.
// It reports whether the value resolved to null.
func evalString(name, field string, expr any, doc *types.Document) (string, bool, error) {
	v, err := evaluateExpression(expr, doc)
	if err != nil {
		return "", false, err
	}

	if isNullValue(v) {
		return "", true, nil
	}

	s, ok := v.(string)
	if !ok {
		return "", false, dateError(name, fmt.Sprintf(
			"%s expects a string for %s, but got %s", name, field, handlerparams.AliasFromType(v),
		))
	}

	return s, false, nil
}

// resolveDate evaluates a date extraction argument that is either a date
// expression or a document of the form `{date: <expr>, timezone: <expr>}`.
//
// It returns the date shifted into the requested timezone. When the date or
// timezone resolves to null it reports isNull true so the caller can tolerate
// the `$project` dry-run and MongoDB null semantics.
func resolveDate(name string, arg any, doc *types.Document) (t time.Time, isNull bool, err error) {
	var dateExpr, tzExpr any

	if d, ok := arg.(*types.Document); ok && !IsOperator(d) && d.Has("date") {
		dateExpr = must.NotFail(d.Get("date"))

		if d.Has("timezone") {
			tzExpr = must.NotFail(d.Get("timezone"))
		}
	} else {
		dateExpr = arg
	}

	dv, err := evaluateExpression(dateExpr, doc)
	if err != nil {
		return t, false, err
	}

	if isNullValue(dv) {
		return t, true, nil
	}

	if t, err = asDate(name, dv); err != nil {
		return t, false, err
	}

	loc := time.UTC

	if tzExpr != nil {
		tz, tzNull, tzErr := evalString(name, "timezone", tzExpr, doc)
		if tzErr != nil {
			return t, false, tzErr
		}

		if tzNull {
			return t, true, nil
		}

		if loc, err = loadLocation(tz); err != nil {
			return t, false, dateError(name, err.Error())
		}
	}

	return t.In(loc), false, nil
}

// weekNumber returns the MongoDB `$week` number: weeks start on Sunday and week
// 1 begins with the first Sunday of the year; earlier days are in week 0.
func weekNumber(t time.Time) int {
	return (t.YearDay() + 6 - int(t.Weekday())) / 7
}

// isoDayOfWeek returns the ISO 8601 day of week (Monday = 1 ... Sunday = 7).
func isoDayOfWeek(t time.Time) int {
	if wd := int(t.Weekday()); wd != 0 {
		return wd
	}

	return 7
}

// dateExtract represents the family of single-argument date extraction
// operators such as `$year`, `$month`, `$hour` and `$isoWeek`.
type dateExtract struct {
	name    string
	arg     any
	extract func(time.Time) int
}

// newDateExtract returns a constructor for the extraction operator with the
// given name and extraction function.
func newDateExtract(name string, extract func(time.Time) int) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) != 1 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes exactly 1 arguments. %d were passed in.", name, len(args)),
			)
		}

		return &dateExtract{name: name, arg: args[0], extract: extract}, nil
	}
}

// Process implements Operator interface.
func (o *dateExtract) Process(doc *types.Document) (any, error) {
	t, isNull, err := resolveDate(o.name, o.arg, doc)
	if err != nil {
		return nil, err
	}

	if isNull {
		return types.Null, nil
	}

	return int32(o.extract(t)), nil
}

// newYear returns `$year` operator.
func newYear(args ...any) (Operator, error) {
	return newDateExtract("$year", func(t time.Time) int { return t.Year() })(args...)
}

// newMonth returns `$month` operator.
func newMonth(args ...any) (Operator, error) {
	return newDateExtract("$month", func(t time.Time) int { return int(t.Month()) })(args...)
}

// newDayOfMonth returns `$dayOfMonth` operator.
func newDayOfMonth(args ...any) (Operator, error) {
	return newDateExtract("$dayOfMonth", func(t time.Time) int { return t.Day() })(args...)
}

// newHour returns `$hour` operator.
func newHour(args ...any) (Operator, error) {
	return newDateExtract("$hour", func(t time.Time) int { return t.Hour() })(args...)
}

// newMinute returns `$minute` operator.
func newMinute(args ...any) (Operator, error) {
	return newDateExtract("$minute", func(t time.Time) int { return t.Minute() })(args...)
}

// newSecond returns `$second` operator.
func newSecond(args ...any) (Operator, error) {
	return newDateExtract("$second", func(t time.Time) int { return t.Second() })(args...)
}

// newMillisecond returns `$millisecond` operator.
func newMillisecond(args ...any) (Operator, error) {
	return newDateExtract("$millisecond", func(t time.Time) int { return t.Nanosecond() / 1e6 })(args...)
}

// newDayOfWeek returns `$dayOfWeek` operator (Sunday = 1 ... Saturday = 7).
func newDayOfWeek(args ...any) (Operator, error) {
	return newDateExtract("$dayOfWeek", func(t time.Time) int { return int(t.Weekday()) + 1 })(args...)
}

// newDayOfYear returns `$dayOfYear` operator.
func newDayOfYear(args ...any) (Operator, error) {
	return newDateExtract("$dayOfYear", func(t time.Time) int { return t.YearDay() })(args...)
}

// newWeek returns `$week` operator.
func newWeek(args ...any) (Operator, error) {
	return newDateExtract("$week", weekNumber)(args...)
}

// newIsoDayOfWeek returns `$isoDayOfWeek` operator.
func newIsoDayOfWeek(args ...any) (Operator, error) {
	return newDateExtract("$isoDayOfWeek", isoDayOfWeek)(args...)
}

// newIsoWeek returns `$isoWeek` operator.
func newIsoWeek(args ...any) (Operator, error) {
	return newDateExtract("$isoWeek", func(t time.Time) int {
		_, w := t.ISOWeek()
		return w
	})(args...)
}

// newIsoWeekYear returns `$isoWeekYear` operator.
func newIsoWeekYear(args ...any) (Operator, error) {
	return newDateExtract("$isoWeekYear", func(t time.Time) int {
		y, _ := t.ISOWeek()
		return y
	})(args...)
}

// dateToString represents `$dateToString` operator.
type dateToString struct {
	date     any
	format   any
	timezone any
	onNull   any
	hasNull  bool
}

// newDateToString returns `$dateToString` operator.
//
// The specification document has the shape
// `{date: <expr>, format?: <expr>, timezone?: <expr>, onNull?: <expr>}`.
func newDateToString(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$dateToString",
			fmt.Sprintf("Expression $dateToString takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, dateParseError("$dateToString", "$dateToString only supports an object as its argument")
	}

	date, err := spec.Get("date")
	if err != nil {
		return nil, dateParseError("$dateToString", "Missing 'date' parameter to $dateToString")
	}

	op := &dateToString{date: date}

	if spec.Has("format") {
		op.format = must.NotFail(spec.Get("format"))
	}

	if spec.Has("timezone") {
		op.timezone = must.NotFail(spec.Get("timezone"))
	}

	if spec.Has("onNull") {
		op.onNull = must.NotFail(spec.Get("onNull"))
		op.hasNull = true
	}

	return op, nil
}

// Process implements Operator interface.
func (o *dateToString) Process(doc *types.Document) (any, error) {
	format := defaultDateFormat

	if o.format != nil {
		f, fNull, err := evalString("$dateToString", "format", o.format, doc)
		if err != nil {
			return nil, err
		}

		if fNull {
			return types.Null, nil
		}

		format = f
	}

	loc := time.UTC

	if o.timezone != nil {
		tz, tzNull, err := evalString("$dateToString", "timezone", o.timezone, doc)
		if err != nil {
			return nil, err
		}

		if tzNull {
			return o.nullResult(doc)
		}

		if loc, err = loadLocation(tz); err != nil {
			return nil, dateError("$dateToString", err.Error())
		}
	}

	dv, err := evaluateExpression(o.date, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(dv) {
		return o.nullResult(doc)
	}

	t, err := asDate("$dateToString", dv)
	if err != nil {
		return nil, err
	}

	return formatMongoDate(t.In(loc), format), nil
}

// nullResult returns the configured onNull value, or Null when none is set.
func (o *dateToString) nullResult(doc *types.Document) (any, error) {
	if o.hasNull {
		return evaluateExpression(o.onNull, doc)
	}

	return types.Null, nil
}

// formatMongoDate formats t according to the MongoDB `$dateToString` format
// specifiers.
func formatMongoDate(t time.Time, format string) string {
	_, offsetSec := t.Zone()

	sign := "+"
	if offsetSec < 0 {
		sign = "-"
		offsetSec = -offsetSec
	}

	offHH := offsetSec / 3600
	offMM := (offsetSec % 3600) / 60

	isoYear, isoWeek := t.ISOWeek()

	var b strings.Builder

	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}

		i++

		switch format[i] {
		case 'Y':
			fmt.Fprintf(&b, "%04d", t.Year())
		case 'm':
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case 'd':
			fmt.Fprintf(&b, "%02d", t.Day())
		case 'H':
			fmt.Fprintf(&b, "%02d", t.Hour())
		case 'M':
			fmt.Fprintf(&b, "%02d", t.Minute())
		case 'S':
			fmt.Fprintf(&b, "%02d", t.Second())
		case 'L':
			fmt.Fprintf(&b, "%03d", t.Nanosecond()/1e6)
		case 'j':
			fmt.Fprintf(&b, "%03d", t.YearDay())
		case 'w':
			fmt.Fprintf(&b, "%d", int(t.Weekday())+1)
		case 'U':
			fmt.Fprintf(&b, "%02d", weekNumber(t))
		case 'G':
			fmt.Fprintf(&b, "%04d", isoYear)
		case 'V':
			fmt.Fprintf(&b, "%02d", isoWeek)
		case 'u':
			fmt.Fprintf(&b, "%d", isoDayOfWeek(t))
		case 'z':
			fmt.Fprintf(&b, "%s%02d%02d", sign, offHH, offMM)
		case 'Z':
			fmt.Fprintf(&b, "%s%d", sign, offHH*60+offMM)
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(format[i])
		}
	}

	return b.String()
}

// dateFromString represents `$dateFromString` operator.
type dateFromString struct {
	dateString any
	format     any
	timezone   any
	onError    any
	onNull     any
	hasError   bool
	hasNull    bool
}

// newDateFromString returns `$dateFromString` operator.
//
// The specification document has the shape
// `{dateString: <expr>, format?: <expr>, timezone?: <expr>, onError?: <expr>, onNull?: <expr>}`.
func newDateFromString(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$dateFromString",
			fmt.Sprintf("Expression $dateFromString takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, dateParseError("$dateFromString", "$dateFromString only supports an object as an argument")
	}

	dateString, err := spec.Get("dateString")
	if err != nil {
		return nil, dateParseError("$dateFromString", "Missing 'dateString' parameter to $dateFromString")
	}

	op := &dateFromString{dateString: dateString}

	if spec.Has("format") {
		op.format = must.NotFail(spec.Get("format"))
	}

	if spec.Has("timezone") {
		op.timezone = must.NotFail(spec.Get("timezone"))
	}

	if spec.Has("onError") {
		op.onError = must.NotFail(spec.Get("onError"))
		op.hasError = true
	}

	if spec.Has("onNull") {
		op.onNull = must.NotFail(spec.Get("onNull"))
		op.hasNull = true
	}

	return op, nil
}

// Process implements Operator interface.
func (o *dateFromString) Process(doc *types.Document) (any, error) {
	sv, err := evaluateExpression(o.dateString, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(sv) {
		if o.hasNull {
			return evaluateExpression(o.onNull, doc)
		}

		return types.Null, nil
	}

	s, ok := sv.(string)
	if !ok {
		return o.errorResult(doc, dateError("$dateFromString", fmt.Sprintf(
			"$dateFromString requires that 'dateString' be a string, found: %s", handlerparams.AliasFromType(sv),
		)))
	}

	loc := time.UTC

	if o.timezone != nil {
		tz, tzNull, tzErr := evalString("$dateFromString", "timezone", o.timezone, doc)
		if tzErr != nil {
			return nil, tzErr
		}

		if tzNull {
			return types.Null, nil
		}

		if loc, err = loadLocation(tz); err != nil {
			return nil, dateError("$dateFromString", tzErr.Error())
		}
	}

	var format string

	if o.format != nil {
		f, fNull, fErr := evalString("$dateFromString", "format", o.format, doc)
		if fErr != nil {
			return nil, fErr
		}

		if fNull {
			return types.Null, nil
		}

		format = f
	}

	t, err := parseDateString(s, format, loc)
	if err != nil {
		return o.errorResult(doc, dateParseError("$dateFromString", err.Error()))
	}

	return t.UTC(), nil
}

// errorResult returns the configured onError value, or the given error.
func (o *dateFromString) errorResult(doc *types.Document, err error) (any, error) {
	if o.hasError {
		return evaluateExpression(o.onError, doc)
	}

	return nil, err
}

// mongoFormatToGoLayout converts a MongoDB `$dateFromString` format string into
// a Go reference layout for the supported specifiers.
func mongoFormatToGoLayout(format string) (string, error) {
	var b strings.Builder

	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}

		i++

		switch format[i] {
		case 'Y':
			b.WriteString("2006")
		case 'm':
			b.WriteString("01")
		case 'd':
			b.WriteString("02")
		case 'H':
			b.WriteString("15")
		case 'M':
			b.WriteString("04")
		case 'S':
			b.WriteString("05")
		case 'L':
			b.WriteString("000")
		case 'z':
			b.WriteString("-0700")
		case 'Z':
			b.WriteString("-07:00")
		case '%':
			b.WriteByte('%')
		default:
			return "", fmt.Errorf("unsupported format character '%%%c' in $dateFromString", format[i])
		}
	}

	return b.String(), nil
}

// parseDateString parses s into a time.Time using the given MongoDB format (or
// a list of common layouts when format is empty), interpreting timezone-less
// values in loc.
func parseDateString(s, format string, loc *time.Location) (time.Time, error) {
	if format != "" {
		layout, err := mongoFormatToGoLayout(format)
		if err != nil {
			return time.Time{}, err
		}

		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}

		return time.Time{}, fmt.Errorf("error parsing date string '%s'; does not match format '%s'", s, format)
	}

	for _, layout := range dateParseLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("error parsing date string '%s'; unsupported format", s)
}

// check interfaces
var (
	_ Operator = (*dateExtract)(nil)
	_ Operator = (*dateToString)(nil)
	_ Operator = (*dateFromString)(nil)
)
