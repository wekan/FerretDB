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

package handler

import (
	"slices"
	"strings"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// filterNeedsWholeDocument reports whether a filter can observe fields that its
// keys do not name. $expr and $where read arbitrary fields through expressions,
// $jsonSchema and $text read the document as a whole. When one of them appears
// anywhere in the filter, decoding only the named fields could change which
// documents match, so the whole document must be decoded.
func filterNeedsWholeDocument(doc *types.Document) bool {
	if doc == nil {
		return false
	}

	for _, key := range doc.Keys() {
		switch key {
		case "$expr", "$where", "$jsonSchema", "$text", "$function":
			return true
		}

		switch value := must.NotFail(doc.Get(key)).(type) {
		case *types.Document:
			if filterNeedsWholeDocument(value) {
				return true
			}
		case *types.Array:
			for i := 0; i < value.Len(); i++ {
				if branch, ok := must.NotFail(value.Get(i)).(*types.Document); ok && filterNeedsWholeDocument(branch) {
					return true
				}
			}
		}
	}

	return false
}

// filterDecodeFields returns the top-level fields a counting query has to
// decode to apply its filters: the filters' own fields and _id. It returns nil
// (decode whole documents) when a filter can read more than it names.
//
// A count only ever looks at the filter's fields, yet every candidate document
// used to be decoded completely - a count over a collection of large documents
// (long text fields, many keys) spent nearly all of its time decoding fields
// that nothing read.
func filterDecodeFields(filters ...*types.Document) []string {
	fields := map[string]struct{}{"_id": {}}

	for _, filter := range filters {
		if filterNeedsWholeDocument(filter) {
			return nil
		}

		collectDecodeFields(filter, fields)
	}

	res := make([]string, 0, len(fields))
	for field := range fields {
		res = append(res, field)
	}

	slices.Sort(res)

	return res
}

// countPipelineDecodeFields returns the fields to decode for an aggregation
// pipeline that only counts: any number of $match, $skip and $limit stages
// followed by a final $count, or by a $group with a constant _id whose every
// accumulator is {$sum: <number>} - the shape drivers send for countDocuments.
// Such a pipeline reads nothing but its $match filters. For any other pipeline
// it returns nil, and whole documents are decoded as before.
func countPipelineDecodeFields(pipeline []any) []string {
	if len(pipeline) == 0 {
		return nil
	}

	var filters []*types.Document

	for i, v := range pipeline {
		stage, ok := v.(*types.Document)
		if !ok || stage.Len() != 1 {
			return nil
		}

		name := stage.Keys()[0]
		value := must.NotFail(stage.Get(name))
		last := i == len(pipeline)-1

		switch name {
		case "$match":
			filter, ok := value.(*types.Document)
			if !ok || last {
				return nil
			}

			filters = append(filters, filter)

		case "$skip", "$limit":
			if last {
				return nil
			}

		case "$count":
			if _, ok := value.(string); !ok || !last {
				return nil
			}

		case "$group":
			group, ok := value.(*types.Document)
			if !ok || !last || !countingGroup(group) {
				return nil
			}

		default:
			return nil
		}
	}

	return filterDecodeFields(filters...)
}

// countingGroup reports whether a $group stage reads no document field: a
// constant _id and only {$sum: <number>} accumulators.
func countingGroup(group *types.Document) bool {
	id, err := group.Get("_id")
	if err != nil {
		return false
	}

	switch id := id.(type) {
	case *types.Document, *types.Array:
		return false
	case string:
		if strings.HasPrefix(id, "$") {
			return false
		}
	}

	for _, key := range group.Keys() {
		if key == "_id" {
			continue
		}

		acc, ok := must.NotFail(group.Get(key)).(*types.Document)
		if !ok || acc.Len() != 1 || acc.Keys()[0] != "$sum" {
			return false
		}

		switch must.NotFail(acc.Get("$sum")).(type) {
		case int32, int64, float64:
		default:
			return false
		}
	}

	return true
}
