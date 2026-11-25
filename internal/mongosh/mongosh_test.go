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
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// nodb runs the shell without a server and returns stdout, stderr and the code.
func nodb(t *testing.T, args ...string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	code := Main(context.Background(), append([]string{"--nodb", "--quiet"}, args...), IO{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &errOut,
		Getenv: func(string) string { return "" },
	})

	return out.String(), errOut.String(), code
}

func TestParseArgs(t *testing.T) {
	t.Parallel()

	o, err := parseArgs([]string{
		"mongodb://h:1/db", "--quiet", "-u", "bob", "-p", "pw", "--authenticationDatabase=admin",
		"--eval", "1", "--eval=2", "a.js", "-f", "b.js", "--json=canonical",
	})
	require.NoError(t, err)
	assert.Equal(t, "mongodb://h:1/db", o.address)
	assert.True(t, o.quiet)
	assert.Equal(t, "bob", o.username)
	assert.Equal(t, "pw", o.password)
	assert.Equal(t, "admin", o.authDB)
	assert.Equal(t, []string{"1", "2"}, o.evals)
	assert.Equal(t, []string{"a.js", "b.js"}, o.files)
	assert.Equal(t, "canonical", o.json)

	o, err = parseArgs([]string{"-u", "bob", "-p", "--quiet"})
	require.NoError(t, err)
	assert.True(t, o.passwordPrompt, "-p without a value prompts")

	for name, args := range map[string][]string{
		"unknown option":     {"--bogus"},
		"missing value":      {"--eval"},
		"two addresses":      {"h1", "h2"},
		"json without eval":  {"--json"},
		"bad json mode":      {"--eval", "1", "--json=pretty"},
		"missing host value": {"--host"},
	} {
		_, err := parseArgs(args)
		assert.Error(t, err, name)
	}
}

func TestConnectionURI(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	for name, tc := range map[string]struct {
		args []string
		env  map[string]string
		uri  string
		db   string
	}{
		"default": {
			uri: "mongodb://127.0.0.1:27017/test?directConnection=true&appName=mongosh", db: "test",
		},
		"listen addr wildcard": {
			env: map[string]string{"FERRETDB_LISTEN_ADDR": "0.0.0.0:27018"},
			uri: "mongodb://127.0.0.1:27018/test?directConnection=true&appName=mongosh", db: "test",
		},
		"listen addr without host": {
			env: map[string]string{"FERRETDB_LISTEN_ADDR": ":27019"},
			uri: "mongodb://127.0.0.1:27019/test?directConnection=true&appName=mongosh", db: "test",
		},
		"database name": {
			args: []string{"mydb"},
			uri:  "mongodb://127.0.0.1:27017/mydb?directConnection=true&appName=mongosh", db: "mydb",
		},
		"host port db": {
			args: []string{"example.com:1234/other"},
			uri:  "mongodb://example.com:1234/other?directConnection=true&appName=mongosh", db: "other",
		},
		"host and port flags": {
			args: []string{"--host", "db.example", "--port", "4000"},
			uri:  "mongodb://db.example:4000/test?directConnection=true&appName=mongosh", db: "test",
		},
		"uri": {
			args: []string{"mongodb://u:p@h:1/x?replicaSet=rs0"},
			uri:  "mongodb://u:p@h:1/x?replicaSet=rs0&appName=mongosh", db: "x",
		},
		"uri without path": {
			args: []string{"mongodb://h:1"},
			uri:  "mongodb://h:1/?directConnection=true&appName=mongosh", db: "test",
		},
		"tls": {
			args: []string{"--tls", "--tlsCAFile", "/ca.pem", "mongodb://h:1/"},
			uri:  "mongodb://h:1/?directConnection=true&appName=mongosh&tls=true&tlsCAFile=%2Fca.pem", db: "test",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			o, err := parseArgs(tc.args)
			require.NoError(t, err)

			uri, db, err := o.connectionURI(env(tc.env))
			require.NoError(t, err)
			assert.Equal(t, tc.uri, uri)
			assert.Equal(t, tc.db, db)
		})
	}

	// negative: an invalid URI, and --host with a URI
	for _, args := range [][]string{{"mongodb://[::1"}, {"mongodb://h/", "--host", "x"}} {
		o, err := parseArgs(args)
		require.NoError(t, err)

		_, _, err = o.connectionURI(env(nil))
		assert.Error(t, err, args)
	}

	assert.Equal(t, "mongodb://u:xxxxx@h:1/", redact("mongodb://u:secret@h:1/"))
}

func TestRewriteShellCommands(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `__shellCommand("use", "foo");`, rewriteShellCommands("use foo"))
	assert.Equal(t, `__shellCommand("show", "dbs");`, rewriteShellCommands("  show dbs;"))
	assert.Equal(t, `__shellCommand("it", "");`, rewriteShellCommands("it"))
	assert.Equal(t, "x = 1\n"+`__shellCommand("exit", "");`, rewriteShellCommands("x = 1\nexit"))

	// negative: JavaScript that only looks similar stays as it is
	for _, src := range []string{"use", "show", "user = 1", "it = 5", "exit(2)", "db.show.find()", "showDbs()"} {
		assert.Equal(t, src, rewriteShellCommands(src))
	}
}

func TestParseISODate(t *testing.T) {
	t.Parallel()

	for _, s := range []string{
		"2024-01-02T03:04:05Z", "2024-01-02T03:04:05.123Z", "2024-01-02T05:04:05+02:00",
		"2024-01-02T03:04:05", "2024-01-02",
	} {
		_, err := parseISODate(s)
		assert.NoError(t, err, s)
	}

	_, err := parseISODate("yesterday")
	assert.Error(t, err)
}

func TestValuesAndOutput(t *testing.T) {
	t.Parallel()

	for src, want := range map[string]string{
		`({a: 1, s: 'x', n: null, b: true})`:                  "{ a: 1, s: 'x', n: null, b: true }",
		`"plain string"`:                                      "plain string",
		`[1, 'a']`:                                            "[ 1, 'a' ]",
		`ObjectId('65a1b2c3d4e5f60718293a4b')`:                "ObjectId('65a1b2c3d4e5f60718293a4b')",
		`ISODate('2024-01-02T03:04:05Z')`:                     "ISODate('2024-01-02T03:04:05.000Z')",
		`NumberLong(5)`:                                       "Long('5')",
		`NumberLong('9007199254740993')`:                      "Long('9007199254740993')",
		`NumberInt(7)`:                                        "Int32(7)",
		`NumberDecimal('1.50')`:                               "Decimal128('1.50')",
		`UUID('0123456789abcdef0123456789abcdef')`:            "UUID('01234567-89ab-cdef-0123-456789abcdef')",
		`BinData(0, 'AAE=')`:                                  "Binary.createFromBase64('AAE=', 0)",
		`Timestamp({t: 1, i: 2})`:                             "Timestamp({ t: 1, i: 2 })",
		`({'we-ird': "it's"})`:                                `{ 'we-ird': "it's" }`,
		`MinKey()`:                                            "MinKey()",
		`NumberLong(5) + 1`:                                   "6",
		`ObjectId('65a1b2c3d4e5f60718293a4b').getTimestamp()`: "ISODate('2024-01-12T21:44:35.000Z')",
		`EJSON.stringify({a: NumberLong(1)})`:                 `{"a":1}`,
		`EJSON.stringify({a: NumberLong(1)}, null, null, {relaxed: false})`:                             `{"a":{"$numberLong":"1"}}`,
		`EJSON.parse('{"$oid": "65a1b2c3d4e5f60718293a4b"}')`:                                           "ObjectId('65a1b2c3d4e5f60718293a4b')",
		`x = {a: 1}; x.x = x; x`:                                                                        "{ a: 1, x: [Circular] }",
		`({acknowledged: true, insertedId: null, matchedCount: 3, modifiedCount: 3, upsertedCount: 0})`: "{\n  acknowledged: true,\n  insertedId: null,\n  matchedCount: 3,\n  modifiedCount: 3,\n  upsertedCount: 0\n}", //nolint:lll // one case
	} {
		out, errOut, code := nodb(t, "--eval", src)
		assert.Equal(t, 0, code, "%s: %s", src, errOut)
		assert.Equal(t, want+"\n", out, src)
	}

	out, _, code := nodb(t, "--eval", "print('a', 1, {b: 2}); printjson('q')")
	assert.Equal(t, 0, code)
	assert.Equal(t, "a 1 { b: 2 }\n'q'\n", out)

	out, _, code = nodb(t, "--eval", "({a: NumberLong(1), d: new Date(0)})", "--json=canonical")
	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"a":{"$numberLong":"1"},"d":{"$date":{"$numberLong":"0"}}}`, out)
}

func TestErrors(t *testing.T) {
	t.Parallel()

	for src, want := range map[string]string{
		"db.c.find({":             "SyntaxError: ",
		"throw new Error('boom')": "Error: boom",
		"require('fs')":           "ReferenceError: require is not defined",
		"ObjectId('xyz')":         "BSONError: Argument passed in must be a string of 24 hex characters",
		"NumberLong('abc')":       "BSONError: invalid 64-bit integer",
		"UUID('nope')":            "BSONError: UUID must be a 32 hex digit string",
		"ISODate('nope')":         "BSONError: \"nope\" is not a valid ISO-8601 date",
		"db.c.findOne()":          "MongoshInvalidInputError: not connected to a server",
	} {
		out, errOut, code := nodb(t, "--eval", src)
		assert.Equal(t, 1, code, src)
		assert.Empty(t, out, src)
		assert.Contains(t, errOut, want, src)
	}

	_, errOut, code := nodb(t, "--bogus")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "unknown option --bogus")

	out, _, code := nodb(t, "--eval", "quit(3)")
	assert.Equal(t, 3, code)
	assert.Empty(t, out)
}

func TestScriptFilesAndStdin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	f := filepath.Join(dir, "a.js")
	require.NoError(t, os.WriteFile(f, []byte("#!/usr/bin/env mongosh\nprint('from file', 1 + 1)\n"), 0o600))

	out, errOut, code := nodb(t, f)
	assert.Equal(t, 0, code, errOut)
	assert.Equal(t, "from file 2\n", out)

	out, _, code = nodb(t, "--eval", "load('"+f+"')")
	assert.Equal(t, 0, code)
	assert.Equal(t, "from file 2\ntrue\n", out)

	_, errOut, code = nodb(t, filepath.Join(dir, "missing.js"))
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "no such file")

	// statements from a pipe: each result is printed, a statement may span
	// lines, and an error makes the exit code 1
	var stdout, stderr bytes.Buffer
	code = Main(context.Background(), []string{"--nodb", "--quiet"}, IO{
		Stdin:  strings.NewReader("1 + 1\n[1,\n2]\nundefinedName\n'still running'\n"),
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: func(string) string { return "" },
	})
	assert.Equal(t, 1, code)
	assert.Equal(t, "2\n[ 1, 2 ]\nstill running\n", stdout.String())
	assert.Contains(t, stderr.String(), "ReferenceError: undefinedName is not defined")
}

func TestToBSON(t *testing.T) {
	t.Parallel()

	s := &Shell{ctx: context.Background(), stdout: new(bytes.Buffer), stderr: new(bytes.Buffer), dbName: "test"}
	s.setup()

	v, err := s.vm.RunString(`({i: 1, f: 1.5, big: 4294967296, neg0: -0, s: 's', l: NumberLong(5),
		d: new Date(0), oid: ObjectId('65a1b2c3d4e5f60718293a4b'), a: [1, undefined],
		u: undefined, fn: function () {}, r: /x/gi, n: null})`)
	require.NoError(t, err)

	want := bson.D{
		{Key: "i", Value: int32(1)},
		{Key: "f", Value: 1.5},
		{Key: "big", Value: float64(4294967296)},
		{Key: "neg0", Value: math.Copysign(0, -1)},
		{Key: "s", Value: "s"},
		{Key: "l", Value: int64(5)},
		{Key: "d", Value: primitive.DateTime(0)},
		{Key: "oid", Value: must(primitive.ObjectIDFromHex("65a1b2c3d4e5f60718293a4b"))},
		{Key: "a", Value: bson.A{int32(1), nil}},
		{Key: "r", Value: primitive.Regex{Pattern: "x", Options: "i"}},
		{Key: "n", Value: nil},
	}
	assert.Equal(t, want, s.toBSON(v))

	raw, err := bson.Marshal(want)
	require.NoError(t, err)

	back := s.toBSON(s.fromRaw(raw))
	assert.Equal(t, want, back, "BSON -> JavaScript -> BSON keeps every value and its type")
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}

	return v
}
