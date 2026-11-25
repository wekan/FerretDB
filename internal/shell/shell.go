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

// Package shell is a POSIX shell built into the ferretdb binary, for images
// that have no other programs: the mvdan.cc/sh interpreter with the commonly
// used commands (ls, cat, grep, ps, df, ...) built in.
//
// It never runs another program: a command that is neither a shell builtin
// nor one of the commands here prints "<name>: not available in this image"
// and exits with status 127. Files are only touched with the permissions of
// the user the shell runs as.
package shell

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"

	"github.com/FerretDB/FerretDB/internal/util/termline"
)

// Env is what a command runs with.
type Env struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Dir     string   // current directory
	Environ []string // exported variables, NAME=value
}

// Getenv returns an exported variable.
func (e *Env) Getenv(name string) string {
	for i := len(e.Environ) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(e.Environ[i], "="); ok && k == name {
			return v
		}
	}

	return ""
}

// Path resolves p against the current directory.
func (e *Env) Path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}

	return filepath.Join(e.Dir, p)
}

// Command is a built-in command; it returns the exit status.
type Command func(ctx context.Context, e *Env, args []string) int

// Options configure a shell.
type Options struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Environ is the initial environment, NAME=value; os.Environ() if nil.
	Environ []string

	// Dir is the initial directory; the process' current directory if empty.
	Dir string

	// Commands are additional built-in commands (such as mongosh), by name.
	Commands map[string]Command
}

// Names returns the names of the built-in commands (not counting the
// interpreter's own builtins such as echo, cd and test), sorted.
func Names(extra map[string]Command) []string {
	var names []string
	for n := range builtins {
		names = append(names, n)
	}

	for n := range extra {
		names = append(names, n)
	}

	names = append(names, "sh", "bash")
	sort.Strings(names)

	return names
}

// shell is the state shared by a shell and the shells it starts.
type shell struct {
	opts     *Options
	commands map[string]Command
}

// Main runs the shell named name (sh or bash) with arguments (without the
// program name) and returns the exit status.
func Main(ctx context.Context, name string, args []string, opts *Options) int {
	if opts.Environ == nil {
		opts.Environ = os.Environ()
	}

	if opts.Dir == "" {
		opts.Dir, _ = os.Getwd()
	}

	sh := &shell{opts: opts, commands: map[string]Command{}}
	for n, c := range builtins {
		sh.commands[n] = c
	}

	for n, c := range opts.Commands {
		sh.commands[n] = c
	}

	self := func(ctx context.Context, e *Env, args []string) int {
		sub := &Options{
			Stdin: e.Stdin, Stdout: e.Stdout, Stderr: e.Stderr,
			Environ: e.Environ, Dir: e.Dir, Commands: opts.Commands,
		}

		return Main(ctx, path.Base(args[0]), args[1:], sub)
	}
	sh.commands["sh"] = self
	sh.commands["bash"] = self

	return sh.main(ctx, name, args)
}

// usage is printed by --help.
const usage = `Usage: %[1]s [-eux] [-o option] [-c command [name [arg ...]] | script [arg ...]]

A POSIX shell built into FerretDB (mvdan.cc/sh). Without -c or a script it
reads commands from standard input, with a prompt on a terminal.

Built-in commands, besides the shell's own (echo, printf, test, [, cd, pwd,
read, export, set, exit, ...):
  %[2]s

Any other command is not available in this image.
`

// main parses the shell arguments and runs it.
func (sh *shell) main(ctx context.Context, name string, args []string) int {
	var params []string

	var command, script string

	hasCommand, interactive := false, false

	i := 0

flags:
	for ; i < len(args); i++ {
		a := args[i]

		switch {
		case a == "--help":
			fmt.Fprintf(sh.opts.Stdout, usage, name, strings.Join(Names(sh.opts.Commands), " "))
			return 0
		case a == "--version":
			fmt.Fprintf(sh.opts.Stdout, "%s (built into FerretDB, mvdan.cc/sh)\n", name)
			return 0
		case a == "--login" || a == "--norc" || a == "--noprofile" || a == "--posix":
		case a == "--":
			i++
			break flags
		case a == "-o" || a == "+o":
			if i+1 >= len(args) {
				fmt.Fprintf(sh.opts.Stderr, "%s: %s: option requires an argument\n", name, a)
				return 2
			}

			params = append(params, a, args[i+1])
			i++
		case len(a) > 1 && (a[0] == '-' || a[0] == '+') && !strings.HasPrefix(a, "--"):
			for _, f := range a[1:] {
				switch f {
				case 'c':
					hasCommand = true
				case 'i':
					interactive = true
				case 's', 'l':
				default:
					params = append(params, string(a[0])+string(f))
				}
			}
		default:
			break flags
		}
	}

	rest := args[i:]

	if hasCommand {
		if len(rest) == 0 {
			fmt.Fprintf(sh.opts.Stderr, "%s: -c: option requires an argument\n", name)
			return 2
		}

		command, rest = rest[0], rest[1:]
		script = name

		if len(rest) > 0 {
			script, rest = rest[0], rest[1:]
		}
	} else if len(rest) > 0 && !interactive {
		script, rest = rest[0], rest[1:]
	}

	params = append(params, "--")
	params = append(params, rest...)

	// Commands read from standard input are parsed from it; the commands
	// themselves then get it only when it is a file (and so can be shared),
	// not a reader the interpreter would copy concurrently into a pipe.
	scriptFromStdin := !hasCommand && script == "" && !interactive && !termline.IsTerminal(sh.opts.Stdin)

	stdin := sh.opts.Stdin
	if _, isFile := stdin.(*os.File); scriptFromStdin && !isFile {
		stdin = strings.NewReader("")
	}

	r, err := interp.New(
		interp.StdIO(stdin, sh.opts.Stdout, sh.opts.Stderr),
		interp.Env(expand.ListEnviron(sh.opts.Environ...)),
		interp.Dir(sh.opts.Dir),
		interp.Params(params...),
		interp.Interactive(interactive || (!hasCommand && script == "" && termline.IsTerminal(sh.opts.Stdin))),
		interp.ExecHandlers(sh.execMiddleware),
		interp.CallHandler(callHandler),
	)
	if err != nil {
		fmt.Fprintf(sh.opts.Stderr, "%s: %s\n", name, err)
		return 2
	}

	switch {
	case hasCommand:
		return sh.runSource(ctx, r, strings.NewReader(command), script)
	case script != "":
		f, err := os.Open(filepath.Join(sh.opts.Dir, script))
		if filepath.IsAbs(script) {
			f, err = os.Open(script)
		}

		if err != nil {
			fmt.Fprintf(sh.opts.Stderr, "%s: %s: %s\n", name, script, cleanErr(err))
			return 127
		}
		defer f.Close() //nolint:errcheck // read-only

		return sh.runSource(ctx, r, f, script)
	case interactive || termline.IsTerminal(sh.opts.Stdin):
		return sh.interactive(ctx, r, name)
	default:
		return sh.runSource(ctx, r, sh.opts.Stdin, name)
	}
}

// exitCode converts a Run error to an exit status.
func exitCode(err error, stderr io.Writer, name string) int {
	if err == nil {
		return 0
	}

	var es interp.ExitStatus
	if errors.As(err, &es) {
		return int(es)
	}

	fmt.Fprintf(stderr, "%s: %s\n", name, err)

	return 1
}

// runSource parses and runs a whole script.
func (sh *shell) runSource(ctx context.Context, r *interp.Runner, src io.Reader, name string) int {
	file, err := syntax.NewParser().Parse(src, name)
	if err != nil {
		fmt.Fprintf(sh.opts.Stderr, "%s\n", err)
		return 2
	}

	return exitCode(r.Run(ctx, file), sh.opts.Stderr, name)
}

// promptReader feeds lines from the terminal to the interactive parser,
// with the primary prompt or, inside an unfinished command, "> ".
type promptReader struct {
	lines       *termline.Reader
	parser      *syntax.Parser
	ps1         string
	buf         []byte
	eof         bool
	interrupted bool
}

// Read implements io.Reader.
func (p *promptReader) Read(b []byte) (int, error) {
	for len(p.buf) == 0 {
		prompt := p.ps1
		if p.parser.Incomplete() {
			prompt = "> "
		}

		line, err := p.lines.ReadLine(prompt)
		if errors.Is(err, termline.ErrInterrupt) {
			p.interrupted = true
			return 0, err
		}

		if err != nil {
			p.eof = true
			return 0, io.EOF
		}

		p.buf = append([]byte(line), '\n')
	}

	n := copy(b, p.buf)
	p.buf = p.buf[n:]

	return n, nil
}

// interactive runs the prompt loop.
func (sh *shell) interactive(ctx context.Context, r *interp.Runner, name string) int {
	ps1 := "$ "
	if os.Geteuid() == 0 {
		ps1 = "# "
	}

	lines := termline.New(sh.opts.Stdin, sh.opts.Stdout)
	status := 0

	for {
		parser := syntax.NewParser()
		pr := &promptReader{lines: lines, parser: parser, ps1: ps1}

		for stmts, err := range parser.InteractiveSeq(bufio.NewReaderSize(pr, 1)) {
			if err != nil {
				if !pr.interrupted && !pr.eof {
					fmt.Fprintf(sh.opts.Stderr, "%s: %s\n", name, err)
					status = 2
				}

				break
			}

			if parser.Incomplete() {
				continue
			}

			for _, stmt := range stmts {
				stmtCtx, cancel := context.WithCancel(ctx)
				sig := make(chan os.Signal, 1)
				signal.Notify(sig, os.Interrupt)

				interrupted := make(chan bool, 1)

				go func() {
					_, ok := <-sig
					if ok {
						cancel()
					}

					interrupted <- ok
				}()

				err := r.Run(stmtCtx, stmt)

				signal.Stop(sig)
				close(sig)
				cancel()

				if <-interrupted {
					fmt.Fprintln(sh.opts.Stdout)
				}

				status = exitCode(err, sh.opts.Stderr, name)

				if r.Exited() {
					return status
				}

				// terminated (SIGTERM), not just interrupted
				if ctx.Err() != nil {
					return 143
				}
			}
		}

		if pr.eof {
			return status
		}
	}
}

// cleanErr strips the operation and path from a file error.
func cleanErr(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}

	var le *os.LinkError
	if errors.As(err, &le) {
		err = le.Err
	}

	msg := err.Error()
	if msg == "" {
		return msg
	}

	return strings.ToUpper(msg[:1]) + msg[1:]
}

// environ returns the exported variables of the interpreter.
func environ(env expand.Environ) []string {
	seen := map[string]string{}
	var order []string

	env.Each(func(name string, vr expand.Variable) bool {
		if !vr.Exported || vr.Kind != expand.String {
			return true
		}

		if _, ok := seen[name]; !ok {
			order = append(order, name)
		}

		seen[name] = vr.Str

		return true
	})

	sort.Strings(order)

	res := make([]string, 0, len(order))
	for _, n := range order {
		res = append(res, n+"="+seen[n])
	}

	return res
}

// unimplementedBuiltins are names the interpreter reserves as builtins but
// does not implement; they are given to the exec handler under another
// name, so that the commands here run instead.
var unimplementedBuiltins = map[string]string{
	"kill": "\x00kill",
}

// standaloneBuiltins are interpreter builtins that are run by path too.
var standaloneBuiltins = map[string]bool{
	"echo": true, "printf": true, "true": true, "false": true, "test": true, "[": true, "pwd": true,
}

// callHandler renames the unimplemented builtins.
func callHandler(_ context.Context, args []string) ([]string, error) {
	if n, ok := unimplementedBuiltins[args[0]]; ok {
		args = append([]string{n}, args[1:]...)
	}

	return args, nil
}

// execMiddleware runs built-in commands, and refuses everything else: there
// is no other program to run.
func (sh *shell) execMiddleware(interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)

		e := &Env{
			Stdin:   hc.Stdin,
			Stdout:  hc.Stdout,
			Stderr:  hc.Stderr,
			Dir:     hc.Dir,
			Environ: environ(hc.Env),
		}

		ctx = context.WithValue(ctx, runnerKey{}, sh.run)

		if code := sh.run(ctx, e, args); code != 0 {
			return interp.ExitStatus(uint8(code))
		}

		return nil
	}
}

// run runs one command line (args[0] is the command).
func (sh *shell) run(ctx context.Context, e *Env, args []string) int {
	name := args[0]

	for real, alias := range unimplementedBuiltins {
		if name == alias {
			name = real
			args = append([]string{real}, args[1:]...)
		}
	}

	// /bin/ls is ls, as in BusyBox.
	base := path.Base(filepath.ToSlash(name))

	if c, ok := sh.commands[base]; ok {
		return c(ctx, e, args)
	}

	// /bin/echo, env printf ...: the interpreter's builtins that are also
	// programs elsewhere.
	if standaloneBuiltins[base] {
		err := interp.HandlerCtx(ctx).Builtin(ctx, append([]string{base}, args[1:]...))
		if err == nil {
			return 0
		}

		var code int
		if _, scanErr := fmt.Sscanf(err.Error(), "builtin exit status %d", &code); scanErr == nil {
			return code
		}

		fmt.Fprintf(e.Stderr, "%s: %s\n", name, err)

		return 1
	}

	// A script with a #!/bin/sh (or bash) line is run by this shell.
	if strings.Contains(name, "/") {
		if prog, ok := shebang(e.Path(name)); ok {
			if c, ok := sh.commands[prog]; ok {
				return c(ctx, e, append([]string{prog, name}, args[1:]...))
			}
		}
	}

	fmt.Fprintf(e.Stderr, "%s: not available in this image\n", name)

	return 127
}

// shebang returns the interpreter name of a #! script.
func shebang(file string) (string, bool) {
	f, err := os.Open(file)
	if err != nil {
		return "", false
	}
	defer f.Close() //nolint:errcheck // read-only

	line, _ := bufio.NewReader(io.LimitReader(f, 256)).ReadString('\n')
	if !strings.HasPrefix(line, "#!") {
		return "", false
	}

	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return "", false
	}

	name := path.Base(fields[0])
	if name == "env" && len(fields) > 1 {
		name = fields[1]
	}

	return name, true
}
