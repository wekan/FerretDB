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

// Package mongosh is a MongoDB-compatible shell built into the ferretdb
// binary: the goja JavaScript engine and the Go driver, with no Node.js and
// no C library, so that it runs in a static image.
//
// It implements the commonly used part of mongosh: --eval, script files,
// an interactive prompt, the db/collection/cursor methods, the BSON value
// constructors and mongosh's output format. Node.js modules (require) are
// not available.
package mongosh

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	mopts "go.mongodb.org/mongo-driver/mongo/options"

	"github.com/FerretDB/FerretDB/build/version"
	"github.com/FerretDB/FerretDB/internal/util/termline"
)

//go:embed prelude.js
var prelude string

// Shell is one shell session: a JavaScript runtime and a client.
type Shell struct {
	vm         *goja.Runtime
	client     *mongo.Client
	ctx        context.Context
	stdout     io.Writer
	stderr     io.Writer
	dbName     string
	lastCursor *cursor
	exitCode   int
	exiting    bool
}

// exitRequest interrupts the runtime when quit() or exit() is called.
type exitRequest struct{}

// IO is what the shell reads from and writes to, and its environment.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
}

// Version returns the version mongosh --version prints.
func Version() string {
	return strings.TrimPrefix(version.Get().Version, "v")
}

// Main runs the shell with the given arguments (without the program name)
// and returns the exit code.
func Main(ctx context.Context, args []string, sio IO) int {
	if sio.Getenv == nil {
		sio.Getenv = os.Getenv
	}

	o, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(sio.Stderr, "MongoshInvalidInputError: %s\n", err)
		fmt.Fprintln(sio.Stderr, "Run mongosh --help for the options.")

		return 1
	}

	if o.help {
		fmt.Fprint(sio.Stdout, usage)
		return 0
	}

	if o.version {
		fmt.Fprintln(sio.Stdout, Version())
		return 0
	}

	s := &Shell{ctx: ctx, stdout: sio.Stdout, stderr: sio.Stderr, dbName: "test"}

	lines := termline.New(sio.Stdin, sio.Stdout)
	interactive := (len(o.evals) == 0 && len(o.files) == 0) || o.shell

	if !o.nodb {
		uri, db, err := o.connectionURI(sio.Getenv)
		if err != nil {
			fmt.Fprintf(sio.Stderr, "MongoParseError: %s\n", err)
			return 1
		}

		s.dbName = db

		copts := mopts.Client().ApplyURI(uri)

		if o.username != "" {
			if o.passwordPrompt || o.password == "" {
				if !lines.Terminal() {
					fmt.Fprintln(sio.Stderr, "MongoshInvalidInputError: a password is required (-p <password>)")
					return 1
				}

				if o.password, err = lines.ReadPassword("Enter password: "); err != nil {
					return 1
				}
			}

			copts.SetAuth(mopts.Credential{
				Username:      o.username,
				Password:      o.password,
				AuthSource:    o.authDB,
				AuthMechanism: o.authMechanism,
			})
		}

		if !o.quiet {
			fmt.Fprintf(sio.Stdout, "Connecting to:\t\t%s\n", redact(uri))
		}

		if s.client, err = mongo.Connect(ctx, copts); err != nil {
			fmt.Fprintf(sio.Stderr, "MongoParseError: %s\n", err)
			return 1
		}

		defer s.client.Disconnect(context.Background()) //nolint:errcheck // exiting anyway

		var build bson.M

		err = s.client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&build)
		if err != nil {
			fmt.Fprintf(sio.Stderr, "%s: %s\n", errorName(err), err)
			return 1
		}

		if !o.quiet {
			fmt.Fprintf(sio.Stdout, "Using MongoDB:\t\t%v\n", build["version"])
			fmt.Fprintf(sio.Stdout, "Using Mongosh:\t\t%s (built into FerretDB; JavaScript without Node.js modules)\n", Version())

			if interactive {
				fmt.Fprintln(sio.Stdout, "\nType \"help\" for help.")
			}
		}
	}

	s.setup()

	for i, e := range o.evals {
		v, err := s.run("[eval]", e)
		if s.exiting {
			return s.exitCode
		}

		if err != nil {
			s.printError(err)
			return 1
		}

		if i != len(o.evals)-1 {
			continue
		}

		if o.json != "" {
			out, err := s.extJSON(v, o.json == "canonical", true)
			if err != nil {
				s.printError(err)
				return 1
			}

			fmt.Fprintln(sio.Stdout, out)

			continue
		}

		if v != nil && !goja.IsUndefined(v) {
			fmt.Fprintln(sio.Stdout, s.inspect(v))
		}
	}

	for _, f := range o.files {
		if err := s.load(f); err != nil {
			if s.exiting {
				return s.exitCode
			}

			s.printError(err)

			return 1
		}

		if s.exiting {
			return s.exitCode
		}
	}

	if !interactive {
		return 0
	}

	return s.repl(lines)
}

// errorName names a driver error the way mongosh does.
func errorName(err error) string {
	var cmdErr mongo.CommandError

	switch {
	case errors.As(err, &cmdErr), strings.Contains(err.Error(), "auth error"):
		return "MongoServerError"
	case mongo.IsTimeout(err) || strings.Contains(err.Error(), "server selection"):
		return "MongoServerSelectionError"
	case mongo.IsNetworkError(err):
		return "MongoNetworkError"
	default:
		return "MongoError"
	}
}

// setup creates the runtime and the global objects.
func (s *Shell) setup() {
	vm := goja.New()
	s.vm = vm

	_ = vm.Set("__native", s.native())
	if _, err := vm.RunString(prelude); err != nil {
		panic(err)
	}

	_ = vm.GlobalObject().Delete("__native")

	s.setDB(s.dbName)
	_ = vm.Set("rs", s.rs())

	printArgs := func(w io.Writer) func(goja.FunctionCall) goja.Value {
		return func(c goja.FunctionCall) goja.Value {
			parts := make([]string, len(c.Arguments))
			for i, a := range c.Arguments {
				parts[i] = s.inspect(a)
			}

			fmt.Fprintln(w, strings.Join(parts, " "))

			return goja.Undefined()
		}
	}

	_ = vm.Set("print", printArgs(s.stdout))
	_ = vm.Set("printjson", func(c goja.FunctionCall) goja.Value {
		fmt.Fprintln(s.stdout, s.inspectValue(c.Argument(0)))
		return goja.Undefined()
	})
	_ = vm.Set("tojson", func(c goja.FunctionCall) goja.Value { return vm.ToValue(s.inspectValue(c.Argument(0))) })

	console := vm.NewObject()
	_ = console.Set("log", printArgs(s.stdout))
	_ = console.Set("info", printArgs(s.stdout))
	_ = console.Set("warn", printArgs(s.stderr))
	_ = console.Set("error", printArgs(s.stderr))
	_ = vm.Set("console", console)

	quit := func(c goja.FunctionCall) goja.Value {
		s.exitCode = int(c.Argument(0).ToInteger())
		s.exiting = true
		vm.Interrupt(exitRequest{})

		return goja.Undefined()
	}
	_ = vm.Set("quit", quit)
	_ = vm.Set("exit", quit)

	_ = vm.Set("sleep", func(c goja.FunctionCall) goja.Value {
		select {
		case <-time.After(time.Duration(c.Argument(0).ToInteger()) * time.Millisecond):
		case <-s.ctx.Done():
		}

		return goja.Undefined()
	})
	_ = vm.Set("version", func(goja.FunctionCall) goja.Value { return vm.ToValue(Version()) })
	_ = vm.Set("load", func(c goja.FunctionCall) goja.Value {
		if err := s.load(c.Argument(0).String()); err != nil {
			var ex *goja.Exception
			if errors.As(err, &ex) {
				panic(ex.Value())
			}

			s.throw("MongoshInvalidInputError", err.Error())
		}

		return vm.ToValue(true)
	})
	_ = vm.Set("help", func(goja.FunctionCall) goja.Value {
		fmt.Fprint(s.stdout, helpText)
		return goja.Undefined()
	})
	_ = vm.Set("__shellCommand", func(c goja.FunctionCall) goja.Value {
		return s.shellCommand(c.Argument(0).String(), c.Argument(1).String())
	})

	ejson := vm.NewObject()
	_ = ejson.Set("stringify", func(c goja.FunctionCall) goja.Value {
		relaxed := true
		if o := optDoc(c.Argument(3)); o != nil && optValue(o, "relaxed") != nil {
			relaxed = optBool(o, "relaxed")
		}

		indent := c.Argument(2)

		out, err := s.extJSON(c.Argument(0), !relaxed, !goja.IsUndefined(indent) && !goja.IsNull(indent))
		if err != nil {
			s.throw("BSONError", err.Error())
		}

		return vm.ToValue(out)
	})
	_ = ejson.Set("parse", func(c goja.FunctionCall) goja.Value {
		var raw bson.Raw
		if err := bson.UnmarshalExtJSON([]byte(`{"v":`+c.Argument(0).String()+`}`), false, &raw); err != nil {
			s.throw("BSONError", err.Error())
		}

		return s.fromRawValue(raw.Lookup("v"))
	})
	_ = vm.Set("EJSON", ejson)
}

// setDB makes name the current database.
func (s *Shell) setDB(name string) {
	s.dbName = name
	_ = s.vm.Set("db", s.newDB(name))
}

// helpText is printed by help.
const helpText = `  Shell commands:
    use <db>                  switch the current database
    show dbs                  list databases
    show collections          list collections of the current database
    show users                list users of the current database
    it                        show more results of the last cursor
    exit, quit                exit the shell

  Database (db): getName, getSiblingDB, getCollection, getCollectionNames,
    getCollectionInfos, createCollection, runCommand, adminCommand, hello,
    isMaster, stats, serverStatus, version, dropDatabase, createUser, dropUser,
    getUsers, aggregate
  Collection (db.<name>): find, findOne, insertOne, insertMany, updateOne,
    updateMany, replaceOne, deleteOne, deleteMany, findOneAndUpdate,
    findOneAndReplace, findOneAndDelete, countDocuments,
    estimatedDocumentCount, count, distinct, aggregate, createIndex,
    createIndexes, getIndexes, dropIndex, dropIndexes, drop, stats,
    renameCollection
  Cursor: limit, skip, sort, projection, batchSize, hint, toArray, forEach,
    map, hasNext, next, count, size, itcount, pretty, explain, close
  Replica set (rs): status, conf, initiate, hello
  Values: ObjectId, ISODate, Date, NumberLong/Long, NumberInt/Int32, Double,
    NumberDecimal/Decimal128, UUID, BinData, Binary, Timestamp, MinKey, MaxKey
  Functions: print, printjson, EJSON.stringify, EJSON.parse, load, sleep,
    version, quit, exit

  This shell has no Node.js modules (require is not available).
`

// shellCmdRe matches the mongosh commands that are not JavaScript.
var shellCmdRe = regexp.MustCompile(`^\s*(use|show|it|exit|quit|help)(?:\s+([^\s;]+))?\s*;?\s*$`)

// rewriteShellCommands turns lines that are shell commands into calls.
func rewriteShellCommands(src string) string {
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		m := shellCmdRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}

		if m[1] != "use" && m[1] != "show" && m[2] != "" {
			continue
		}

		if (m[1] == "use" || m[1] == "show") && m[2] == "" {
			continue
		}

		b, _ := json.Marshal(m[2])
		lines[i] = fmt.Sprintf("__shellCommand(%q, %s);", m[1], b)
	}

	return strings.Join(lines, "\n")
}

// shellCommand runs use, show, it, exit, quit and help.
func (s *Shell) shellCommand(cmd, arg string) goja.Value {
	switch cmd {
	case "use":
		s.setDB(arg)
		return s.vm.ToValue("switched to db " + arg)
	case "it":
		if s.lastCursor == nil {
			return s.vm.ToValue("no cursor")
		}

		return s.vm.ToValue(s.cursorString(s.lastCursor, true))
	case "exit", "quit":
		s.exiting = true
		s.vm.Interrupt(exitRequest{})

		return goja.Undefined()
	case "help":
		fmt.Fprint(s.stdout, helpText)
		return goja.Undefined()
	}

	switch arg {
	case "dbs", "databases":
		res, err := s.needClient().ListDatabases(s.ctx, bson.D{})
		if err != nil {
			s.throwErr(err)
		}

		width := 0
		for _, d := range res.Databases {
			width = max(width, len(d.Name))
		}

		var b strings.Builder
		for i, d := range res.Databases {
			if i > 0 {
				b.WriteByte('\n')
			}

			fmt.Fprintf(&b, "%-*s  %s", width, d.Name, humanSize(d.SizeOnDisk))
		}

		return s.vm.ToValue(b.String())
	case "collections", "tables":
		names, err := s.needClient().Database(s.dbName).ListCollectionNames(s.ctx, bson.D{})
		if err != nil {
			s.throwErr(err)
		}

		if len(names) == 0 {
			return goja.Undefined()
		}

		sort.Strings(names)

		return s.vm.ToValue(strings.Join(names, "\n"))
	case "users":
		return s.runCommandDoc(s.dbName, bson.D{{Key: "usersInfo", Value: int32(1)}}).(*goja.Object).Get("users")
	}

	s.throw("MongoshInvalidInputError", "'"+arg+"' is not a valid argument for \"show\".")

	return nil
}

// humanSize formats a size as mongosh does: 40.00 KiB.
func humanSize(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f := float64(n)

	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}

	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}

	return fmt.Sprintf("%.2f %s", f, units[i])
}

// run compiles and runs source, returning the value of its last expression.
func (s *Shell) run(name, src string) (goja.Value, error) {
	prg, err := goja.Compile(name, rewriteShellCommands(src), false)
	if err != nil {
		return nil, err
	}

	v, err := s.vm.RunProgram(prg)

	var ie *goja.InterruptedError
	if errors.As(err, &ie) {
		s.vm.ClearInterrupt()

		if _, ok := ie.Value().(exitRequest); ok {
			return nil, nil
		}
	}

	return v, err
}

// load runs a script file.
func (s *Shell) load(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	src := string(b)
	if strings.HasPrefix(src, "#!") {
		src = "//" + src
	}

	_, err = s.run(path, src)

	return err
}

// printError prints an uncaught error as mongosh does.
func (s *Shell) printError(err error) {
	var ex *goja.Exception
	var syn *goja.CompilerSyntaxError
	var ie *goja.InterruptedError

	switch {
	case errors.As(err, &ex):
		v := ex.Value()
		if obj, ok := v.(*goja.Object); ok && obj.ClassName() == "Error" {
			fmt.Fprintln(s.stderr, obj.Get("name").String()+": "+obj.Get("message").String())
			return
		}

		fmt.Fprintln(s.stderr, "Uncaught "+s.inspectValue(v))
	case errors.As(err, &syn):
		fmt.Fprintln(s.stderr, "SyntaxError: "+syn.Message)
	case errors.As(err, &ie):
		fmt.Fprintln(s.stderr, "Interrupted")
	default:
		fmt.Fprintln(s.stderr, "Error: "+err.Error())
	}
}

// extJSON returns a value as Extended JSON.
func (s *Shell) extJSON(v goja.Value, canonical, indent bool) (string, error) {
	if obj, ok := v.(*goja.Object); ok {
		if c := cursorOf(obj); c != nil {
			v = s.vm.NewArray(c.all()...)
		}
	}

	val := s.toBSON(v)
	if _, skip := val.(skipValue); skip {
		val = nil
	}

	b, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: val}}, canonical, false)
	if err != nil {
		return "", err
	}

	var wrapper map[string]json.RawMessage
	if err = json.Unmarshal(b, &wrapper); err != nil {
		return "", err
	}

	out := []byte(wrapper["v"])

	if indent {
		var buf strings.Builder

		var tmp json.RawMessage = out

		pretty, err := json.MarshalIndent(tmp, "", "  ")
		if err != nil {
			return "", err
		}

		buf.Write(pretty)

		return buf.String(), nil
	}

	return string(out), nil
}

// incomplete reports whether a compile error means more input is needed.
func incomplete(err error) bool {
	var syn *goja.CompilerSyntaxError
	if !errors.As(err, &syn) {
		return false
	}

	return strings.Contains(syn.Message, "Unexpected end of input") ||
		strings.Contains(syn.Message, "Unterminated template")
}

// repl runs the interactive prompt; without a terminal it reads statements
// from the input in the same way, without prompts.
func (s *Shell) repl(lines *termline.Reader) int {
	failed := false
	base := s.ctx

	for !s.exiting {
		var src string

		prompt := s.dbName + "> "

		for {
			line, err := lines.ReadLine(prompt)
			if errors.Is(err, termline.ErrInterrupt) {
				src = ""
				prompt = s.dbName + "> "

				continue
			}

			if err != nil {
				if src == "" {
					if failed && !lines.Terminal() {
						return 1
					}

					return s.exitCode
				}

				break
			}

			if src == "" {
				src = line
			} else {
				src += "\n" + line
			}

			if strings.TrimSpace(src) == "" {
				src = ""
				continue
			}

			if _, err := goja.Compile("", rewriteShellCommands(src), false); err != nil && incomplete(err) {
				prompt = "... "
				continue
			}

			break
		}

		if strings.TrimSpace(src) == "" {
			continue
		}

		ctx, cancel := context.WithCancel(base)
		s.ctx = ctx

		var sig chan os.Signal
		if lines.Terminal() {
			sig = make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt)

			go func() {
				if _, ok := <-sig; ok {
					cancel()
					s.vm.Interrupt("interrupted")
				}
			}()
		}

		v, err := s.run("REPL", src)

		if sig != nil {
			signal.Stop(sig)
			close(sig)
		}

		cancel()
		s.ctx = base

		if s.exiting {
			break
		}

		if err != nil {
			var ie *goja.InterruptedError
			if errors.As(err, &ie) {
				s.vm.ClearInterrupt()
			}

			failed = true

			s.printError(err)

			continue
		}

		if v != nil && !goja.IsUndefined(v) {
			fmt.Fprintln(s.stdout, s.inspect(v))
		}
	}

	return s.exitCode
}
