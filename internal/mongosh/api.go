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
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// fn is the signature of every shell method implemented in Go.
type fn = func(goja.FunctionCall) goja.Value

// reservedKeys are property names that are never collection names, so that
// JavaScript itself (and Promise detection) sees an ordinary object.
var reservedKeys = map[string]bool{
	"then": true, "constructor": true, "toJSON": true, "hasOwnProperty": true, "isPrototypeOf": true,
	"propertyIsEnumerable": true, "toLocaleString": true, "valueOf": true, "__proto__": true,
	"_bsontype": true, "__cursor": true, "inspect": true, "length": true,
}

// throw throws a JavaScript Error with the given name; it does not return.
func (s *Shell) throw(name, msg string) {
	panic(s.newError(name, msg, 0, ""))
}

// newError creates a JavaScript Error with the given name and server code.
func (s *Shell) newError(name, msg string, code int32, codeName string) goja.Value {
	e, err := s.vm.New(s.vm.Get("Error"), s.vm.ToValue(msg))
	if err != nil {
		panic(err)
	}

	_ = e.Set("name", name)

	if code != 0 {
		_ = e.Set("code", code)
	}

	if codeName != "" {
		_ = e.Set("codeName", codeName)
	}

	return e
}

// throwErr throws a driver error the way mongosh reports it.
func (s *Shell) throwErr(err error) {
	var cmdErr mongo.CommandError
	var writeErr mongo.WriteException
	var bulkErr mongo.BulkWriteException

	switch {
	case errors.As(err, &cmdErr):
		panic(s.newError("MongoServerError", cmdErr.Message, cmdErr.Code, cmdErr.Name))
	case errors.As(err, &writeErr):
		if len(writeErr.WriteErrors) > 0 {
			we := writeErr.WriteErrors[0]
			panic(s.newError("MongoServerError", we.Message, int32(we.Code), ""))
		}

		if writeErr.WriteConcernError != nil {
			panic(s.newError("MongoServerError", writeErr.WriteConcernError.Message, int32(writeErr.WriteConcernError.Code), ""))
		}
	case errors.As(err, &bulkErr):
		if len(bulkErr.WriteErrors) > 0 {
			we := bulkErr.WriteErrors[0]
			panic(s.newError("MongoBulkWriteError", we.Message, int32(we.Code), ""))
		}
	case mongo.IsTimeout(err):
		panic(s.newError("MongoServerSelectionError", err.Error(), 0, ""))
	}

	panic(s.newError("MongoError", err.Error(), 0, ""))
}

// needClient throws when the shell was started without a connection.
func (s *Shell) needClient() *mongo.Client {
	if s.client == nil {
		s.throw("MongoshInvalidInputError", "not connected to a server (started with --nodb)")
	}

	return s.client
}

// runCommand runs a command document against a database and returns the reply.
func (s *Shell) runCommand(dbName string, cmd goja.Value) goja.Value {
	var doc bson.D

	if str, ok := cmd.Export().(string); ok {
		doc = bson.D{{Key: str, Value: int32(1)}}
	} else {
		doc = s.toDoc(cmd, "command")
	}

	if len(doc) == 0 {
		s.throw("MongoshInvalidInputError", "the command document is empty")
	}

	raw, err := s.needClient().Database(dbName).RunCommand(s.ctx, doc).Raw()
	if err != nil {
		s.throwErr(err)
	}

	return s.fromRaw(raw)
}

// method adds a Go function to a methods map.
func method(m map[string]goja.Value, vm *goja.Runtime, name string, f fn) {
	m[name] = vm.ToValue(f)
}

// dbObject is the JavaScript `db`: its methods, and any other property is a
// collection of that name.
type dbObject struct {
	s       *Shell
	name    string
	methods map[string]goja.Value
	colls   map[string]*goja.Object
}

// newDB returns the JavaScript object for a database.
func (s *Shell) newDB(name string) *goja.Object {
	d := &dbObject{s: s, name: name, methods: map[string]goja.Value{}, colls: map[string]*goja.Object{}}
	vm, m := s.vm, d.methods

	method(m, vm, "getName", func(goja.FunctionCall) goja.Value { return vm.ToValue(name) })
	method(m, vm, "toString", func(goja.FunctionCall) goja.Value { return vm.ToValue(name) })
	method(m, vm, "getSiblingDB", func(c goja.FunctionCall) goja.Value { return s.newDB(c.Argument(0).String()) })
	method(m, vm, "getCollection", func(c goja.FunctionCall) goja.Value { return d.coll(c.Argument(0).String()) })
	method(m, vm, "getMongo", func(goja.FunctionCall) goja.Value { return s.vm.Get("db") })
	method(m, vm, "runCommand", func(c goja.FunctionCall) goja.Value { return s.runCommand(name, c.Argument(0)) })
	method(m, vm, "adminCommand", func(c goja.FunctionCall) goja.Value { return s.runCommand("admin", c.Argument(0)) })
	method(m, vm, "hello", func(goja.FunctionCall) goja.Value { return s.runCommand(name, vm.ToValue("hello")) })
	method(m, vm, "isMaster", func(goja.FunctionCall) goja.Value { return s.runCommand(name, vm.ToValue("isMaster")) })
	method(m, vm, "serverStatus", func(goja.FunctionCall) goja.Value {
		return s.runCommand("admin", vm.ToValue("serverStatus"))
	})
	method(m, vm, "stats", func(goja.FunctionCall) goja.Value { return s.runCommand(name, vm.ToValue("dbStats")) })
	method(m, vm, "version", func(goja.FunctionCall) goja.Value {
		return s.runCommand("admin", vm.ToValue("buildInfo")).(*goja.Object).Get("version")
	})
	method(m, vm, "getCollectionNames", func(goja.FunctionCall) goja.Value {
		names, err := s.needClient().Database(name).ListCollectionNames(s.ctx, bson.D{})
		if err != nil {
			s.throwErr(err)
		}

		sort.Strings(names)

		return vm.ToValue(names)
	})
	method(m, vm, "getCollectionInfos", func(c goja.FunctionCall) goja.Value {
		cur, err := s.needClient().Database(name).ListCollections(s.ctx, s.toDoc(c.Argument(0), "filter"))
		if err != nil {
			s.throwErr(err)
		}

		return s.drain(cur)
	})
	method(m, vm, "createCollection", func(c goja.FunctionCall) goja.Value {
		cmd := bson.D{{Key: "create", Value: c.Argument(0).String()}}
		cmd = append(cmd, s.toDoc(c.Argument(1), "options")...)

		return s.runCommandDoc(name, cmd)
	})
	method(m, vm, "dropDatabase", func(goja.FunctionCall) goja.Value {
		if err := s.needClient().Database(name).Drop(s.ctx); err != nil {
			s.throwErr(err)
		}

		res := vm.NewObject()
		_ = res.Set("ok", 1)
		_ = res.Set("dropped", name)

		return res
	})
	method(m, vm, "createUser", func(c goja.FunctionCall) goja.Value {
		u := s.toDoc(c.Argument(0), "user")
		cmd := bson.D{}

		for _, e := range u {
			if e.Key == "user" {
				cmd = append(bson.D{{Key: "createUser", Value: e.Value}}, cmd...)
				continue
			}

			cmd = append(cmd, e)
		}

		return s.runCommandDoc(name, cmd)
	})
	method(m, vm, "dropUser", func(c goja.FunctionCall) goja.Value {
		return s.runCommandDoc(name, bson.D{{Key: "dropUser", Value: c.Argument(0).String()}})
	})
	method(m, vm, "getUsers", func(goja.FunctionCall) goja.Value {
		return s.runCommandDoc(name, bson.D{{Key: "usersInfo", Value: int32(1)}})
	})
	method(m, vm, "aggregate", func(c goja.FunctionCall) goja.Value {
		cur := &cursor{s: s, dbName: name, agg: true, pipeline: s.pipeline(c)}
		return s.cursorObject(cur)
	})

	return vm.NewDynamicObject(d)
}

// runCommandDoc runs a command built in Go.
func (s *Shell) runCommandDoc(dbName string, cmd bson.D) goja.Value {
	raw, err := s.needClient().Database(dbName).RunCommand(s.ctx, cmd).Raw()
	if err != nil {
		s.throwErr(err)
	}

	return s.fromRaw(raw)
}

// coll returns the (cached) collection object for name.
func (d *dbObject) coll(name string) *goja.Object {
	if c, ok := d.colls[name]; ok {
		return c
	}

	c := d.s.newColl(d.name, name)
	d.colls[name] = c

	return c
}

// Get implements goja.DynamicObject.
func (d *dbObject) Get(key string) goja.Value {
	if m, ok := d.methods[key]; ok {
		return m
	}

	if reservedKeys[key] || key == "" {
		return nil
	}

	return d.coll(key)
}

// Set implements goja.DynamicObject.
func (d *dbObject) Set(string, goja.Value) bool { return false }

// Has implements goja.DynamicObject.
func (d *dbObject) Has(key string) bool { return d.methods[key] != nil }

// Delete implements goja.DynamicObject.
func (d *dbObject) Delete(string) bool { return true }

// Keys implements goja.DynamicObject.
func (d *dbObject) Keys() []string { return nil }

// collObject is a JavaScript collection, db.<name>.
type collObject struct {
	s       *Shell
	db      string
	name    string
	methods map[string]goja.Value
	subs    map[string]*goja.Object
}

// Get implements goja.DynamicObject.
func (c *collObject) Get(key string) goja.Value {
	if m, ok := c.methods[key]; ok {
		return m
	}

	if reservedKeys[key] || key == "" {
		return nil
	}

	if sub, ok := c.subs[key]; ok {
		return sub
	}

	sub := c.s.newColl(c.db, c.name+"."+key)
	c.subs[key] = sub

	return sub
}

// Set implements goja.DynamicObject.
func (c *collObject) Set(string, goja.Value) bool { return false }

// Has implements goja.DynamicObject.
func (c *collObject) Has(key string) bool { return c.methods[key] != nil }

// Delete implements goja.DynamicObject.
func (c *collObject) Delete(string) bool { return true }

// Keys implements goja.DynamicObject.
func (c *collObject) Keys() []string { return nil }

// writeResult builds a result object with acknowledged: true first.
func (s *Shell) writeResult(kv ...any) goja.Value {
	res := s.vm.NewObject()
	_ = res.Set("acknowledged", true)

	for i := 0; i+1 < len(kv); i += 2 {
		_ = res.Set(kv[i].(string), kv[i+1])
	}

	return res
}

// optDoc returns the options argument as a map of JavaScript values.
func optDoc(v goja.Value) *goja.Object {
	if o, ok := v.(*goja.Object); ok {
		return o
	}

	return nil
}

// optBool reads a boolean option.
func optBool(o *goja.Object, name string) bool {
	if o == nil {
		return false
	}

	v := o.Get(name)

	return v != nil && !goja.IsUndefined(v) && v.ToBoolean()
}

// optValue reads an option, or nil when it is not given.
func optValue(o *goja.Object, name string) goja.Value {
	if o == nil {
		return nil
	}

	v := o.Get(name)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}

	return v
}

// indexName generates the default index name, as the server does: a_1_b_-1.
func indexName(keys bson.D) string {
	parts := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		parts = append(parts, k.Key, fmt.Sprint(k.Value))
	}

	return strings.Join(parts, "_")
}

// pipeline reads an aggregation pipeline: one array, or stages as arguments.
func (s *Shell) pipeline(c goja.FunctionCall) bson.A {
	if len(c.Arguments) == 0 {
		return bson.A{}
	}

	if a, ok := s.toBSON(c.Argument(0)).(bson.A); ok {
		return a
	}

	p := bson.A{}
	for _, st := range c.Arguments {
		p = append(p, s.toDoc(st, "pipeline stage"))
	}

	return p
}

// newColl returns the JavaScript object for a collection.
func (s *Shell) newColl(dbName, name string) *goja.Object {
	c := &collObject{s: s, db: dbName, name: name, methods: map[string]goja.Value{}, subs: map[string]*goja.Object{}}
	vm, m := s.vm, c.methods
	mc := func() *mongo.Collection { return s.needClient().Database(dbName).Collection(name) }

	method(m, vm, "getName", func(goja.FunctionCall) goja.Value { return vm.ToValue(name) })
	method(m, vm, "getFullName", func(goja.FunctionCall) goja.Value { return vm.ToValue(dbName + "." + name) })
	method(m, vm, "toString", func(goja.FunctionCall) goja.Value { return vm.ToValue(dbName + "." + name) })
	method(m, vm, "getDB", func(goja.FunctionCall) goja.Value { return s.newDB(dbName) })

	method(m, vm, "find", func(a goja.FunctionCall) goja.Value {
		cur := &cursor{s: s, dbName: dbName, coll: name, filter: s.toDoc(a.Argument(0), "filter")}
		if p := a.Argument(1); !goja.IsUndefined(p) && !goja.IsNull(p) {
			cur.proj = s.toDoc(p, "projection")
		}

		if o := optDoc(a.Argument(2)); o != nil {
			cur.applyOptions(o)
		}

		return s.cursorObject(cur)
	})
	method(m, vm, "findOne", func(a goja.FunctionCall) goja.Value {
		opts := options.FindOne()
		if p := a.Argument(1); !goja.IsUndefined(p) && !goja.IsNull(p) {
			opts.SetProjection(s.toDoc(p, "projection"))
		}

		if o := optDoc(a.Argument(2)); o != nil {
			if v := optValue(o, "sort"); v != nil {
				opts.SetSort(s.toDoc(v, "sort"))
			}

			if v := optValue(o, "skip"); v != nil {
				opts.SetSkip(v.ToInteger())
			}
		}

		raw, err := mc().FindOne(s.ctx, s.toDoc(a.Argument(0), "filter"), opts).Raw()
		if errors.Is(err, mongo.ErrNoDocuments) {
			return goja.Null()
		}

		if err != nil {
			s.throwErr(err)
		}

		return s.fromRaw(raw)
	})
	method(m, vm, "insertOne", func(a goja.FunctionCall) goja.Value {
		res, err := mc().InsertOne(s.ctx, s.toDoc(a.Argument(0), "document"))
		if err != nil {
			s.throwErr(err)
		}

		return s.writeResult("insertedId", s.fromGo(res.InsertedID))
	})
	method(m, vm, "insertMany", func(a goja.FunctionCall) goja.Value {
		docs, ok := s.toBSON(a.Argument(0)).(bson.A)
		if !ok {
			s.throw("MongoshInvalidInputError", "insertMany requires an array of documents")
		}

		opts := options.InsertMany()
		if o := optDoc(a.Argument(1)); o != nil && optValue(o, "ordered") != nil {
			opts.SetOrdered(optBool(o, "ordered"))
		}

		res, err := mc().InsertMany(s.ctx, docs, opts)
		if err != nil {
			s.throwErr(err)
		}

		ids := vm.NewObject()
		for i, id := range res.InsertedIDs {
			_ = ids.Set(strconv.Itoa(i), s.fromGo(id))
		}

		return s.writeResult("insertedIds", ids)
	})

	update := func(many, replace bool) fn {
		return func(a goja.FunctionCall) goja.Value {
			filter := s.toDoc(a.Argument(0), "filter")
			o := optDoc(a.Argument(2))
			opts := options.Update().SetUpsert(optBool(o, "upsert"))
			ropts := options.Replace().SetUpsert(optBool(o, "upsert"))

			if v := optValue(o, "arrayFilters"); v != nil {
				if af, ok := s.toBSON(v).(bson.A); ok {
					opts.SetArrayFilters(options.ArrayFilters{Filters: af})
				}
			}

			var res *mongo.UpdateResult
			var err error

			switch {
			case replace:
				res, err = mc().ReplaceOne(s.ctx, filter, s.toDoc(a.Argument(1), "replacement"), ropts)
			case many:
				res, err = mc().UpdateMany(s.ctx, filter, s.toDocOrPipeline(a.Argument(1), "update"), opts)
			default:
				res, err = mc().UpdateOne(s.ctx, filter, s.toDocOrPipeline(a.Argument(1), "update"), opts)
			}

			if err != nil {
				s.throwErr(err)
			}

			return s.writeResult(
				"insertedId", s.fromGo(res.UpsertedID),
				"matchedCount", res.MatchedCount,
				"modifiedCount", res.ModifiedCount,
				"upsertedCount", res.UpsertedCount,
			)
		}
	}
	method(m, vm, "updateOne", update(false, false))
	method(m, vm, "updateMany", update(true, false))
	method(m, vm, "replaceOne", update(false, true))

	del := func(many bool) fn {
		return func(a goja.FunctionCall) goja.Value {
			var res *mongo.DeleteResult
			var err error

			if many {
				res, err = mc().DeleteMany(s.ctx, s.toDoc(a.Argument(0), "filter"))
			} else {
				res, err = mc().DeleteOne(s.ctx, s.toDoc(a.Argument(0), "filter"))
			}

			if err != nil {
				s.throwErr(err)
			}

			return s.writeResult("deletedCount", res.DeletedCount)
		}
	}
	method(m, vm, "deleteOne", del(false))
	method(m, vm, "deleteMany", del(true))

	findAndModify := func(kind string) fn {
		return func(a goja.FunctionCall) goja.Value {
			cmd := bson.D{{Key: "findAndModify", Value: name}, {Key: "query", Value: s.toDoc(a.Argument(0), "filter")}}

			oi := 2
			switch kind {
			case "delete":
				cmd = append(cmd, bson.E{Key: "remove", Value: true})
				oi = 1
			case "replace":
				cmd = append(cmd, bson.E{Key: "update", Value: s.toDoc(a.Argument(1), "replacement")})
			default:
				cmd = append(cmd, bson.E{Key: "update", Value: s.toDocOrPipeline(a.Argument(1), "update")})
			}

			o := optDoc(a.Argument(oi))
			if v := optValue(o, "returnDocument"); v != nil && v.String() == "after" || optBool(o, "returnNewDocument") {
				cmd = append(cmd, bson.E{Key: "new", Value: true})
			}

			if optBool(o, "upsert") {
				cmd = append(cmd, bson.E{Key: "upsert", Value: true})
			}

			if v := optValue(o, "sort"); v != nil {
				cmd = append(cmd, bson.E{Key: "sort", Value: s.toDoc(v, "sort")})
			}

			if v := optValue(o, "projection"); v != nil {
				cmd = append(cmd, bson.E{Key: "fields", Value: s.toDoc(v, "projection")})
			}

			return s.runCommandDoc(dbName, cmd).(*goja.Object).Get("value")
		}
	}
	method(m, vm, "findOneAndUpdate", findAndModify("update"))
	method(m, vm, "findOneAndReplace", findAndModify("replace"))
	method(m, vm, "findOneAndDelete", findAndModify("delete"))

	count := func(a goja.FunctionCall) goja.Value {
		opts := options.Count()
		if o := optDoc(a.Argument(1)); o != nil {
			if v := optValue(o, "limit"); v != nil {
				opts.SetLimit(v.ToInteger())
			}

			if v := optValue(o, "skip"); v != nil {
				opts.SetSkip(v.ToInteger())
			}
		}

		n, err := mc().CountDocuments(s.ctx, s.toDoc(a.Argument(0), "filter"), opts)
		if err != nil {
			s.throwErr(err)
		}

		return vm.ToValue(n)
	}
	method(m, vm, "countDocuments", count)
	method(m, vm, "count", count)
	method(m, vm, "estimatedDocumentCount", func(goja.FunctionCall) goja.Value {
		n, err := mc().EstimatedDocumentCount(s.ctx)
		if err != nil {
			s.throwErr(err)
		}

		return vm.ToValue(n)
	})
	method(m, vm, "distinct", func(a goja.FunctionCall) goja.Value {
		cmd := bson.D{
			{Key: "distinct", Value: name},
			{Key: "key", Value: a.Argument(0).String()},
			{Key: "query", Value: s.toDoc(a.Argument(1), "filter")},
		}

		return s.runCommandDoc(dbName, cmd).(*goja.Object).Get("values")
	})
	method(m, vm, "aggregate", func(a goja.FunctionCall) goja.Value {
		cur := &cursor{s: s, dbName: dbName, coll: name, agg: true, pipeline: s.pipeline(a)}

		// aggregate(pipeline, options): the second argument is options only
		// when the first is an array.
		if _, isArr := s.toBSON(a.Argument(0)).(bson.A); isArr {
			if o := optDoc(a.Argument(1)); o != nil {
				cur.aggOpts = s.toDoc(o, "options")
			}
		}

		return s.cursorObject(cur)
	})
	method(m, vm, "createIndex", func(a goja.FunctionCall) goja.Value {
		names := s.createIndexes(dbName, name, []goja.Value{a.Argument(0)}, a.Argument(1))
		return vm.ToValue(names[0])
	})
	method(m, vm, "createIndexes", func(a goja.FunctionCall) goja.Value {
		arr, ok := a.Argument(0).(*goja.Object)
		if !ok || arr.ClassName() != "Array" {
			s.throw("MongoshInvalidInputError", "createIndexes requires an array of index keys")
		}

		var specs []goja.Value
		for i := int64(0); i < arr.Get("length").ToInteger(); i++ {
			specs = append(specs, arr.Get(strconv.FormatInt(i, 10)))
		}

		return vm.ToValue(s.createIndexes(dbName, name, specs, a.Argument(1)))
	})
	method(m, vm, "getIndexes", func(goja.FunctionCall) goja.Value {
		cur, err := mc().Indexes().List(s.ctx)
		if err != nil {
			s.throwErr(err)
		}

		return s.drain(cur)
	})
	method(m, vm, "dropIndex", func(a goja.FunctionCall) goja.Value {
		var index any = a.Argument(0).String()
		if _, ok := a.Argument(0).(*goja.Object); ok {
			index = s.toDoc(a.Argument(0), "index")
		}

		return s.runCommandDoc(dbName, bson.D{{Key: "dropIndexes", Value: name}, {Key: "index", Value: index}})
	})
	method(m, vm, "dropIndexes", func(goja.FunctionCall) goja.Value {
		return s.runCommandDoc(dbName, bson.D{{Key: "dropIndexes", Value: name}, {Key: "index", Value: "*"}})
	})
	method(m, vm, "drop", func(goja.FunctionCall) goja.Value {
		if err := mc().Drop(s.ctx); err != nil {
			s.throwErr(err)
		}

		return vm.ToValue(true)
	})
	method(m, vm, "stats", func(goja.FunctionCall) goja.Value {
		return s.runCommandDoc(dbName, bson.D{{Key: "collStats", Value: name}})
	})
	method(m, vm, "renameCollection", func(a goja.FunctionCall) goja.Value {
		return s.runCommandDoc("admin", bson.D{
			{Key: "renameCollection", Value: dbName + "." + name},
			{Key: "to", Value: dbName + "." + a.Argument(0).String()},
			{Key: "dropTarget", Value: a.Argument(1).ToBoolean()},
		})
	})

	return vm.NewDynamicObject(c)
}

// createIndexes creates indexes from key documents and shared options and
// returns their names.
func (s *Shell) createIndexes(dbName, coll string, keys []goja.Value, opts goja.Value) []string {
	o := s.toDoc(opts, "options")
	indexes := bson.A{}
	names := make([]string, 0, len(keys))

	for _, k := range keys {
		kd := s.toDoc(k, "index keys")
		if len(kd) == 0 {
			s.throw("MongoshInvalidInputError", "index keys must not be empty")
		}

		spec := bson.D{{Key: "key", Value: kd}}

		name := indexName(kd)
		for _, e := range o {
			if e.Key == "name" {
				name = fmt.Sprint(e.Value)
				continue
			}

			if e.Key == "background" {
				continue
			}

			spec = append(spec, e)
		}

		spec = append(spec, bson.E{Key: "name", Value: name})
		indexes = append(indexes, spec)
		names = append(names, name)
	}

	s.runCommandDoc(dbName, bson.D{{Key: "createIndexes", Value: coll}, {Key: "indexes", Value: indexes}})

	return names
}

// drain returns every document of a driver cursor as a JavaScript array.
func (s *Shell) drain(cur *mongo.Cursor) goja.Value {
	defer cur.Close(s.ctx) //nolint:errcheck // nothing to do about it

	var items []any
	for cur.Next(s.ctx) {
		items = append(items, s.fromRaw(slices.Clone(cur.Current)))
	}

	if err := cur.Err(); err != nil {
		s.throwErr(err)
	}

	return s.vm.NewArray(items...)
}

// rs returns the replica set helper object.
func (s *Shell) rs() *goja.Object {
	rs := s.vm.NewObject()
	_ = rs.Set("status", func(goja.FunctionCall) goja.Value {
		return s.runCommand("admin", s.vm.ToValue("replSetGetStatus"))
	})
	_ = rs.Set("conf", func(goja.FunctionCall) goja.Value {
		return s.runCommand("admin", s.vm.ToValue("replSetGetConfig")).(*goja.Object).Get("config")
	})
	_ = rs.Set("config", rs.Get("conf"))
	_ = rs.Set("initiate", func(c goja.FunctionCall) goja.Value {
		var cfg any = bson.D{}
		if v := c.Argument(0); !goja.IsUndefined(v) {
			cfg = s.toDoc(v, "config")
		}

		return s.runCommandDoc("admin", bson.D{{Key: "replSetInitiate", Value: cfg}})
	})
	_ = rs.Set("hello", func(goja.FunctionCall) goja.Value { return s.runCommand("admin", s.vm.ToValue("hello")) })
	_ = rs.Set("isMaster", rs.Get("hello"))

	return rs
}
