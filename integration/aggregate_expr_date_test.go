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

package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestAggregateExprDate covers the date aggregation expression operators added
// on top of FerretDB v1.24.2: extraction operators ($year, $month, $dayOfMonth,
// $hour, $minute, $second, $millisecond, $dayOfWeek, $dayOfYear, $week,
// $isoDayOfWeek, $isoWeek, $isoWeekYear), $dateToString, $dateFromString,
// $dateToParts, $dateFromParts, $dateAdd, $dateSubtract, $dateDiff and
// $dateTrunc.
func TestAggregateExprDate(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	// A fixed instant: 2024-03-09 15:04:05.678 UTC (Saturday).
	date := time.Date(2024, time.March, 9, 15, 4, 5, 678*int(time.Millisecond), time.UTC)
	// A fixed end instant used for $dateDiff: 2024-03-14 10:00:00 UTC.
	endDate := time.Date(2024, time.March, 14, 10, 0, 0, 0, time.UTC)

	doc := bson.D{
		{"_id", "doc1"},
		{"dateField", date},
		{"endField", endDate},
		{"strField", "2024-03-09T15:04:05.678Z"},
		{"notDate", "hello"},
		{"nullField", nil},
	}

	_, err := collection.InsertOne(ctx, doc)
	require.NoError(t, err)

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			expr any    // computed expression for field "r"
			res  bson.D // expected single result document
		}{
			"Year": {
				expr: bson.D{{"$year", "$dateField"}},
				res:  bson.D{{"r", int32(2024)}},
			},
			"Month": {
				expr: bson.D{{"$month", "$dateField"}},
				res:  bson.D{{"r", int32(3)}},
			},
			"DayOfMonth": {
				expr: bson.D{{"$dayOfMonth", "$dateField"}},
				res:  bson.D{{"r", int32(9)}},
			},
			"Hour": {
				expr: bson.D{{"$hour", "$dateField"}},
				res:  bson.D{{"r", int32(15)}},
			},
			"Minute": {
				expr: bson.D{{"$minute", "$dateField"}},
				res:  bson.D{{"r", int32(4)}},
			},
			"Second": {
				expr: bson.D{{"$second", "$dateField"}},
				res:  bson.D{{"r", int32(5)}},
			},
			"Millisecond": {
				expr: bson.D{{"$millisecond", "$dateField"}},
				res:  bson.D{{"r", int32(678)}},
			},
			"DayOfWeek": {
				// 2024-03-09 is a Saturday: 7 in MongoDB (Sunday = 1).
				expr: bson.D{{"$dayOfWeek", "$dateField"}},
				res:  bson.D{{"r", int32(7)}},
			},
			"DayOfYear": {
				expr: bson.D{{"$dayOfYear", "$dateField"}},
				res:  bson.D{{"r", int32(69)}},
			},
			"Week": {
				// Sunday-based week: week 1 begins the first Sunday (Jan 7 2024),
				// so 2024-03-09 is in week 9 (distinct from ISO week 10).
				expr: bson.D{{"$week", "$dateField"}},
				res:  bson.D{{"r", int32(9)}},
			},
			"IsoDayOfWeek": {
				// Saturday is 6 in ISO (Monday = 1).
				expr: bson.D{{"$isoDayOfWeek", "$dateField"}},
				res:  bson.D{{"r", int32(6)}},
			},
			"IsoWeek": {
				expr: bson.D{{"$isoWeek", "$dateField"}},
				res:  bson.D{{"r", int32(10)}},
			},
			"IsoWeekYear": {
				expr: bson.D{{"$isoWeekYear", "$dateField"}},
				res:  bson.D{{"r", int32(2024)}},
			},
			"HourTimezoneNewYork": {
				// 15:04 UTC is 10:04 (EST, UTC-5) on 2024-03-09.
				expr: bson.D{{"$hour", bson.D{
					{"date", "$dateField"},
					{"timezone", "America/New_York"},
				}}},
				res: bson.D{{"r", int32(10)}},
			},
			"HourTimezoneOffset": {
				// 15:04 UTC is 20:34 at +05:30.
				expr: bson.D{{"$hour", bson.D{
					{"date", "$dateField"},
					{"timezone", "+05:30"},
				}}},
				res: bson.D{{"r", int32(20)}},
			},
			"MinuteTimezoneOffset": {
				expr: bson.D{{"$minute", bson.D{
					{"date", "$dateField"},
					{"timezone", "+05:30"},
				}}},
				res: bson.D{{"r", int32(34)}},
			},
			"YearNull": {
				expr: bson.D{{"$year", "$nullField"}},
				res:  bson.D{{"r", nil}},
			},
			"DateToStringDefault": {
				expr: bson.D{{"$dateToString", bson.D{{"date", "$dateField"}}}},
				res:  bson.D{{"r", "2024-03-09T15:04:05.678Z"}},
			},
			"DateToStringCustom": {
				expr: bson.D{{"$dateToString", bson.D{
					{"date", "$dateField"},
					{"format", "%Y/%m/%d %H:%M:%S"},
				}}},
				res: bson.D{{"r", "2024/03/09 15:04:05"}},
			},
			"DateToStringTimezone": {
				expr: bson.D{{"$dateToString", bson.D{
					{"date", "$dateField"},
					{"format", "%H:%M %z"},
					{"timezone", "+05:30"},
				}}},
				res: bson.D{{"r", "20:34 +0530"}},
			},
			"DateToStringOnNull": {
				expr: bson.D{{"$dateToString", bson.D{
					{"date", "$nullField"},
					{"onNull", "missing"},
				}}},
				res: bson.D{{"r", "missing"}},
			},
			"DateFromString": {
				expr: bson.D{{"$dateFromString", bson.D{{"dateString", "$strField"}}}},
				res:  bson.D{{"r", primitive.NewDateTimeFromTime(date)}},
			},
			"DateFromStringFormat": {
				expr: bson.D{{"$dateFromString", bson.D{
					{"dateString", "2024-03-09 15:04:05"},
					{"format", "%Y-%m-%d %H:%M:%S"},
				}}},
				res: bson.D{{"r", primitive.NewDateTimeFromTime(
					time.Date(2024, time.March, 9, 15, 4, 5, 0, time.UTC),
				)}},
			},
			"DateFromStringOnError": {
				expr: bson.D{{"$dateFromString", bson.D{
					{"dateString", "not a date"},
					{"onError", "bad"},
				}}},
				res: bson.D{{"r", "bad"}},
			},
			"DateAddMonths": {
				expr: bson.D{{"$dateAdd", bson.D{
					{"startDate", "$dateField"},
					{"unit", "month"},
					{"amount", int64(2)},
				}}},
				res: bson.D{{"r", primitive.NewDateTimeFromTime(
					time.Date(2024, time.May, 9, 15, 4, 5, 678*int(time.Millisecond), time.UTC),
				)}},
			},
			"DateAddDays": {
				expr: bson.D{{"$dateAdd", bson.D{
					{"startDate", "$dateField"},
					{"unit", "day"},
					{"amount", int64(5)},
				}}},
				res: bson.D{{"r", primitive.NewDateTimeFromTime(
					time.Date(2024, time.March, 14, 15, 4, 5, 678*int(time.Millisecond), time.UTC),
				)}},
			},
			"DateSubtractDays": {
				expr: bson.D{{"$dateSubtract", bson.D{
					{"startDate", "$dateField"},
					{"unit", "day"},
					{"amount", int64(9)},
				}}},
				res: bson.D{{"r", primitive.NewDateTimeFromTime(
					time.Date(2024, time.February, 29, 15, 4, 5, 678*int(time.Millisecond), time.UTC),
				)}},
			},
			"DateDiffDays": {
				expr: bson.D{{"$dateDiff", bson.D{
					{"startDate", "$dateField"},
					{"endDate", "$endField"},
					{"unit", "day"},
				}}},
				res: bson.D{{"r", int64(5)}},
			},
			"DateDiffHours": {
				// 2024-03-09 15:04 -> 2024-03-14 10:00 crosses 115 hour boundaries.
				expr: bson.D{{"$dateDiff", bson.D{
					{"startDate", "$dateField"},
					{"endDate", "$endField"},
					{"unit", "hour"},
				}}},
				res: bson.D{{"r", int64(115)}},
			},
			"DateTruncMonth": {
				expr: bson.D{{"$dateTrunc", bson.D{
					{"date", "$dateField"},
					{"unit", "month"},
				}}},
				res: bson.D{{"r", primitive.NewDateTimeFromTime(
					time.Date(2024, time.March, 1, 0, 0, 0, 0, time.UTC),
				)}},
			},
			"DateTruncDay": {
				expr: bson.D{{"$dateTrunc", bson.D{
					{"date", "$dateField"},
					{"unit", "day"},
				}}},
				res: bson.D{{"r", primitive.NewDateTimeFromTime(
					time.Date(2024, time.March, 9, 0, 0, 0, 0, time.UTC),
				)}},
			},
			"DateToParts": {
				expr: bson.D{{"$dateToParts", bson.D{{"date", "$dateField"}}}},
				res: bson.D{{"r", bson.D{
					{"year", int32(2024)},
					{"month", int32(3)},
					{"day", int32(9)},
					{"hour", int32(15)},
					{"minute", int32(4)},
					{"second", int32(5)},
					{"millisecond", int32(678)},
				}}},
			},
			"DateFromParts": {
				expr: bson.D{{"$dateFromParts", bson.D{
					{"year", int32(2024)},
					{"month", int32(3)},
					{"day", int32(9)},
					{"hour", int32(15)},
					{"minute", int32(4)},
					{"second", int32(5)},
					{"millisecond", int32(678)},
				}}},
				res: bson.D{{"r", primitive.NewDateTimeFromTime(date)}},
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				pipeline := bson.A{
					bson.D{{"$project", bson.D{{"_id", false}, {"r", tc.expr}}}},
				}

				cursor, err := collection.Aggregate(ctx, pipeline)
				require.NoError(t, err)
				defer cursor.Close(ctx)

				var res []bson.D
				err = cursor.All(ctx, &res)
				require.NoError(t, err)
				require.Equal(t, []bson.D{tc.res}, res)
			})
		}
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			expr any // computed expression that must fail
		}{
			"YearOnNonDate": {
				expr: bson.D{{"$year", "$notDate"}},
			},
			"HourInvalidTimezone": {
				expr: bson.D{{"$hour", bson.D{
					{"date", "$dateField"},
					{"timezone", "Mars/Phobos"},
				}}},
			},
			"DateAddInvalidUnit": {
				expr: bson.D{{"$dateAdd", bson.D{
					{"startDate", "$dateField"},
					{"unit", "fortnight"},
					{"amount", int64(1)},
				}}},
			},
			"DateDiffInvalidUnit": {
				expr: bson.D{{"$dateDiff", bson.D{
					{"startDate", "$dateField"},
					{"endDate", "$endField"},
					{"unit", "fortnight"},
				}}},
			},
			"DateFromStringNoOnError": {
				expr: bson.D{{"$dateFromString", bson.D{{"dateString", "not a date"}}}},
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				pipeline := bson.A{
					bson.D{{"$project", bson.D{{"_id", false}, {"r", tc.expr}}}},
				}

				cursor, err := collection.Aggregate(ctx, pipeline)
				if err == nil {
					// error may surface while draining the cursor
					err = cursor.All(ctx, &[]bson.D{})
					cursor.Close(ctx)
				}

				require.Error(t, err)
			})
		}
	})
}
