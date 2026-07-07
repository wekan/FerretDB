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

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestAggregateExprString covers the string aggregation expression operators
// added on top of FerretDB v1.24.2: $concat, $toUpper, $toLower, $strLenCP,
// $strLenBytes, $strcasecmp, $substr, $substrCP, $substrBytes, $split, $trim,
// $ltrim, $rtrim, $indexOfCP, $indexOfBytes, $replaceOne, $replaceAll and
// $regexMatch.
func TestAggregateExprString(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	// "aébc" mixes a multi-byte code point (é is 2 bytes) with ASCII, so the CP
	// and byte variants of the operators return different results.
	doc := bson.D{
		{"_id", "doc1"},
		{"s", "Hello"},
		{"multi", "aébc"},
		{"csv", "a,b,c"},
		{"padded", "--Hello--"},
		{"spaced", "  Hello  "},
		{"sentence", "the cat sat"},
		{"num", int32(42)},
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
			"Concat": {
				expr: bson.D{{"$concat", bson.A{"$s", " ", "World"}}},
				res:  bson.D{{"r", "Hello World"}},
			},
			"ConcatNull": {
				expr: bson.D{{"$concat", bson.A{"$s", "$nullField"}}},
				res:  bson.D{{"r", nil}},
			},
			"ToUpper": {
				expr: bson.D{{"$toUpper", "$s"}},
				res:  bson.D{{"r", "HELLO"}},
			},
			"ToUpperNull": {
				expr: bson.D{{"$toUpper", "$nullField"}},
				res:  bson.D{{"r", ""}},
			},
			"ToLower": {
				expr: bson.D{{"$toLower", "$s"}},
				res:  bson.D{{"r", "hello"}},
			},
			"StrLenCP": {
				expr: bson.D{{"$strLenCP", "$multi"}},
				res:  bson.D{{"r", int32(4)}},
			},
			"StrLenBytes": {
				expr: bson.D{{"$strLenBytes", "$multi"}},
				res:  bson.D{{"r", int32(5)}},
			},
			"StrcasecmpEqual": {
				expr: bson.D{{"$strcasecmp", bson.A{"$s", "HELLO"}}},
				res:  bson.D{{"r", int32(0)}},
			},
			"StrcasecmpLess": {
				expr: bson.D{{"$strcasecmp", bson.A{"$s", "world"}}},
				res:  bson.D{{"r", int32(-1)}},
			},
			"Substr": {
				expr: bson.D{{"$substr", bson.A{"$s", int32(0), int32(2)}}},
				res:  bson.D{{"r", "He"}},
			},
			"SubstrBytes": {
				expr: bson.D{{"$substrBytes", bson.A{"$multi", int32(0), int32(3)}}},
				res:  bson.D{{"r", "aé"}},
			},
			"SubstrCP": {
				expr: bson.D{{"$substrCP", bson.A{"$multi", int32(0), int32(3)}}},
				res:  bson.D{{"r", "aéb"}},
			},
			"Split": {
				expr: bson.D{{"$split", bson.A{"$csv", ","}}},
				res:  bson.D{{"r", bson.A{"a", "b", "c"}}},
			},
			"SplitNoMatch": {
				expr: bson.D{{"$split", bson.A{"$s", "x"}}},
				res:  bson.D{{"r", bson.A{"Hello"}}},
			},
			"Trim": {
				expr: bson.D{{"$trim", bson.D{{"input", "$spaced"}}}},
				res:  bson.D{{"r", "Hello"}},
			},
			"TrimChars": {
				expr: bson.D{{"$trim", bson.D{{"input", "$padded"}, {"chars", "-"}}}},
				res:  bson.D{{"r", "Hello"}},
			},
			"Ltrim": {
				expr: bson.D{{"$ltrim", bson.D{{"input", "$padded"}, {"chars", "-"}}}},
				res:  bson.D{{"r", "Hello--"}},
			},
			"Rtrim": {
				expr: bson.D{{"$rtrim", bson.D{{"input", "$padded"}, {"chars", "-"}}}},
				res:  bson.D{{"r", "--Hello"}},
			},
			"IndexOfCPHit": {
				expr: bson.D{{"$indexOfCP", bson.A{"$multi", "b"}}},
				res:  bson.D{{"r", int32(2)}},
			},
			"IndexOfCPMiss": {
				expr: bson.D{{"$indexOfCP", bson.A{"$multi", "z"}}},
				res:  bson.D{{"r", int32(-1)}},
			},
			"IndexOfBytes": {
				expr: bson.D{{"$indexOfBytes", bson.A{"$multi", "b"}}},
				res:  bson.D{{"r", int32(3)}},
			},
			"IndexOfCPStart": {
				expr: bson.D{{"$indexOfCP", bson.A{"$sentence", "t", int32(1)}}},
				res:  bson.D{{"r", int32(6)}},
			},
			"ReplaceOne": {
				expr: bson.D{{"$replaceOne", bson.D{{"input", "$sentence"}, {"find", "cat"}, {"replacement", "dog"}}}},
				res:  bson.D{{"r", "the dog sat"}},
			},
			"ReplaceAll": {
				expr: bson.D{{"$replaceAll", bson.D{{"input", "$csv"}, {"find", ","}, {"replacement", "-"}}}},
				res:  bson.D{{"r", "a-b-c"}},
			},
			"ReplaceOneNull": {
				expr: bson.D{{"$replaceOne", bson.D{{"input", "$nullField"}, {"find", "a"}, {"replacement", "b"}}}},
				res:  bson.D{{"r", nil}},
			},
			"RegexMatchTrue": {
				expr: bson.D{{"$regexMatch", bson.D{{"input", "$s"}, {"regex", "ell"}}}},
				res:  bson.D{{"r", true}},
			},
			"RegexMatchFalse": {
				expr: bson.D{{"$regexMatch", bson.D{{"input", "$s"}, {"regex", "xyz"}}}},
				res:  bson.D{{"r", false}},
			},
			"RegexMatchInsensitive": {
				expr: bson.D{{"$regexMatch", bson.D{{"input", "$s"}, {"regex", "hello"}, {"options", "i"}}}},
				res:  bson.D{{"r", true}},
			},
			"RegexMatchInsensitiveFalse": {
				expr: bson.D{{"$regexMatch", bson.D{{"input", "$s"}, {"regex", "hello"}}}},
				res:  bson.D{{"r", false}},
			},
			"RegexMatchBSONRegex": {
				expr: bson.D{{"$regexMatch", bson.D{{"input", "$s"}, {"regex", primitive.Regex{Pattern: "HELLO", Options: "i"}}}}},
				res:  bson.D{{"r", true}},
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
			"ConcatNonString": {
				expr: bson.D{{"$concat", bson.A{"$s", "$num"}}},
			},
			"StrcasecmpWrongArgsLen": {
				expr: bson.D{{"$strcasecmp", bson.A{"$s"}}},
			},
			"SplitEmptyDelimiter": {
				expr: bson.D{{"$split", bson.A{"$csv", ""}}},
			},
			"ToUpperNonString": {
				expr: bson.D{{"$toUpper", "$num"}},
			},
			"SubstrWrongArgsLen": {
				expr: bson.D{{"$substr", bson.A{"$s", int32(0)}}},
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
