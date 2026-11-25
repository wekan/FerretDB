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

package mongosh

import (
	"slices"
	"strings"
	"time"

	"github.com/dop251/goja"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// batchShown is how many documents printing a cursor shows before
// `Type "it" for more`, as in mongosh.
const batchShown = 20

// cursor is a find or aggregate cursor; the query runs on first use.
type cursor struct {
	s        *Shell
	dbName   string
	coll     string // empty for a database-level aggregate
	agg      bool
	filter   bson.D
	pipeline bson.A
	aggOpts  bson.D
	proj     bson.D
	sort     bson.D
	hint     any
	limit    int64
	skip     int64
	batch    int32
	maxTime  int64

	cur    *mongo.Cursor
	peeked bool
	done   bool
}

// applyOptions applies find options given as an object.
func (c *cursor) applyOptions(o *goja.Object) {
	if v := optValue(o, "projection"); v != nil {
		c.proj = c.s.toDoc(v, "projection")
	}

	if v := optValue(o, "sort"); v != nil {
		c.sort = c.s.toDoc(v, "sort")
	}

	if v := optValue(o, "limit"); v != nil {
		c.limit = v.ToInteger()
	}

	if v := optValue(o, "skip"); v != nil {
		c.skip = v.ToInteger()
	}

	if v := optValue(o, "batchSize"); v != nil {
		c.batch = int32(v.ToInteger())
	}
}

// start runs the query, once.
func (c *cursor) start() {
	if c.cur != nil || c.done {
		return
	}

	s := c.s
	db := s.needClient().Database(c.dbName)

	var cur *mongo.Cursor
	var err error

	switch {
	case c.agg:
		opts := options.Aggregate()

		for _, e := range c.aggOpts {
			switch e.Key {
			case "allowDiskUse":
				b, _ := e.Value.(bool)
				opts.SetAllowDiskUse(b)
			case "batchSize":
				if n, ok := e.Value.(int32); ok {
					opts.SetBatchSize(n)
				}
			case "hint":
				opts.SetHint(e.Value)
			case "let":
				opts.SetLet(e.Value)
			case "comment":
				if str, ok := e.Value.(string); ok {
					opts.SetComment(str)
				}
			}
		}

		if c.coll == "" {
			cur, err = db.Aggregate(s.ctx, c.pipeline, opts)
		} else {
			cur, err = db.Collection(c.coll).Aggregate(s.ctx, c.pipeline, opts)
		}
	default:
		opts := options.Find()
		if c.proj != nil {
			opts.SetProjection(c.proj)
		}

		if c.sort != nil {
			opts.SetSort(c.sort)
		}

		if c.limit != 0 {
			opts.SetLimit(c.limit)
		}

		if c.skip != 0 {
			opts.SetSkip(c.skip)
		}

		if c.batch != 0 {
			opts.SetBatchSize(c.batch)
		}

		if c.hint != nil {
			opts.SetHint(c.hint)
		}

		if c.maxTime != 0 {
			opts.SetMaxTime(time.Duration(c.maxTime) * time.Millisecond)
		}

		cur, err = db.Collection(c.coll).Find(s.ctx, c.filter, opts)
	}

	if err != nil {
		c.done = true
		s.throwErr(err)
	}

	c.cur = cur
}

// hasNext reports whether another document is available.
func (c *cursor) hasNext() bool {
	if c.peeked {
		return true
	}

	c.start()

	if c.done {
		return false
	}

	if c.cur.Next(c.s.ctx) {
		c.peeked = true
		return true
	}

	err := c.cur.Err()
	_ = c.cur.Close(c.s.ctx)
	c.done = true

	if err != nil {
		c.s.throwErr(err)
	}

	return false
}

// next returns the next document, or nil.
func (c *cursor) next() goja.Value {
	if !c.hasNext() {
		return nil
	}

	c.peeked = false

	return c.s.fromRaw(slices.Clone(c.cur.Current))
}

// all returns the remaining documents.
func (c *cursor) all() []any {
	var items []any
	for d := c.next(); d != nil; d = c.next() {
		items = append(items, d)
	}

	return items
}

// notStarted throws when a query modifier is applied after the query ran.
func (c *cursor) notStarted(what string) {
	if c.cur != nil || c.done {
		c.s.throw("MongoshInvalidInputError", "cannot call "+what+" after the cursor was used")
	}
}

// cursorOf returns the cursor behind a cursor object, or nil.
func cursorOf(obj *goja.Object) *cursor {
	v := obj.Get("__cursor")
	if v == nil {
		return nil
	}

	c, _ := v.Export().(*cursor)

	return c
}

// cursorString prints up to batchShown documents of a cursor; more are
// shown by the `it` command.
func (s *Shell) cursorString(c *cursor, it bool) string {
	var entries []string

	in := &inspector{s: s, seen: map[*goja.Object]bool{}}

	for len(entries) < batchShown {
		d := c.next()
		if d == nil {
			break
		}

		entries = append(entries, in.format(d, 2, 1))
	}

	if len(entries) == 0 && it {
		s.lastCursor = nil
		return "no cursor"
	}

	out := wrap("[", "]", entries, 0, true)

	if c.hasNext() {
		s.lastCursor = c
		out += "\nType \"it\" for more"
	} else if s.lastCursor == c {
		s.lastCursor = nil
	}

	return out
}

// cursorObject wraps a cursor in its JavaScript methods.
func (s *Shell) cursorObject(c *cursor) goja.Value {
	vm := s.vm
	obj := vm.NewObject()
	_ = obj.DefineDataProperty("__cursor", vm.ToValue(c), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)

	chain := func(name string, f func(goja.Value)) {
		_ = obj.Set(name, func(a goja.FunctionCall) goja.Value {
			c.notStarted(name + "()")
			f(a.Argument(0))

			return obj
		})
	}

	chain("limit", func(v goja.Value) { c.limit = v.ToInteger() })
	chain("skip", func(v goja.Value) { c.skip = v.ToInteger() })
	chain("sort", func(v goja.Value) { c.sort = s.toDoc(v, "sort") })
	chain("projection", func(v goja.Value) { c.proj = s.toDoc(v, "projection") })
	chain("batchSize", func(v goja.Value) { c.batch = int32(v.ToInteger()) })
	chain("hint", func(v goja.Value) {
		if str, ok := v.Export().(string); ok {
			c.hint = str
		} else {
			c.hint = s.toDoc(v, "hint")
		}
	})
	chain("maxTimeMS", func(v goja.Value) { c.maxTime = v.ToInteger() })

	same := func(goja.FunctionCall) goja.Value { return obj }
	_ = obj.Set("pretty", same)
	_ = obj.Set("noCursorTimeout", same)

	_ = obj.Set("toArray", func(goja.FunctionCall) goja.Value { return vm.NewArray(c.all()...) })
	_ = obj.Set("hasNext", func(goja.FunctionCall) goja.Value { return vm.ToValue(c.hasNext()) })
	_ = obj.Set("next", func(goja.FunctionCall) goja.Value {
		if d := c.next(); d != nil {
			return d
		}

		return goja.Null()
	})
	_ = obj.Set("tryNext", obj.Get("next"))
	_ = obj.Set("isExhausted", func(goja.FunctionCall) goja.Value { return vm.ToValue(!c.hasNext()) })
	_ = obj.Set("close", func(goja.FunctionCall) goja.Value {
		if c.cur != nil {
			_ = c.cur.Close(s.ctx)
		}

		c.done = true

		return goja.Undefined()
	})

	callback := func(a goja.FunctionCall) goja.Callable {
		f, ok := goja.AssertFunction(a.Argument(0))
		if !ok {
			s.throw("MongoshInvalidInputError", "a function is required")
		}

		return f
	}

	_ = obj.Set("forEach", func(a goja.FunctionCall) goja.Value {
		f := callback(a)

		for d := c.next(); d != nil; d = c.next() {
			r, err := f(goja.Undefined(), d)
			if err != nil {
				panic(err)
			}

			if r != nil && r.Export() == false {
				break
			}
		}

		return goja.Undefined()
	})
	_ = obj.Set("map", func(a goja.FunctionCall) goja.Value {
		f := callback(a)

		var items []any

		for d := c.next(); d != nil; d = c.next() {
			r, err := f(goja.Undefined(), d)
			if err != nil {
				panic(err)
			}

			items = append(items, r)
		}

		return vm.NewArray(items...)
	})

	count := func(applySkipLimit bool) goja.Value {
		if c.agg {
			s.throw("MongoshInvalidInputError", "count() is not available on an aggregation cursor")
		}

		opts := options.Count()
		if applySkipLimit {
			if c.limit != 0 {
				opts.SetLimit(c.limit)
			}

			if c.skip != 0 {
				opts.SetSkip(c.skip)
			}
		}

		n, err := s.needClient().Database(c.dbName).Collection(c.coll).CountDocuments(s.ctx, c.filter, opts)
		if err != nil {
			s.throwErr(err)
		}

		return vm.ToValue(n)
	}
	_ = obj.Set("count", func(a goja.FunctionCall) goja.Value { return count(a.Argument(0).ToBoolean()) })
	_ = obj.Set("size", func(goja.FunctionCall) goja.Value { return count(true) })
	_ = obj.Set("itcount", func(goja.FunctionCall) goja.Value { return vm.ToValue(len(c.all())) })
	_ = obj.Set("explain", func(goja.FunctionCall) goja.Value {
		var inner bson.D
		if c.agg {
			inner = bson.D{{Key: "aggregate", Value: c.coll}, {Key: "pipeline", Value: c.pipeline}, {Key: "cursor", Value: bson.D{}}}
		} else {
			inner = bson.D{{Key: "find", Value: c.coll}, {Key: "filter", Value: c.filter}}
			if c.sort != nil {
				inner = append(inner, bson.E{Key: "sort", Value: c.sort})
			}
		}

		return s.runCommandDoc(c.dbName, bson.D{{Key: "explain", Value: inner}})
	})
	_ = obj.Set("toString", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(strings.TrimSpace("Cursor " + c.dbName + "." + c.coll))
	})

	return obj
}
