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

// BSON value classes of the built-in MongoDB-compatible shell. Each has a
// non-enumerable _bsontype on its prototype (as the JavaScript BSON library
// does), which is how the Go side recognizes them in both directions.
// __native holds the Go helpers (validation, ObjectId generation, parsing).
(function (g, native) {
  'use strict';

  function hidden(obj, name, value) {
    Object.defineProperty(obj, name, { value: value, enumerable: false });
  }

  function bsonType(ctor, name) {
    hidden(ctor.prototype, '_bsontype', name);
  }

  function ObjectId(id) {
    if (!(this instanceof ObjectId)) return new ObjectId(id);
    if (id instanceof ObjectId) id = id.__id;
    if (id === undefined || id === null) id = native.oidNew();
    else if (typeof id === 'number') id = native.oidFromTime(id);
    else id = native.oidCheck(String(id));
    hidden(this, '__id', id);
  }
  bsonType(ObjectId, 'ObjectId');
  ObjectId.prototype.toHexString = function () { return this.__id; };
  ObjectId.prototype.toString = function () { return this.__id; };
  ObjectId.prototype.toJSON = function () { return this.__id; };
  ObjectId.prototype.valueOf = function () { return this.__id; };
  ObjectId.prototype.equals = function (o) {
    if (o instanceof ObjectId) return o.__id === this.__id;
    return String(o) === this.__id;
  };
  ObjectId.prototype.getTimestamp = function () {
    return new Date(parseInt(this.__id.substring(0, 8), 16) * 1000);
  };
  ObjectId.createFromHexString = function (h) { return new ObjectId(String(h)); };
  ObjectId.createFromTime = function (t) { return new ObjectId(Number(t)); };
  ObjectId.isValid = function (h) {
    try { native.oidCheck(String(h)); return true; } catch (e) { return false; }
  };

  function Long(v) {
    if (!(this instanceof Long)) return new Long(v);
    if (v instanceof Long) v = v.__v;
    hidden(this, '__v', native.longCheck(v === undefined ? '0' : String(v)));
  }
  bsonType(Long, 'Long');
  Long.prototype.toString = function () { return this.__v; };
  Long.prototype.toNumber = function () { return Number(this.__v); };
  Long.prototype.valueOf = function () { return Number(this.__v); };
  Long.prototype.toJSON = function () { return this.__v; };
  Long.fromNumber = function (n) { return new Long(Math.trunc(Number(n))); };
  Long.fromString = function (s) { return new Long(String(s)); };
  Long.fromInt = Long.fromNumber;

  function Int32(v) {
    if (!(this instanceof Int32)) return new Int32(v);
    hidden(this, '__v', native.int32Check(v === undefined ? 0 : Number(v)));
  }
  bsonType(Int32, 'Int32');
  Int32.prototype.valueOf = function () { return this.__v; };
  Int32.prototype.toString = function () { return String(this.__v); };
  Int32.prototype.toJSON = function () { return this.__v; };

  function Double(v) {
    if (!(this instanceof Double)) return new Double(v);
    hidden(this, '__v', v === undefined ? 0 : Number(v));
  }
  bsonType(Double, 'Double');
  Double.prototype.valueOf = function () { return this.__v; };
  Double.prototype.toString = function () { return String(this.__v); };
  Double.prototype.toJSON = function () { return this.__v; };

  function Decimal128(v) {
    if (!(this instanceof Decimal128)) return new Decimal128(v);
    hidden(this, '__v', native.decimalCheck(v === undefined ? '0' : String(v)));
  }
  bsonType(Decimal128, 'Decimal128');
  Decimal128.prototype.toString = function () { return this.__v; };
  Decimal128.prototype.toJSON = function () { return { $numberDecimal: this.__v }; };
  Decimal128.fromString = function (s) { return new Decimal128(String(s)); };

  function Binary(b64, subType) {
    if (!(this instanceof Binary)) return new Binary(b64, subType);
    hidden(this, '__b64', native.base64Check(b64 === undefined ? '' : String(b64)));
    hidden(this, 'sub_type', subType === undefined ? 0 : Number(subType));
  }
  bsonType(Binary, 'Binary');
  Binary.createFromBase64 = function (b64, subType) { return new Binary(b64, subType); };
  Binary.createFromHexString = function (h, subType) {
    return new Binary(native.hexToBase64(String(h)), subType);
  };
  Binary.prototype.base64 = function () { return this.__b64; };
  Binary.prototype.hex = function () { return native.base64ToHex(this.__b64); };
  Binary.prototype.length = function () { return native.base64Len(this.__b64); };
  Binary.prototype.toString = function (enc) {
    if (enc === 'hex') return this.hex();
    if (this.sub_type === 4 && this.length() === 16) return native.uuidFormat(this.__b64);
    return this.__b64;
  };
  Binary.prototype.toUUID = function () { return this; };
  Binary.prototype.toJSON = function () { return this.toString(); };

  function UUID(s) {
    return new Binary(s === undefined ? native.uuidNew() : native.uuidParse(String(s)), 4);
  }

  function BinData(subType, b64) { return new Binary(b64, subType); }
  function HexData(subType, h) { return Binary.createFromHexString(h, subType); }
  function MD5(h) { return Binary.createFromHexString(h, 5); }

  function Timestamp(t, i) {
    if (!(this instanceof Timestamp)) return new Timestamp(t, i);
    if (t !== null && typeof t === 'object') { i = t.i; t = t.t; }
    hidden(this, 't', native.uint32Check(t === undefined ? 0 : Number(t)));
    hidden(this, 'i', native.uint32Check(i === undefined ? 0 : Number(i)));
  }
  bsonType(Timestamp, 'Timestamp');
  Timestamp.prototype.getTime = function () { return this.t; };
  Timestamp.prototype.getInc = function () { return this.i; };
  Timestamp.prototype.toJSON = function () { return { $timestamp: { t: this.t, i: this.i } }; };

  function MinKey() { if (!(this instanceof MinKey)) return new MinKey(); }
  bsonType(MinKey, 'MinKey');
  function MaxKey() { if (!(this instanceof MaxKey)) return new MaxKey(); }
  bsonType(MaxKey, 'MaxKey');

  function BSONRegExp(pattern, options) {
    if (!(this instanceof BSONRegExp)) return new BSONRegExp(pattern, options);
    hidden(this, 'pattern', String(pattern));
    hidden(this, 'options', options === undefined ? '' : String(options));
  }
  bsonType(BSONRegExp, 'BSONRegExp');

  function Code(code, scope) {
    if (!(this instanceof Code)) return new Code(code, scope);
    hidden(this, 'code', String(code));
    hidden(this, 'scope', scope === undefined ? null : scope);
  }
  bsonType(Code, 'Code');

  function ISODate(s) {
    if (s === undefined) return new Date();
    if (s instanceof Date) return new Date(s.getTime());
    if (typeof s === 'number') return new Date(s);
    return new Date(native.parseDate(String(s)));
  }

  g.ObjectId = ObjectId;
  g.Long = Long;
  g.NumberLong = Long;
  g.Int32 = Int32;
  g.NumberInt = Int32;
  g.Double = Double;
  g.Decimal128 = Decimal128;
  g.NumberDecimal = Decimal128;
  g.Binary = Binary;
  g.UUID = UUID;
  g.BinData = BinData;
  g.HexData = HexData;
  g.MD5 = MD5;
  g.Timestamp = Timestamp;
  g.MinKey = MinKey;
  g.MaxKey = MaxKey;
  g.BSONRegExp = BSONRegExp;
  g.Code = Code;
  g.ISODate = ISODate;
})(this, __native);
