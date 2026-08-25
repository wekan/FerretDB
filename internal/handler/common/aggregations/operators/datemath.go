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
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// millisPerDay is the number of milliseconds in a day.
const millisPerDay = int64(24 * 60 * 60 * 1000)

// dateTruncRefYear is the reference year used to align `$dateTrunc` bins,
// matching MongoDB's 2000-01-01 reference point.
const dateTruncRefYear = 2000

// floorDiv returns the floored quotient of a/b, correctly handling negatives.
func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}

	return q
}

// localMillis returns the wall-clock time of t (in its location) expressed as
// milliseconds since the Unix epoch as if the wall clock were UTC.
func localMillis(t time.Time) int64 {
	_, offset := t.Zone()

	return t.UnixMilli() + int64(offset)*1000
}

// localMillisToTime reinterprets wall-clock milliseconds (as produced by
// localMillis) as a time.Time in loc.
func localMillisToTime(ms int64, loc *time.Location) time.Time {
	u := time.UnixMilli(ms).UTC()

	return time.Date(u.Year(), u.Month(), u.Day(), u.Hour(), u.Minute(), u.Second(), u.Nanosecond(), loc)
}

// unitMillis returns the number of milliseconds in a fixed-length unit, and
// reports whether the unit is fixed-length (day and smaller).
func unitMillis(unit string) (int64, bool) {
	switch unit {
	case "day":
		return millisPerDay, true
	case "hour":
		return 60 * 60 * 1000, true
	case "minute":
		return 60 * 1000, true
	case "second":
		return 1000, true
	case "millisecond":
		return 1, true
	default:
		return 0, false
	}
}

// asInt coerces a numeric BSON value to int64 for the given operator.
func asInt(name, field string, v any) (int64, error) {
	switch v := v.(type) {
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) ||
			v >= float64(math.MaxInt64) || v < float64(math.MinInt64) {
			return 0, dateError(name, fmt.Sprintf("%s expects an integer for %s", name, field))
		}

		return int64(v), nil
	default:
		return 0, dateError(name, fmt.Sprintf(
			"%s expects a number for %s, but got %s", name, field, handlerparams.AliasFromType(v),
		))
	}
}

// nativeInt converts an int64 only after proving that it fits the platform's
// native int width. time.Date uses native ints even though BSON numbers are
// fixed-width values.
func nativeInt(name, field string, v int64) (int, error) {
	if strconv.IntSize == 32 && (v > math.MaxInt32 || v < math.MinInt32) {
		return 0, dateError(name, fmt.Sprintf("%s value for %s is out of range", name, field))
	}

	return int(v), nil
}

// evalTimezone evaluates an optional timezone expression on a spec document,
// returning the location and whether it resolved to null.
func evalTimezone(name string, spec *types.Document, doc *types.Document) (*time.Location, bool, error) {
	if !spec.Has("timezone") {
		return time.UTC, false, nil
	}

	tz, tzNull, err := evalString(name, "timezone", must.NotFail(spec.Get("timezone")), doc)
	if err != nil {
		return nil, false, err
	}

	if tzNull {
		return time.UTC, true, nil
	}

	loc, err := loadLocation(tz)
	if err != nil {
		return nil, false, dateError(name, err.Error())
	}

	return loc, false, nil
}

// dateToParts represents `$dateToParts` operator.
type dateToParts struct {
	date     any
	timezone any
	iso8601  any
}

// newDateToParts returns `$dateToParts` operator.
//
// The specification document has the shape
// `{date: <expr>, timezone?: <expr>, iso8601?: <expr>}`.
func newDateToParts(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$dateToParts",
			fmt.Sprintf("Expression $dateToParts takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, dateParseError("$dateToParts", "$dateToParts only supports an object as its argument")
	}

	date, err := spec.Get("date")
	if err != nil {
		return nil, dateParseError("$dateToParts", "Missing 'date' parameter to $dateToParts")
	}

	op := &dateToParts{date: date}

	if spec.Has("timezone") {
		op.timezone = must.NotFail(spec.Get("timezone"))
	}

	if spec.Has("iso8601") {
		op.iso8601 = must.NotFail(spec.Get("iso8601"))
	}

	return op, nil
}

// Process implements Operator interface.
func (o *dateToParts) Process(doc *types.Document) (any, error) {
	loc := time.UTC

	if o.timezone != nil {
		tz, tzNull, err := evalString("$dateToParts", "timezone", o.timezone, doc)
		if err != nil {
			return nil, err
		}

		if tzNull {
			return types.Null, nil
		}

		if loc, err = loadLocation(tz); err != nil {
			return nil, dateError("$dateToParts", err.Error())
		}
	}

	iso := false

	if o.iso8601 != nil {
		v, err := evaluateExpression(o.iso8601, doc)
		if err != nil {
			return nil, err
		}

		if isNullValue(v) {
			return types.Null, nil
		}

		b, ok := v.(bool)
		if !ok {
			return nil, dateError("$dateToParts", "$dateToParts requires 'iso8601' to be a boolean")
		}

		iso = b
	}

	dv, err := evaluateExpression(o.date, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(dv) {
		return types.Null, nil
	}

	t, err := asDate("$dateToParts", dv)
	if err != nil {
		return nil, err
	}

	t = t.In(loc)

	if iso {
		isoYear, isoWeek := t.ISOWeek()

		return must.NotFail(types.NewDocument(
			"isoWeekYear", int32(isoYear),
			"isoWeek", int32(isoWeek),
			"isoDayOfWeek", int32(isoDayOfWeek(t)),
			"hour", int32(t.Hour()),
			"minute", int32(t.Minute()),
			"second", int32(t.Second()),
			"millisecond", int32(t.Nanosecond()/1e6),
		)), nil
	}

	return must.NotFail(types.NewDocument(
		"year", int32(t.Year()),
		"month", int32(t.Month()),
		"day", int32(t.Day()),
		"hour", int32(t.Hour()),
		"minute", int32(t.Minute()),
		"second", int32(t.Second()),
		"millisecond", int32(t.Nanosecond()/1e6),
	)), nil
}

// dateFromParts represents `$dateFromParts` operator.
type dateFromParts struct {
	spec *types.Document
}

// newDateFromParts returns `$dateFromParts` operator.
//
// The specification document accepts either calendar parts
// `{year, month?, day?, hour?, minute?, second?, millisecond?, timezone?}` or
// ISO week-date parts `{isoWeekYear, isoWeek?, isoDayOfWeek?, ...}`.
func newDateFromParts(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$dateFromParts",
			fmt.Sprintf("Expression $dateFromParts takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, dateParseError("$dateFromParts", "$dateFromParts only supports an object as its argument")
	}

	return &dateFromParts{spec: spec}, nil
}

// partValue evaluates the numeric part with the given key, returning def when
// absent and reporting whether it resolved to null.
func (o *dateFromParts) partValue(key string, def int64, doc *types.Document) (int64, bool, error) {
	if !o.spec.Has(key) {
		return def, false, nil
	}

	v, err := evaluateExpression(must.NotFail(o.spec.Get(key)), doc)
	if err != nil {
		return 0, false, err
	}

	if isNullValue(v) {
		return 0, true, nil
	}

	n, err := asInt("$dateFromParts", key, v)
	if err != nil {
		return 0, false, err
	}

	return n, false, nil
}

// Process implements Operator interface.
func (o *dateFromParts) Process(doc *types.Document) (any, error) {
	loc, tzNull, err := evalTimezone("$dateFromParts", o.spec, doc)
	if err != nil {
		return nil, err
	}

	if tzNull {
		return types.Null, nil
	}

	parts := []struct {
		key string
		def int64
	}{}

	iso := o.spec.Has("isoWeekYear")

	if iso {
		parts = []struct {
			key string
			def int64
		}{
			{"isoWeekYear", 1970}, {"isoWeek", 1}, {"isoDayOfWeek", 1},
			{"hour", 0}, {"minute", 0}, {"second", 0}, {"millisecond", 0},
		}
	} else {
		parts = []struct {
			key string
			def int64
		}{
			{"year", 1970}, {"month", 1}, {"day", 1},
			{"hour", 0}, {"minute", 0}, {"second", 0}, {"millisecond", 0},
		}
	}

	vals := make(map[string]int, len(parts))

	for _, p := range parts {
		v, null, err := o.partValue(p.key, p.def, doc)
		if err != nil {
			return nil, err
		}

		if null {
			return types.Null, nil
		}

		vals[p.key], err = nativeInt("$dateFromParts", p.key, v)
		if err != nil {
			return nil, err
		}
	}

	var t time.Time

	if iso {
		t = isoPartsToTime(
			vals["isoWeekYear"], vals["isoWeek"], vals["isoDayOfWeek"],
			vals["hour"], vals["minute"], vals["second"], vals["millisecond"], loc,
		)
	} else {
		t = time.Date(
			vals["year"], time.Month(vals["month"]), vals["day"],
			vals["hour"], vals["minute"], vals["second"],
			vals["millisecond"]*int(time.Millisecond), loc,
		)
	}

	return t.UTC(), nil
}

// isoPartsToTime builds a time.Time from ISO week-date parts in loc.
func isoPartsToTime(isoYear, isoWeek, isoDay, hour, minute, second, ms int, loc *time.Location) time.Time {
	// Jan 4 is always in ISO week 1.
	jan4 := time.Date(isoYear, time.January, 4, 0, 0, 0, 0, loc)

	wd := int(jan4.Weekday())
	if wd == 0 {
		wd = 7
	}

	week1Monday := jan4.AddDate(0, 0, -(wd - 1))
	target := week1Monday.AddDate(0, 0, (isoWeek-1)*7+(isoDay-1))

	return time.Date(
		target.Year(), target.Month(), target.Day(),
		hour, minute, second, ms*int(time.Millisecond), loc,
	)
}

// dateArith represents the `$dateAdd` and `$dateSubtract` operators.
type dateArith struct {
	name string
	spec *types.Document
	sign int64
}

// newDateAdd returns `$dateAdd` operator.
func newDateAdd(args ...any) (Operator, error) {
	return newDateArith("$dateAdd", 1)(args...)
}

// newDateSubtract returns `$dateSubtract` operator.
func newDateSubtract(args ...any) (Operator, error) {
	return newDateArith("$dateSubtract", -1)(args...)
}

// newDateArith returns a constructor for a date add/subtract operator.
//
// The specification document has the shape
// `{startDate: <expr>, unit: <expr>, amount: <expr>, timezone?: <expr>}`.
func newDateArith(name string, sign int64) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) != 1 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes exactly 1 arguments. %d were passed in.", name, len(args)),
			)
		}

		spec, ok := args[0].(*types.Document)
		if !ok {
			return nil, dateParseError(name, fmt.Sprintf("%s only supports an object as its argument", name))
		}

		for _, k := range []string{"startDate", "unit", "amount"} {
			if !spec.Has(k) {
				return nil, dateParseError(name, fmt.Sprintf("Missing '%s' parameter to %s", k, name))
			}
		}

		return &dateArith{name: name, spec: spec, sign: sign}, nil
	}
}

// Process implements Operator interface.
func (o *dateArith) Process(doc *types.Document) (any, error) {
	loc, tzNull, err := evalTimezone(o.name, o.spec, doc)
	if err != nil {
		return nil, err
	}

	if tzNull {
		return types.Null, nil
	}

	startVal, err := evaluateExpression(must.NotFail(o.spec.Get("startDate")), doc)
	if err != nil {
		return nil, err
	}

	unitVal, unitNull, err := evalString(o.name, "unit", must.NotFail(o.spec.Get("unit")), doc)
	if err != nil {
		return nil, err
	}

	amountVal, err := evaluateExpression(must.NotFail(o.spec.Get("amount")), doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(startVal) || isNullValue(amountVal) || unitNull {
		return types.Null, nil
	}

	start, err := asDate(o.name, startVal)
	if err != nil {
		return nil, err
	}

	amount, err := asInt(o.name, "amount", amountVal)
	if err != nil {
		return nil, err
	}

	res, err := addTimeUnit(o.name, start.In(loc), unitVal, o.sign*amount)
	if err != nil {
		return nil, err
	}

	return res.UTC(), nil
}

// addTimeUnit adds amount of the given unit to t using calendar-aware
// arithmetic for month-based units.
func addTimeUnit(name string, t time.Time, unit string, amount int64) (time.Time, error) {
	a := int(amount)

	switch unit {
	case "year":
		return t.AddDate(a, 0, 0), nil
	case "quarter":
		return t.AddDate(0, 3*a, 0), nil
	case "month":
		return t.AddDate(0, a, 0), nil
	case "week":
		return t.AddDate(0, 0, 7*a), nil
	case "day":
		return t.AddDate(0, 0, a), nil
	case "hour":
		return t.Add(time.Duration(amount) * time.Hour), nil
	case "minute":
		return t.Add(time.Duration(amount) * time.Minute), nil
	case "second":
		return t.Add(time.Duration(amount) * time.Second), nil
	case "millisecond":
		return t.Add(time.Duration(amount) * time.Millisecond), nil
	default:
		return time.Time{}, invalidUnitError(name, unit)
	}
}

// invalidUnitError returns a MongoDB-style error for an unknown time unit.
func invalidUnitError(name, unit string) error {
	return handlererrors.NewCommandErrorMsgWithArgument(
		handlererrors.ErrBadValue,
		fmt.Sprintf("unknown time unit value: %s", unit),
		name,
	)
}

// dateDiff represents `$dateDiff` operator.
type dateDiff struct {
	spec *types.Document
}

// newDateDiff returns `$dateDiff` operator.
//
// The specification document has the shape
// `{startDate: <expr>, endDate: <expr>, unit: <expr>, timezone?: <expr>}`.
func newDateDiff(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$dateDiff",
			fmt.Sprintf("Expression $dateDiff takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, dateParseError("$dateDiff", "$dateDiff only supports an object as its argument")
	}

	for _, k := range []string{"startDate", "endDate", "unit"} {
		if !spec.Has(k) {
			return nil, dateParseError("$dateDiff", fmt.Sprintf("Missing '%s' parameter to $dateDiff", k))
		}
	}

	return &dateDiff{spec: spec}, nil
}

// Process implements Operator interface.
func (o *dateDiff) Process(doc *types.Document) (any, error) {
	loc, tzNull, err := evalTimezone("$dateDiff", o.spec, doc)
	if err != nil {
		return nil, err
	}

	if tzNull {
		return types.Null, nil
	}

	startVal, err := evaluateExpression(must.NotFail(o.spec.Get("startDate")), doc)
	if err != nil {
		return nil, err
	}

	endVal, err := evaluateExpression(must.NotFail(o.spec.Get("endDate")), doc)
	if err != nil {
		return nil, err
	}

	unit, unitNull, err := evalString("$dateDiff", "unit", must.NotFail(o.spec.Get("unit")), doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(startVal) || isNullValue(endVal) || unitNull {
		return types.Null, nil
	}

	start, err := asDate("$dateDiff", startVal)
	if err != nil {
		return nil, err
	}

	end, err := asDate("$dateDiff", endVal)
	if err != nil {
		return nil, err
	}

	return diffTimeUnit("$dateDiff", start.In(loc), end.In(loc), unit)
}

// diffTimeUnit returns the number of unit boundaries crossed from start to end.
func diffTimeUnit(name string, start, end time.Time, unit string) (int64, error) {
	switch unit {
	case "year":
		return int64(end.Year() - start.Year()), nil
	case "quarter":
		sq := int64(start.Year())*4 + int64((int(start.Month())-1)/3)
		eq := int64(end.Year())*4 + int64((int(end.Month())-1)/3)

		return eq - sq, nil
	case "month":
		sm := int64(start.Year())*12 + int64(int(start.Month())-1)
		em := int64(end.Year())*12 + int64(int(end.Month())-1)

		return em - sm, nil
	case "week":
		return weekIndex(end) - weekIndex(start), nil
	default:
		ms, ok := unitMillis(unit)
		if !ok {
			return 0, invalidUnitError(name, unit)
		}

		return floorDiv(localMillis(end), ms) - floorDiv(localMillis(start), ms), nil
	}
}

// weekIndex returns the Sunday-aligned week number of t since the Unix epoch.
func weekIndex(t time.Time) int64 {
	// Epoch day 0 (1970-01-01) is a Thursday; +4 aligns week boundaries to Sunday.
	return floorDiv(floorDiv(localMillis(t), millisPerDay)+4, 7)
}

// dateTrunc represents `$dateTrunc` operator.
type dateTrunc struct {
	spec *types.Document
}

// newDateTrunc returns `$dateTrunc` operator.
//
// The specification document has the shape
// `{date: <expr>, unit: <expr>, binSize?: <expr>, timezone?: <expr>, startOfWeek?: <expr>}`.
func newDateTrunc(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$dateTrunc",
			fmt.Sprintf("Expression $dateTrunc takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, dateParseError("$dateTrunc", "$dateTrunc only supports an object as its argument")
	}

	for _, k := range []string{"date", "unit"} {
		if !spec.Has(k) {
			return nil, dateParseError("$dateTrunc", fmt.Sprintf("Missing '%s' parameter to $dateTrunc", k))
		}
	}

	return &dateTrunc{spec: spec}, nil
}

// Process implements Operator interface.
func (o *dateTrunc) Process(doc *types.Document) (any, error) {
	loc, tzNull, err := evalTimezone("$dateTrunc", o.spec, doc)
	if err != nil {
		return nil, err
	}

	if tzNull {
		return types.Null, nil
	}

	unit, unitNull, err := evalString("$dateTrunc", "unit", must.NotFail(o.spec.Get("unit")), doc)
	if err != nil {
		return nil, err
	}

	binSize := int64(1)

	if o.spec.Has("binSize") {
		v, err := evaluateExpression(must.NotFail(o.spec.Get("binSize")), doc)
		if err != nil {
			return nil, err
		}

		if isNullValue(v) {
			return types.Null, nil
		}

		if binSize, err = asInt("$dateTrunc", "binSize", v); err != nil {
			return nil, err
		}

		if binSize < 1 {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrBadValue,
				"$dateTrunc requires 'binSize' to be greater than 0",
				"$dateTrunc",
			)
		}
	}

	startOfWeek := "sunday"

	if o.spec.Has("startOfWeek") {
		s, sNull, err := evalString("$dateTrunc", "startOfWeek", must.NotFail(o.spec.Get("startOfWeek")), doc)
		if err != nil {
			return nil, err
		}

		if sNull {
			return types.Null, nil
		}

		startOfWeek = s
	}

	dv, err := evaluateExpression(must.NotFail(o.spec.Get("date")), doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(dv) || unitNull {
		return types.Null, nil
	}

	t, err := asDate("$dateTrunc", dv)
	if err != nil {
		return nil, err
	}

	res, err := truncateTimeUnit(t.In(loc), unit, binSize, startOfWeek, loc)
	if err != nil {
		return nil, err
	}

	return res.UTC(), nil
}

// truncateTimeUnit truncates t down to the nearest bin boundary of the given
// unit and binSize, aligned to MongoDB's 2000-01-01 reference point.
func truncateTimeUnit(t time.Time, unit string, binSize int64, startOfWeek string, loc *time.Location) (time.Time, error) {
	switch unit {
	case "year", "quarter", "month":
		var binMonths int64

		switch unit {
		case "year":
			binMonths = 12 * binSize
		case "quarter":
			binMonths = 3 * binSize
		default:
			binMonths = binSize
		}

		monthsSinceRef := int64((t.Year()-dateTruncRefYear)*12 + (int(t.Month()) - 1))
		b := floorDiv(monthsSinceRef, binMonths) * binMonths
		y := dateTruncRefYear + int(floorDiv(b, 12))
		m := int(b - floorDiv(b, 12)*12)

		return time.Date(y, time.Month(1+m), 1, 0, 0, 0, 0, loc), nil

	case "week":
		startDow, err := parseStartOfWeek(startOfWeek)
		if err != nil {
			return time.Time{}, err
		}

		ref := time.Date(dateTruncRefYear, time.January, 1, 0, 0, 0, 0, loc)
		delta := (int(ref.Weekday()) - startDow + 7) % 7
		ref = ref.AddDate(0, 0, -delta)

		binMs := binSize * 7 * millisPerDay
		elapsed := localMillis(t) - localMillis(ref)
		binned := floorDiv(elapsed, binMs) * binMs

		return localMillisToTime(localMillis(ref)+binned, loc), nil

	default:
		ms, ok := unitMillis(unit)
		if !ok {
			return time.Time{}, invalidUnitError("$dateTrunc", unit)
		}

		ref := time.Date(dateTruncRefYear, time.January, 1, 0, 0, 0, 0, loc)
		binMs := binSize * ms
		elapsed := localMillis(t) - localMillis(ref)
		binned := floorDiv(elapsed, binMs) * binMs

		return localMillisToTime(localMillis(ref)+binned, loc), nil
	}
}

// parseStartOfWeek maps a MongoDB startOfWeek name to a weekday index
// (Sunday = 0 ... Saturday = 6).
func parseStartOfWeek(s string) (int, error) {
	switch strings.ToLower(s) {
	case "sunday", "sun":
		return 0, nil
	case "monday", "mon":
		return 1, nil
	case "tuesday", "tue":
		return 2, nil
	case "wednesday", "wed":
		return 3, nil
	case "thursday", "thu":
		return 4, nil
	case "friday", "fri":
		return 5, nil
	case "saturday", "sat":
		return 6, nil
	default:
		return 0, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrBadValue,
			fmt.Sprintf("unknown startOfWeek value: %s", s),
			"$dateTrunc",
		)
	}
}

// check interfaces
var (
	_ Operator = (*dateToParts)(nil)
	_ Operator = (*dateFromParts)(nil)
	_ Operator = (*dateArith)(nil)
	_ Operator = (*dateDiff)(nil)
	_ Operator = (*dateTrunc)(nil)
)
