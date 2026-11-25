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

package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reexecEnv makes the test binary run main() instead of the tests, so that
// the tests can start it as the server and under the names mongosh and sh,
// exactly as the image's links do.
const reexecEnv = "FERRETDB_MULTICALL_TEST_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(reexecEnv) == "1" {
		main()
		os.Exit(0)
	}

	os.Exit(m.Run())
}

func TestSelectTool(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		args []string
		tool string
		rest []string
	}{
		{[]string{"/usr/bin/mongosh", "--eval", "1"}, "mongosh", []string{"--eval", "1"}},
		{[]string{"mongosh"}, "mongosh", []string{}},
		{[]string{"/bin/sh", "-c", "true"}, "sh", []string{"-c", "true"}},
		{[]string{"/bin/bash"}, "bash", []string{}},
		{[]string{"mongosh.exe", "x"}, "mongosh", []string{"x"}},
		{[]string{"/ferretdb", "mongosh", "--quiet"}, "mongosh", []string{"--quiet"}},
		{[]string{"ferretdb", "sh", "-c", "ls"}, "sh", []string{"-c", "ls"}},
		{[]string{"ferretdb", "bash"}, "bash", []string{}},

		// negative: the server, its own commands, and look-alike names
		{[]string{"/ferretdb"}, "", nil},
		{[]string{"/ferretdb", "--version"}, "", nil},
		{[]string{"ferretdb", "ping"}, "", nil},
		{[]string{"ferretdb", "--handler=sqlite", "mongosh"}, "", nil},
		{[]string{"/usr/bin/mongoshx"}, "", nil},
		{[]string{"zsh", "-c", "true"}, "", nil},
		{[]string{"ferretdb", "shx"}, "", nil},
		{[]string{"ferretdb", "MONGOSH"}, "", nil},
		{nil, "", nil},
	} {
		tool, rest := selectTool(tc.args)
		assert.Equal(t, tc.tool, tool, "%q", tc.args)
		assert.Equal(t, tc.rest, rest, "%q", tc.args)
	}
}

// result is what a re-executed binary did.
type result struct {
	stdout, stderr string
	code           int
}

// runAs runs this test binary as argv0 (the name the image's link has).
func runAs(t *testing.T, argv0 string, stdin string, env []string, args ...string) result {
	t.Helper()

	cmd := exec.Command(os.Args[0], args...)
	cmd.Args[0] = argv0
	cmd.Env = append(append(os.Environ(), reexecEnv+"=1"), env...)
	cmd.Stdin = strings.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()

	code := 0
	if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec returns it unwrapped
		code = ee.ExitCode()
	} else {
		require.NoError(t, err)
	}

	return result{stdout.String(), stderr.String(), code}
}

// startServer starts this binary as a FerretDB server with the sqlite
// handler in a temporary directory and returns its address.
func startServer(t *testing.T, args ...string) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := l.Addr().String()
	require.NoError(t, l.Close())

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "data"), 0o700))

	cmd := exec.Command(os.Args[0], append([]string{
		"--handler=sqlite",
		"--sqlite-url=file:" + filepath.Join(dir, "data") + "/",
		"--state-dir=" + dir,
		"--listen-addr=" + addr,
		"--debug-addr=-",
		"--log-level=error",
	}, args...)...)
	cmd.Args[0] = "ferretdb"
	cmd.Env = append(os.Environ(), reexecEnv+"=1")

	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs

	require.NoError(t, cmd.Start())

	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)

		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}

		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})

	deadline := time.Now().Add(30 * time.Second)

	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = c.Close()
			return addr
		}

		require.True(t, time.Now().Before(deadline), "the server did not start")
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMongosh(t *testing.T) {
	t.Parallel()

	addr := startServer(t)
	uri := "mongodb://" + addr + "/shelltest"

	mongosh := func(stdin string, args ...string) result {
		return runAs(t, "/usr/bin/mongosh", stdin, nil, append([]string{"--quiet", uri}, args...)...)
	}

	eval := func(js string) string {
		t.Helper()

		r := mongosh("", "--eval", js)
		require.Equal(t, 0, r.code, "%s\n%s", js, r.stderr)

		return r.stdout
	}

	assert.Equal(t, "{ acknowledged: true, insertedId: 1 }\n", eval(`db.c.insertOne({_id: 1, a: 1, s: 'x'})`))
	assert.Equal(t, "{ acknowledged: true, insertedIds: { '0': 2, '1': 3 } }\n",
		eval(`db.c.insertMany([{_id: 2, a: 2}, {_id: 3, a: 3, d: ISODate('2024-01-02T03:04:05Z'), l: NumberLong(5)}])`))
	assert.Equal(t, "{ _id: 1, a: 1, s: 'x' }\n", eval(`db.c.findOne({a: 1})`))
	assert.Equal(t, "{ _id: 3, a: 3, d: ISODate('2024-01-02T03:04:05.000Z'), l: Long('5') }\n",
		eval(`db.c.findOne({_id: 3})`))
	assert.Equal(t, "3\n", eval(`db.c.countDocuments({})`))
	assert.Equal(t, "[ { a: 3 }, { a: 2 } ]\n", eval(`db.c.find({}, {_id: 0, a: 1}).sort({a: -1}).limit(2)`))
	assert.Equal(t, "[ { a: 2 } ]\n", eval(`db.c.find({}, {_id: 0, a: 1}).sort({a: 1}).skip(1).limit(1).toArray()`))
	assert.Equal(t, "1\n2\n3\n", eval(`db.c.find().sort({_id: 1}).forEach(d => print(d._id))`))
	assert.Equal(t, "3\n", eval(`db.c.find().count()`))
	assert.Equal(t,
		"{\n  acknowledged: true,\n  insertedId: null,\n  matchedCount: 1,\n  modifiedCount: 1,\n  upsertedCount: 0\n}\n",
		eval(`db.c.updateOne({_id: 1}, {$set: {b: true}})`),
	)
	assert.Contains(t, eval(`db.c.updateMany({}, {$inc: {a: 10}})`), "matchedCount: 3")
	assert.Equal(t, "[ { _id: null, total: 36 } ]\n", eval(`db.c.aggregate([{$group: {_id: null, total: {$sum: '$a'}}}])`))
	assert.Equal(t, "a_1\n", eval(`db.c.createIndex({a: 1})`))
	assert.Equal(t, "[ '_id_', 'a_1' ]\n", eval(`db.c.getIndexes().map(i => i.name)`))
	assert.Equal(t, "[ 'c' ]\n", eval(`db.getCollectionNames()`))
	assert.Equal(t, "c\n", eval(`show collections`))
	assert.Contains(t, eval(`show dbs`), "shelltest")
	assert.Equal(t, "other\n", eval("use other\ndb.getName()"))
	assert.Equal(t, "shelltest.c\n", eval(`db.getSiblingDB('shelltest').getCollection('c')`))
	assert.Equal(t, "true\n", eval(`db.hello().isWritablePrimary`))
	assert.Equal(t, "{ ok: 1 }\n", eval(`db.runCommand({ping: 1})`))
	assert.Equal(t, "1\n", eval(`db.adminCommand('ping').ok`))
	assert.Equal(t, "{ acknowledged: true, deletedCount: 1 }\n", eval(`db.c.deleteOne({_id: 1})`))
	assert.Equal(t, "{ acknowledged: true, deletedCount: 2 }\n", eval(`db.c.deleteMany({})`))
	assert.Equal(t, "true\n", eval(`db.c.drop()`))

	r := mongosh("", "--json", "--eval", `db.j.insertOne({_id: 1, l: NumberLong(2)}); db.j.find()`)
	assert.Equal(t, 0, r.code, r.stderr)
	assert.JSONEq(t, `[{"_id": 1, "l": 2}]`, r.stdout)

	// statements from standard input, as `docker exec -i ... mongosh < x`
	r = mongosh("db.p.insertOne({_id: 'a'}).acknowledged\ndb.p.countDocuments()\n")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, "true\n1\n", r.stdout)

	// a script file
	script := filepath.Join(t.TempDir(), "s.js")
	require.NoError(t, os.WriteFile(script, []byte("print('docs:', db.p.countDocuments())\n"), 0o600))
	r = mongosh("", script)
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, "docs: 1\n", r.stdout)

	// rs.status() without a replica set is the server's error, exit code 1
	r = mongosh("", "--eval", "rs.status()")
	assert.Equal(t, 1, r.code)
	assert.Contains(t, r.stderr, "MongoServerError:")

	// negative: the server's error for an unknown command
	r = mongosh("", "--eval", "db.runCommand({noSuchCommand: 1})")
	assert.Equal(t, 1, r.code)
	assert.Equal(t, "", r.stdout)
	assert.Contains(t, r.stderr, "MongoServerError: no such command: 'noSuchCommand'")

	// negative: a duplicate key is a server error with its code
	assert.Equal(t, "MongoServerError 11000\n",
		eval(`try { db.p.insertOne({_id: 'a'}) } catch (e) { print(e.name, e.code) }`))
}

func TestMongoshDefaultConnection(t *testing.T) {
	t.Parallel()

	addr := startServer(t)
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	// A bare mongosh connects to the server's own listen address, with the
	// wildcard address (as in the image) replaced by the loopback one.
	env := []string{"FERRETDB_LISTEN_ADDR=0.0.0.0:" + port}

	r := runAs(t, "mongosh", "", env, "--eval", "db.runCommand({ping: 1}).ok")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Contains(t, r.stdout, "Connecting to:\t\tmongodb://127.0.0.1:"+port+"/test")
	assert.True(t, strings.HasSuffix(r.stdout, "\n1\n"), r.stdout)

	// the same as a subcommand of ferretdb, and from the built-in shell
	r = runAs(t, "/ferretdb", "", env, "mongosh", "--quiet", "--eval", "db.getName()")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, "test\n", r.stdout)

	r = runAs(t, "/bin/sh", "", env, "-c", "mongosh --quiet --eval 'db.hello().ok' && /usr/bin/mongosh --quiet mydb --eval 'db'")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, "1\nmydb\n", r.stdout)

	r = runAs(t, "/bin/bash", "", env, "-c", "ferretdb mongosh --quiet --eval 1+1; ferretdb sh -c 'echo nested'")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, "2\nnested\n", r.stdout)
}

func TestMongoshAuth(t *testing.T) {
	t.Parallel()

	addr := startServer(t,
		"--test-enable-new-auth", "--setup-database=admin", "--setup-username=user", "--setup-password=secret",
	)
	uri := "mongodb://" + addr + "/"

	r := runAs(t, "mongosh", "", nil, uri, "--quiet", "-u", "user", "-p", "secret", "--eval", "db.runCommand({ping: 1}).ok")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, "1\n", r.stdout)

	// negative: wrong credentials, no credentials, and no password without a terminal
	r = runAs(t, "mongosh", "", nil, uri, "--quiet", "-u", "user", "-p", "wrong", "--eval", "1")
	assert.Equal(t, 1, r.code)
	assert.Contains(t, r.stderr, "MongoServerError:")
	assert.Contains(t, r.stderr, "Authentication failed")
	assert.NotContains(t, r.stderr+r.stdout, "wrong", "the password is not printed")

	r = runAs(t, "mongosh", "", nil, uri, "--quiet", "--eval", "db.c.find().toArray()")
	assert.Equal(t, 1, r.code)
	assert.Contains(t, r.stderr, "requires authentication")

	r = runAs(t, "mongosh", "", nil, uri, "--quiet", "-u", "user", "--eval", "1")
	assert.Equal(t, 1, r.code)
	assert.Contains(t, r.stderr, "a password is required")

	// the banner never shows the password of a URI
	r = runAs(t, "mongosh", "", nil, "mongodb://user:secret@"+addr+"/", "--eval", "1")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.NotContains(t, r.stdout, "secret")
	assert.Contains(t, r.stdout, "mongodb://user:xxxxx@")
}

func TestMongoshNegative(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		args []string
		err  string
	}{
		"bad URI":        {[]string{"mongodb://[::1", "--eval", "1"}, "MongoParseError:"},
		"unknown option": {[]string{"--no-such-option"}, "unknown option --no-such-option"},
		"unreachable": {
			[]string{"mongodb://127.0.0.1:1/?serverSelectionTimeoutMS=300", "--quiet", "--eval", "1"},
			"MongoServerSelectionError:",
		},
		"syntax error": {[]string{"--nodb", "--eval", "db.c.find({"}, "SyntaxError:"},
	} {
		r := runAs(t, "mongosh", "", nil, tc.args...)
		assert.Equal(t, 1, r.code, name)
		assert.Contains(t, r.stderr, tc.err, name)
	}

	r := runAs(t, "mongosh", "", nil, "--version")
	assert.Equal(t, 0, r.code)
	assert.NotEmpty(t, strings.TrimSpace(r.stdout))
}

func TestShell(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	r := runAs(t, "/bin/sh", "", nil, "-c", "cd "+dir+" && echo hi > f && cat f && ls && wc -c f")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, "hi\nf\n3 f\n", r.stdout)

	r = runAs(t, "bash", "echo from stdin; exit 3\n", nil)
	assert.Equal(t, 3, r.code)
	assert.Equal(t, "from stdin\n", r.stdout)

	// negative: anything else is not available, with status 127
	r = runAs(t, "sh", "", nil, "-c", "apt-get update")
	assert.Equal(t, 127, r.code)
	assert.Equal(t, "apt-get: not available in this image\n", r.stderr)

	// the ferretdb built-in runs this binary for its own commands
	r = runAs(t, "sh", "", nil, "-c", "ferretdb --version")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Contains(t, r.stdout, "version:")

	r = runAs(t, "/ferretdb", "", nil, "sh", "-c", "echo subcommand")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, "subcommand\n", r.stdout)

	r = runAs(t, "/ferretdb", "", nil, "--log-level=error", "sh", "-c", "echo after a flag")
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, "after a flag\n", r.stdout)
}
