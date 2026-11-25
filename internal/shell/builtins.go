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

package shell

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// builtins are the commands of the shell besides the interpreter's own.
var builtins map[string]Command

func init() {
	builtins = map[string]Command{
		"basename": cmdBasename,
		"cat":      cmdCat,
		"cp":       cmdCp,
		"date":     cmdDate,
		"df":       cmdDf,
		"dirname":  cmdDirname,
		"du":       cmdDu,
		"env":      cmdEnv,
		"grep":     cmdGrep,
		"head":     cmdHead,
		"hostname": cmdHostname,
		"id":       cmdID,
		"kill":     cmdKill,
		"ls":       cmdLs,
		"mkdir":    cmdMkdir,
		"mv":       cmdMv,
		"ps":       cmdPs,
		"rm":       cmdRm,
		"rmdir":    cmdRmdir,
		"sleep":    cmdSleep,
		"tail":     cmdTail,
		"touch":    cmdTouch,
		"uname":    cmdUname,
		"wc":       cmdWc,
		"whoami":   cmdWhoami,
	}
}

// flagSet is the result of parseFlags: single-letter flags and their values.
type flagSet map[rune]string

func (f flagSet) has(r rune) bool { _, ok := f[r]; return ok }

// parseFlags parses leading single-letter options (combinable, as -la);
// the letters in valued take a value (-n5 or -n 5). "--" ends the options,
// and "-" alone is an operand.
func parseFlags(name string, args []string, allowed, valued string) (flagSet, []string, error) {
	flags := flagSet{}

	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}

		if len(a) < 2 || a[0] != '-' {
			break
		}

		for j, r := range a[1:] {
			if !strings.ContainsRune(allowed+valued, r) {
				return nil, nil, fmt.Errorf("%s: invalid option -- '%c'", name, r)
			}

			if strings.ContainsRune(valued, r) {
				v := a[2+j:]
				if v == "" {
					if i+1 >= len(args) {
						return nil, nil, fmt.Errorf("%s: option requires an argument -- '%c'", name, r)
					}

					i++
					v = args[i]
				}

				if old, ok := flags[r]; ok {
					v = old + "\n" + v
				}

				flags[r] = v

				break
			}

			flags[r] = ""
		}
	}

	return flags, args[i:], nil
}

// unsupported returns a command that is not available on this platform.
func unsupported(name string) Command {
	return func(_ context.Context, e *Env, _ []string) int {
		return fail(e, name, "not supported on %s", runtime.GOOS)
	}
}

// fail prints an error for a command and returns status 1.
func fail(e *Env, name, format string, args ...any) int {
	fmt.Fprintf(e.Stderr, name+": "+format+"\n", args...)
	return 1
}

// usageErr prints an option error and returns status 2 (1 for some tools).
func usageErr(e *Env, err error, code int) int {
	fmt.Fprintln(e.Stderr, err)
	return code
}

// open opens a file operand; "-" is standard input.
func open(e *Env, name string) (io.ReadCloser, error) {
	if name == "-" {
		return io.NopCloser(e.Stdin), nil
	}

	return os.Open(e.Path(name))
}

func cmdCat(_ context.Context, e *Env, args []string) int {
	flags, files, err := parseFlags("cat", args[1:], "nu", "")
	if err != nil {
		return usageErr(e, err, 1)
	}

	if len(files) == 0 {
		files = []string{"-"}
	}

	status, line := 0, 1

	for _, f := range files {
		r, err := open(e, f)
		if err != nil {
			status = fail(e, "cat", "%s: %s", f, cleanErr(err))
			continue
		}

		if flags.has('n') {
			sc := bufio.NewScanner(r)
			sc.Buffer(make([]byte, 64*1024), 16*1024*1024)

			for sc.Scan() {
				fmt.Fprintf(e.Stdout, "%6d\t%s\n", line, sc.Text())
				line++
			}
		} else if _, err := io.Copy(e.Stdout, r); err != nil {
			status = fail(e, "cat", "%s: %s", f, cleanErr(err))
		}

		_ = r.Close()
	}

	return status
}

// humanize formats a size as ls -h and df -h do: 4.0K, 12M.
func humanize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10)
	}

	f := float64(n)
	for _, u := range []string{"K", "M", "G", "T", "P"} {
		f /= 1024
		if f < 1024 || u == "P" {
			if f < 10 {
				return strconv.FormatFloat(f, 'f', 1, 64) + u
			}

			return strconv.FormatFloat(f, 'f', 0, 64) + u
		}
	}

	return strconv.FormatInt(n, 10)
}

func cmdLs(_ context.Context, e *Env, args []string) int {
	flags, paths, err := parseFlags("ls", args[1:], "1aAlhdFtrS", "")
	if err != nil {
		return usageErr(e, err, 2)
	}

	if len(paths) == 0 {
		paths = []string{"."}
	}

	type entry struct {
		name string
		fi   fs.FileInfo
		dir  string
	}

	status := 0

	var files []entry

	var dirs []string

	for _, p := range paths {
		fi, err := os.Lstat(e.Path(p))
		if err != nil {
			status = fail(e, "ls", "cannot access '%s': %s", p, cleanErr(err))
			status = 2

			continue
		}

		if fi.IsDir() && !flags.has('d') {
			dirs = append(dirs, p)
		} else {
			files = append(files, entry{name: p, fi: fi, dir: filepath.Dir(e.Path(p))})
		}
	}

	sortEntries := func(list []entry) {
		sort.SliceStable(list, func(i, j int) bool {
			switch {
			case flags.has('t'):
				return list[i].fi.ModTime().After(list[j].fi.ModTime())
			case flags.has('S'):
				return list[i].fi.Size() > list[j].fi.Size()
			default:
				return list[i].name < list[j].name
			}
		})

		if flags.has('r') {
			for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
				list[i], list[j] = list[j], list[i]
			}
		}
	}

	print := func(list []entry) {
		sortEntries(list)

		for _, en := range list {
			name := en.name
			if flags.has('F') {
				switch {
				case en.fi.IsDir():
					name += "/"
				case en.fi.Mode()&fs.ModeSymlink != 0:
					name += "@"
				case en.fi.Mode()&0o111 != 0:
					name += "*"
				}
			}

			if !flags.has('l') {
				fmt.Fprintln(e.Stdout, name)
				continue
			}

			size := strconv.FormatInt(en.fi.Size(), 10)
			if flags.has('h') {
				size = humanize(en.fi.Size())
			}

			nlink, owner, group := fileOwner(en.fi)

			mtime := en.fi.ModTime()
			stamp := mtime.Format("Jan _2 15:04")

			if time.Since(mtime) > 180*24*time.Hour || time.Until(mtime) > time.Hour {
				stamp = mtime.Format("Jan _2  2006")
			}

			if en.fi.Mode()&fs.ModeSymlink != 0 {
				if target, err := os.Readlink(filepath.Join(en.dir, filepath.Base(en.name))); err == nil {
					name += " -> " + target
				}
			}

			fmt.Fprintf(e.Stdout, "%s %3d %-8s %-8s %8s %s %s\n",
				modeString(en.fi.Mode()), nlink, owner, group, size, stamp, name)
		}
	}

	print(files)

	for i, d := range dirs {
		dirents, err := os.ReadDir(e.Path(d))
		if err != nil {
			status = fail(e, "ls", "cannot open directory '%s': %s", d, cleanErr(err))
			status = 2

			continue
		}

		if len(dirs)+len(files) > 1 {
			if i > 0 || len(files) > 0 {
				fmt.Fprintln(e.Stdout)
			}

			fmt.Fprintf(e.Stdout, "%s:\n", d)
		}

		var list []entry

		if flags.has('a') {
			for _, n := range []string{".", ".."} {
				if fi, err := os.Stat(filepath.Join(e.Path(d), n)); err == nil {
					list = append(list, entry{name: n, fi: fi, dir: e.Path(d)})
				}
			}
		}

		for _, de := range dirents {
			if strings.HasPrefix(de.Name(), ".") && !flags.has('a') && !flags.has('A') {
				continue
			}

			fi, err := de.Info()
			if err != nil {
				continue
			}

			list = append(list, entry{name: de.Name(), fi: fi, dir: e.Path(d)})
		}

		if flags.has('l') {
			var blocks int64
			for _, en := range list {
				blocks += (en.fi.Size() + 1023) / 1024
			}

			fmt.Fprintf(e.Stdout, "total %d\n", blocks)
		}

		print(list)
	}

	return status
}

// modeString formats a mode as ls -l does: drwxr-xr-x.
func modeString(m fs.FileMode) string {
	b := []byte("----------")

	switch {
	case m.IsDir():
		b[0] = 'd'
	case m&fs.ModeSymlink != 0:
		b[0] = 'l'
	case m&fs.ModeNamedPipe != 0:
		b[0] = 'p'
	case m&fs.ModeSocket != 0:
		b[0] = 's'
	case m&fs.ModeCharDevice != 0:
		b[0] = 'c'
	case m&fs.ModeDevice != 0:
		b[0] = 'b'
	}

	const rwx = "rwxrwxrwx"
	for i := range 9 {
		if m&(1<<uint(8-i)) != 0 {
			b[i+1] = rwx[i]
		}
	}

	if m&fs.ModeSetuid != 0 {
		b[3] = map[bool]byte{true: 's', false: 'S'}[b[3] == 'x']
	}

	if m&fs.ModeSetgid != 0 {
		b[6] = map[bool]byte{true: 's', false: 'S'}[b[6] == 'x']
	}

	if m&fs.ModeSticky != 0 {
		b[9] = map[bool]byte{true: 't', false: 'T'}[b[9] == 'x']
	}

	return string(b)
}

// copyFile copies one regular file (or symbolic link), keeping its mode.
func copyFile(src, dst string, fi fs.FileInfo, keepTimes bool) error {
	if fi.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}

		_ = os.Remove(dst)

		return os.Symlink(target, dst)
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // read-only

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}

	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}

	if err = out.Close(); err != nil {
		return err
	}

	if keepTimes {
		return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	}

	return nil
}

// copyTree copies src to dst, recursively when recursive is set.
func copyTree(src, dst string, recursive, keepTimes bool) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}

	if !fi.IsDir() {
		return copyFile(src, dst, fi, keepTimes)
	}

	if !recursive {
		return fmt.Errorf("-r not specified; omitting directory '%s'", src)
	}

	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}

		return copyFile(p, target, info, keepTimes)
	})
}

// destinations pairs each source with its destination: into dst when dst
// is a directory or there are several sources.
func destinations(e *Env, name string, operands []string) ([][2]string, int) {
	if len(operands) < 2 {
		return nil, fail(e, name, "missing destination file operand")
	}

	srcs, dst := operands[:len(operands)-1], operands[len(operands)-1]

	fi, err := os.Stat(e.Path(dst))
	intoDir := err == nil && fi.IsDir()

	if len(srcs) > 1 && !intoDir {
		return nil, fail(e, name, "target '%s' is not a directory", dst)
	}

	var pairs [][2]string

	for _, s := range srcs {
		d := e.Path(dst)
		if intoDir {
			d = filepath.Join(d, filepath.Base(filepath.Clean(s)))
		}

		pairs = append(pairs, [2]string{s, d})
	}

	return pairs, 0
}

func cmdCp(_ context.Context, e *Env, args []string) int {
	flags, operands, err := parseFlags("cp", args[1:], "rRfpaPdL", "")
	if err != nil {
		return usageErr(e, err, 1)
	}

	pairs, status := destinations(e, "cp", operands)
	if status != 0 {
		return status
	}

	recursive := flags.has('r') || flags.has('R') || flags.has('a')
	keep := flags.has('p') || flags.has('a')

	for _, p := range pairs {
		if err := copyTree(e.Path(p[0]), p[1], recursive, keep); err != nil {
			status = fail(e, "cp", "cannot copy '%s': %s", p[0], cleanErr(err))
		}
	}

	return status
}

func cmdMv(_ context.Context, e *Env, args []string) int {
	_, operands, err := parseFlags("mv", args[1:], "fin", "")
	if err != nil {
		return usageErr(e, err, 1)
	}

	pairs, status := destinations(e, "mv", operands)
	if status != 0 {
		return status
	}

	for _, p := range pairs {
		src := e.Path(p[0])

		err := os.Rename(src, p[1])
		if err != nil {
			// another file system: copy, then remove
			if _, statErr := os.Lstat(src); statErr == nil {
				if err = copyTree(src, p[1], true, true); err == nil {
					err = os.RemoveAll(src)
				}
			}
		}

		if err != nil {
			status = fail(e, "mv", "cannot move '%s' to '%s': %s", p[0], operands[len(operands)-1], cleanErr(err))
		}
	}

	return status
}

func cmdRm(_ context.Context, e *Env, args []string) int {
	flags, operands, err := parseFlags("rm", args[1:], "rRfdiv", "")
	if err != nil {
		return usageErr(e, err, 1)
	}

	force, recursive := flags.has('f'), flags.has('r') || flags.has('R')

	if len(operands) == 0 && !force {
		return fail(e, "rm", "missing operand")
	}

	status := 0

	for _, o := range operands {
		p := filepath.Clean(e.Path(o))

		if recursive && p == string(filepath.Separator) {
			status = fail(e, "rm", "it is dangerous to operate recursively on '/'")
			continue
		}

		if base := filepath.Base(o); base == "." || base == ".." {
			status = fail(e, "rm", "refusing to remove '.' or '..' directory: skipping '%s'", o)
			continue
		}

		fi, err := os.Lstat(p)
		if err != nil {
			if !force {
				status = fail(e, "rm", "cannot remove '%s': %s", o, cleanErr(err))
			}

			continue
		}

		switch {
		case fi.IsDir() && recursive:
			err = os.RemoveAll(p)
		case fi.IsDir() && !flags.has('d'):
			status = fail(e, "rm", "cannot remove '%s': Is a directory", o)
			continue
		default:
			err = os.Remove(p)
		}

		if err != nil {
			status = fail(e, "rm", "cannot remove '%s': %s", o, cleanErr(err))
		}
	}

	return status
}

func cmdMkdir(_ context.Context, e *Env, args []string) int {
	flags, operands, err := parseFlags("mkdir", args[1:], "pv", "m")
	if err != nil {
		return usageErr(e, err, 1)
	}

	if len(operands) == 0 {
		return fail(e, "mkdir", "missing operand")
	}

	mode := fs.FileMode(0o777)

	if m, ok := flags['m']; ok {
		n, err := strconv.ParseUint(m, 8, 32)
		if err != nil {
			return fail(e, "mkdir", "invalid mode '%s'", m)
		}

		mode = fs.FileMode(n)
	}

	status := 0

	for _, o := range operands {
		var err error
		if flags.has('p') {
			err = os.MkdirAll(e.Path(o), mode)
		} else {
			err = os.Mkdir(e.Path(o), mode)
		}

		if err == nil && flags.has('m') {
			err = os.Chmod(e.Path(o), mode)
		}

		if err != nil {
			status = fail(e, "mkdir", "cannot create directory '%s': %s", o, cleanErr(err))
		}
	}

	return status
}

func cmdRmdir(_ context.Context, e *Env, args []string) int {
	_, operands, err := parseFlags("rmdir", args[1:], "p", "")
	if err != nil {
		return usageErr(e, err, 1)
	}

	if len(operands) == 0 {
		return fail(e, "rmdir", "missing operand")
	}

	status := 0

	for _, o := range operands {
		fi, err := os.Stat(e.Path(o))
		if err == nil && !fi.IsDir() {
			status = fail(e, "rmdir", "failed to remove '%s': Not a directory", o)
			continue
		}

		if err == nil {
			err = os.Remove(e.Path(o))
		}

		if err != nil {
			status = fail(e, "rmdir", "failed to remove '%s': %s", o, cleanErr(err))
		}
	}

	return status
}

func cmdTouch(_ context.Context, e *Env, args []string) int {
	flags, operands, err := parseFlags("touch", args[1:], "cam", "")
	if err != nil {
		return usageErr(e, err, 1)
	}

	if len(operands) == 0 {
		return fail(e, "touch", "missing file operand")
	}

	status := 0
	now := time.Now()

	for _, o := range operands {
		p := e.Path(o)

		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			if flags.has('c') {
				continue
			}

			f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0o666)
			if err != nil {
				status = fail(e, "touch", "cannot touch '%s': %s", o, cleanErr(err))
				continue
			}

			_ = f.Close()

			continue
		}

		if err := os.Chtimes(p, now, now); err != nil {
			status = fail(e, "touch", "cannot touch '%s': %s", o, cleanErr(err))
		}
	}

	return status
}

// dirSize returns the disk usage of p in bytes, printing each directory
// (or each file with all) through each.
func dirSize(p string, each func(string, int64)) (int64, error) {
	var total int64

	fi, err := os.Lstat(p)
	if err != nil {
		return 0, err
	}

	if !fi.IsDir() {
		return diskUsage(fi), nil
	}

	total = diskUsage(fi)

	entries, err := os.ReadDir(p)
	if err != nil {
		return total, err
	}

	var firstErr error

	for _, de := range entries {
		child := filepath.Join(p, de.Name())

		n, err := dirSize(child, each)
		if err != nil && firstErr == nil {
			firstErr = err
		}

		total += n
	}

	if each != nil {
		each(p, total)
	}

	return total, firstErr
}

func cmdDu(_ context.Context, e *Env, args []string) int {
	flags, operands, err := parseFlags("du", args[1:], "shkcaxm", "d")
	if err != nil {
		return usageErr(e, err, 1)
	}

	if len(operands) == 0 {
		operands = []string{"."}
	}

	format := func(n int64) string {
		switch {
		case flags.has('h'):
			return humanize(n)
		case flags.has('m'):
			return strconv.FormatInt((n+1024*1024-1)/(1024*1024), 10)
		default:
			return strconv.FormatInt((n+1023)/1024, 10)
		}
	}

	maxDepth := -1
	if d, ok := flags['d']; ok {
		if maxDepth, err = strconv.Atoi(d); err != nil {
			return fail(e, "du", "invalid maximum depth '%s'", d)
		}
	}

	status := 0

	var grand int64

	for _, o := range operands {
		root := e.Path(o)

		var each func(string, int64)
		if !flags.has('s') {
			each = func(p string, n int64) {
				rel, _ := filepath.Rel(root, p)
				if maxDepth >= 0 && rel != "." && strings.Count(rel, string(filepath.Separator))+1 > maxDepth {
					return
				}

				if p == root {
					return
				}

				fmt.Fprintf(e.Stdout, "%s\t%s\n", format(n), filepath.Join(o, rel))
			}
		}

		n, err := dirSize(root, each)
		if err != nil {
			status = fail(e, "du", "%s: %s", o, cleanErr(err))
		}

		grand += n

		fmt.Fprintf(e.Stdout, "%s\t%s\n", format(n), o)
	}

	if flags.has('c') {
		fmt.Fprintf(e.Stdout, "%s\ttotal\n", format(grand))
	}

	return status
}

func cmdEnv(ctx context.Context, e *Env, args []string) int {
	flags, rest, err := parseFlags("env", args[1:], "i0", "u")
	if err != nil {
		return usageErr(e, err, 125)
	}

	environ := e.Environ
	if flags.has('i') {
		environ = nil
	}

	unset := func(list []string, name string) []string {
		out := list[:0:0]
		for _, kv := range list {
			if k, _, _ := strings.Cut(kv, "="); k != name {
				out = append(out, kv)
			}
		}

		return out
	}

	if u, ok := flags['u']; ok {
		environ = unset(environ, u)
	}

	for len(rest) > 0 && strings.Contains(rest[0], "=") {
		k, _, _ := strings.Cut(rest[0], "=")
		environ = append(unset(environ, k), rest[0])
		rest = rest[1:]
	}

	if len(rest) == 0 {
		sep := "\n"
		if flags.has('0') {
			sep = "\x00"
		}

		for _, kv := range environ {
			fmt.Fprint(e.Stdout, kv+sep)
		}

		return 0
	}

	sub := *e
	sub.Environ = environ

	runner, _ := ctx.Value(runnerKey{}).(func(context.Context, *Env, []string) int)
	if runner == nil {
		return fail(e, "env", "cannot run '%s'", rest[0])
	}

	return runner(ctx, &sub, rest)
}

// runnerKey is the context key under which the shell passes a function
// that runs a command line (for env and similar).
type runnerKey struct{}

func cmdID(_ context.Context, e *Env, args []string) int {
	flags, operands, err := parseFlags("id", args[1:], "ugGnr", "")
	if err != nil {
		return usageErr(e, err, 1)
	}

	var u *user.User

	if len(operands) > 0 {
		if u, err = user.Lookup(operands[0]); err != nil {
			return fail(e, "id", "'%s': no such user", operands[0])
		}
	} else {
		u = currentUser()
	}

	groups, _ := u.GroupIds()
	if len(groups) == 0 {
		groups = []string{u.Gid}
	}

	groupName := func(gid string) string {
		if g, err := user.LookupGroupId(gid); err == nil {
			return g.Name
		}

		return gid
	}

	userName := u.Username
	if userName == "" {
		userName = u.Uid
	}

	name := flags.has('n')

	switch {
	case flags.has('u'):
		fmt.Fprintln(e.Stdout, map[bool]string{true: userName, false: u.Uid}[name])
	case flags.has('g'):
		fmt.Fprintln(e.Stdout, map[bool]string{true: groupName(u.Gid), false: u.Gid}[name])
	case flags.has('G'):
		out := make([]string, len(groups))
		for i, g := range groups {
			out[i] = map[bool]string{true: groupName(g), false: g}[name]
		}

		fmt.Fprintln(e.Stdout, strings.Join(out, " "))
	default:
		out := make([]string, len(groups))
		for i, g := range groups {
			out[i] = g + "(" + groupName(g) + ")"
		}

		fmt.Fprintf(e.Stdout, "uid=%s(%s) gid=%s(%s) groups=%s\n",
			u.Uid, userName, u.Gid, groupName(u.Gid), strings.Join(out, ","))
	}

	return 0
}

// currentUser returns the current user, with the numeric ids when there is
// no entry in /etc/passwd.
func currentUser() *user.User {
	if u, err := user.Current(); err == nil {
		return u
	}

	uid, gid := strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())

	return &user.User{Uid: uid, Gid: gid}
}

func cmdWhoami(_ context.Context, e *Env, _ []string) int {
	u := currentUser()
	if u.Username == "" {
		return fail(e, "whoami", "cannot find name for user ID %s", u.Uid)
	}

	fmt.Fprintln(e.Stdout, u.Username)

	return 0
}

func cmdHostname(_ context.Context, e *Env, _ []string) int {
	h, err := os.Hostname()
	if err != nil {
		return fail(e, "hostname", "%s", err)
	}

	fmt.Fprintln(e.Stdout, h)

	return 0
}

// strftime formats t with the date(1) conversions.
func strftime(t time.Time, format string) string {
	var b strings.Builder

	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' || i+1 == len(format) {
			b.WriteByte(c)
			continue
		}

		i++

		switch format[i] {
		case 'Y':
			b.WriteString(t.Format("2006"))
		case 'y':
			b.WriteString(t.Format("06"))
		case 'm':
			b.WriteString(t.Format("01"))
		case 'd':
			b.WriteString(t.Format("02"))
		case 'e':
			b.WriteString(t.Format("_2"))
		case 'H':
			b.WriteString(t.Format("15"))
		case 'I':
			b.WriteString(t.Format("03"))
		case 'M':
			b.WriteString(t.Format("04"))
		case 'S':
			b.WriteString(t.Format("05"))
		case 'N':
			fmt.Fprintf(&b, "%09d", t.Nanosecond())
		case 'p':
			b.WriteString(t.Format("PM"))
		case 'a':
			b.WriteString(t.Format("Mon"))
		case 'A':
			b.WriteString(t.Format("Monday"))
		case 'b', 'h':
			b.WriteString(t.Format("Jan"))
		case 'B':
			b.WriteString(t.Format("January"))
		case 'j':
			fmt.Fprintf(&b, "%03d", t.YearDay())
		case 'u':
			wd := int(t.Weekday())
			if wd == 0 {
				wd = 7
			}

			b.WriteString(strconv.Itoa(wd))
		case 'w':
			b.WriteString(strconv.Itoa(int(t.Weekday())))
		case 's':
			b.WriteString(strconv.FormatInt(t.Unix(), 10))
		case 'z':
			b.WriteString(t.Format("-0700"))
		case 'Z':
			b.WriteString(t.Format("MST"))
		case 'F':
			b.WriteString(t.Format("2006-01-02"))
		case 'T':
			b.WriteString(t.Format("15:04:05"))
		case 'R':
			b.WriteString(t.Format("15:04"))
		case 'D':
			b.WriteString(t.Format("01/02/06"))
		case 'c':
			b.WriteString(t.Format("Mon Jan _2 15:04:05 2006"))
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(format[i])
		}
	}

	return b.String()
}

func cmdDate(_ context.Context, e *Env, args []string) int {
	flags, operands, err := parseFlags("date", args[1:], "uRI", "dr")
	if err != nil {
		return usageErr(e, err, 1)
	}

	t := time.Now()

	if d, ok := flags['d']; ok {
		if strings.HasPrefix(d, "@") {
			n, err := strconv.ParseInt(d[1:], 10, 64)
			if err != nil {
				return fail(e, "date", "invalid date '%s'", d)
			}

			t = time.Unix(n, 0)
		} else {
			parsed := false

			for _, l := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
				if p, err := time.ParseInLocation(l, d, time.Local); err == nil {
					t, parsed = p, true
					break
				}
			}

			if !parsed {
				return fail(e, "date", "invalid date '%s'", d)
			}
		}
	}

	if r, ok := flags['r']; ok {
		fi, err := os.Stat(e.Path(r))
		if err != nil {
			return fail(e, "date", "%s: %s", r, cleanErr(err))
		}

		t = fi.ModTime()
	}

	if flags.has('u') || e.Getenv("TZ") == "UTC" {
		t = t.UTC()
	} else if tz := e.Getenv("TZ"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			t = t.In(loc)
		}
	}

	format := "%a %b %e %H:%M:%S %Z %Y"

	switch {
	case flags.has('R'):
		format = "%a, %d %b %Y %H:%M:%S %z"
	case flags.has('I'):
		format = "%F"
	}

	for _, o := range operands {
		if !strings.HasPrefix(o, "+") {
			return fail(e, "date", "setting the date is not supported: '%s'", o)
		}

		format = o[1:]
	}

	fmt.Fprintln(e.Stdout, strftime(t, format))

	return 0
}

// parseDuration parses a sleep operand: a number with an optional s, m,
// h or d suffix.
func parseDuration(s string) (time.Duration, error) {
	unit := time.Second

	switch {
	case strings.HasSuffix(s, "s"):
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "m"):
		s, unit = s[:len(s)-1], time.Minute
	case strings.HasSuffix(s, "h"):
		s, unit = s[:len(s)-1], time.Hour
	case strings.HasSuffix(s, "d"):
		s, unit = s[:len(s)-1], 24*time.Hour
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid time interval")
	}

	return time.Duration(f * float64(unit)), nil
}

func cmdSleep(ctx context.Context, e *Env, args []string) int {
	if len(args) < 2 {
		return fail(e, "sleep", "missing operand")
	}

	var total time.Duration

	for _, a := range args[1:] {
		d, err := parseDuration(a)
		if err != nil {
			return fail(e, "sleep", "invalid time interval '%s'", a)
		}

		total += d
	}

	t := time.NewTimer(total)
	defer t.Stop()

	select {
	case <-t.C:
		return 0
	case <-ctx.Done():
		return 130
	}
}

// breToERE translates a POSIX basic regular expression to the extended
// syntax Go's regexp understands: \( \) \{ \} \| \+ \? are the operators,
// and the bare characters are literals.
func breToERE(p string) string {
	var b strings.Builder

	for i := 0; i < len(p); i++ {
		c := p[i]

		switch {
		case c == '\\' && i+1 < len(p):
			i++

			switch n := p[i]; n {
			case '(', ')', '{', '}', '|', '+', '?':
				b.WriteByte(n)
			default:
				b.WriteByte('\\')
				b.WriteByte(n)
			}
		case strings.IndexByte("(){}|+?", c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '*' && (i == 0 || p[i-1] == '^' && i == 1):
			b.WriteString(`\*`)
		default:
			b.WriteByte(c)
		}
	}

	return b.String()
}

func cmdGrep(_ context.Context, e *Env, args []string) int {
	flags, operands, err := parseFlags("grep", args[1:], "ivcnlLqsEFrRhHwxoa", "efm")
	if err != nil {
		return usageErr(e, err, 2)
	}

	var patterns []string

	if p, ok := flags['e']; ok {
		patterns = append(patterns, strings.Split(p, "\n")...)
	}

	if f, ok := flags['f']; ok {
		b, err := os.ReadFile(e.Path(f))
		if err != nil {
			fmt.Fprintf(e.Stderr, "grep: %s: %s\n", f, cleanErr(err))
			return 2
		}

		patterns = append(patterns, strings.Split(strings.TrimRight(string(b), "\n"), "\n")...)
	}

	if len(patterns) == 0 {
		if len(operands) == 0 {
			fmt.Fprintln(e.Stderr, "Usage: grep [OPTION]... PATTERNS [FILE]...")
			return 2
		}

		patterns = strings.Split(operands[0], "\n")
		operands = operands[1:]
	}

	alts := make([]string, len(patterns))

	for i, p := range patterns {
		switch {
		case flags.has('F'):
			p = regexp.QuoteMeta(p)
		case !flags.has('E'):
			p = breToERE(p)
		}

		if flags.has('w') {
			p = `\b(?:` + p + `)\b`
		}

		if flags.has('x') {
			p = `^(?:` + p + `)$`
		}

		alts[i] = "(?:" + p + ")"
	}

	expr := strings.Join(alts, "|")
	if flags.has('i') {
		expr = "(?i)" + expr
	}

	re, err := regexp.Compile(expr)
	if err != nil {
		fmt.Fprintf(e.Stderr, "grep: invalid regular expression: %s\n", err)
		return 2
	}

	maxCount := -1
	if m, ok := flags['m']; ok {
		if maxCount, err = strconv.Atoi(m); err != nil {
			fmt.Fprintf(e.Stderr, "grep: invalid max count '%s'\n", m)
			return 2
		}
	}

	recursive := flags.has('r') || flags.has('R')

	if len(operands) == 0 {
		operands = []string{"-"}
		if recursive {
			operands = []string{"."}
		}
	}

	var files []string

	status := 1
	errored := false

	for _, o := range operands {
		if o == "-" || !recursive {
			files = append(files, o)
			continue
		}

		err := filepath.WalkDir(e.Path(o), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if !flags.has('s') {
					fmt.Fprintf(e.Stderr, "grep: %s: %s\n", p, cleanErr(err))
				}

				errored = true

				return nil
			}

			if !d.IsDir() {
				rel, _ := filepath.Rel(e.Path(o), p)
				files = append(files, filepath.Join(o, rel))
			}

			return nil
		})
		if err != nil {
			errored = true
		}
	}

	withName := (len(files) > 1 || recursive) && !flags.has('h') || flags.has('H')

	for _, f := range files {
		r, err := open(e, f)
		if err != nil {
			if !flags.has('s') {
				fmt.Fprintf(e.Stderr, "grep: %s: %s\n", f, cleanErr(err))
			}

			errored = true

			continue
		}

		label := f
		if f == "-" {
			label = "(standard input)"
		}

		count := 0
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		lineNo := 0

		for sc.Scan() && (maxCount < 0 || count < maxCount) {
			lineNo++
			line := sc.Text()

			if re.MatchString(line) == flags.has('v') {
				continue
			}

			count++

			if flags.has('q') {
				_ = r.Close()
				return 0
			}

			if flags.has('l') || flags.has('L') || flags.has('c') {
				continue
			}

			prefix := ""
			if withName {
				prefix = label + ":"
			}

			if flags.has('n') {
				prefix += strconv.Itoa(lineNo) + ":"
			}

			if flags.has('o') && !flags.has('v') {
				for _, m := range re.FindAllString(line, -1) {
					fmt.Fprintln(e.Stdout, prefix+m)
				}

				continue
			}

			fmt.Fprintln(e.Stdout, prefix+line)
		}

		_ = r.Close()

		switch {
		case flags.has('c'):
			if withName {
				fmt.Fprintf(e.Stdout, "%s:%d\n", label, count)
			} else {
				fmt.Fprintln(e.Stdout, count)
			}
		case flags.has('l') && count > 0:
			fmt.Fprintln(e.Stdout, label)
		case flags.has('L') && count == 0:
			fmt.Fprintln(e.Stdout, label)
		}

		if count > 0 {
			status = 0
		}
	}

	if errored && (status != 0 || !flags.has('s')) {
		return 2
	}

	return status
}

// countArg parses -n N (or the obsolete -N) for head and tail; a leading
// '+' (tail -n +N) means "from line N".
func countArg(name string, args []string) (n int64, fromStart, bytesMode bool, files []string, err error) {
	n = 10

	var rest []string

	for i := 0; i < len(args); i++ {
		a := args[i]

		switch {
		case a == "--":
			rest = append(rest, args[i+1:]...)
			i = len(args)
		case a == "-f" || a == "-F":
			return 0, false, false, nil, fmt.Errorf("%s: -f is not supported by this shell", name)
		case a == "-q" || a == "-v":
		case strings.HasPrefix(a, "-n") || strings.HasPrefix(a, "-c"):
			bytesMode = a[1] == 'c'
			v := a[2:]

			if v == "" {
				if i+1 >= len(args) {
					return 0, false, false, nil, fmt.Errorf("%s: option requires an argument -- '%c'", name, a[1])
				}

				i++
				v = args[i]
			}

			if strings.HasPrefix(v, "+") {
				fromStart = true
				v = v[1:]
			}

			v = strings.TrimPrefix(v, "-")

			if n, err = strconv.ParseInt(v, 10, 64); err != nil || n < 0 {
				return 0, false, false, nil, fmt.Errorf("%s: invalid number of lines: '%s'", name, v)
			}
		case len(a) > 1 && a[0] == '-' && unicode.IsDigit(rune(a[1])):
			if n, err = strconv.ParseInt(a[1:], 10, 64); err != nil {
				return 0, false, false, nil, fmt.Errorf("%s: invalid number of lines: '%s'", name, a[1:])
			}
		case len(a) > 1 && a[0] == '-':
			return 0, false, false, nil, fmt.Errorf("%s: invalid option -- '%s'", name, a[1:])
		default:
			rest = append(rest, a)
		}
	}

	if len(rest) == 0 {
		rest = []string{"-"}
	}

	return n, fromStart, bytesMode, rest, nil
}

// eachFile opens every operand, printing ==> name <== headers for several.
func eachFile(e *Env, name string, files []string, f func(io.Reader) error) int {
	status := 0

	for i, file := range files {
		r, err := open(e, file)
		if err != nil {
			status = fail(e, name, "cannot open '%s' for reading: %s", file, cleanErr(err))
			continue
		}

		if len(files) > 1 {
			if i > 0 {
				fmt.Fprintln(e.Stdout)
			}

			label := file
			if file == "-" {
				label = "standard input"
			}

			fmt.Fprintf(e.Stdout, "==> %s <==\n", label)
		}

		if err := f(r); err != nil {
			status = fail(e, name, "%s: %s", file, cleanErr(err))
		}

		_ = r.Close()
	}

	return status
}

func cmdHead(_ context.Context, e *Env, args []string) int {
	n, _, bytesMode, files, err := countArg("head", args[1:])
	if err != nil {
		return usageErr(e, err, 1)
	}

	return eachFile(e, "head", files, func(r io.Reader) error {
		if bytesMode {
			_, err := io.Copy(e.Stdout, io.LimitReader(r, n))
			return err
		}

		br := bufio.NewReader(r)

		for i := int64(0); i < n; i++ {
			line, err := br.ReadString('\n')
			if line != "" {
				fmt.Fprint(e.Stdout, line)
			}

			if err == io.EOF {
				return nil
			}

			if err != nil {
				return err
			}
		}

		return nil
	})
}

func cmdTail(_ context.Context, e *Env, args []string) int {
	n, fromStart, bytesMode, files, err := countArg("tail", args[1:])
	if err != nil {
		return usageErr(e, err, 1)
	}

	return eachFile(e, "tail", files, func(r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}

		if bytesMode {
			switch {
			case fromStart:
				data = data[min(int64(len(data)), max(n-1, 0)):]
			case int64(len(data)) > n:
				data = data[int64(len(data))-n:]
			}

			_, err = e.Stdout.Write(data)

			return err
		}

		lines := bytes.SplitAfter(data, []byte("\n"))
		if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
			lines = lines[:len(lines)-1]
		}

		switch {
		case fromStart:
			lines = lines[min(int64(len(lines)), max(n-1, 0)):]
		case int64(len(lines)) > n:
			lines = lines[int64(len(lines))-n:]
		}

		_, err = e.Stdout.Write(bytes.Join(lines, nil))

		return err
	})
}

func cmdWc(_ context.Context, e *Env, args []string) int {
	flags, files, err := parseFlags("wc", args[1:], "lwcmL", "")
	if err != nil {
		return usageErr(e, err, 1)
	}

	if !flags.has('l') && !flags.has('w') && !flags.has('c') && !flags.has('m') {
		flags['l'], flags['w'], flags['c'] = "", "", ""
	}

	stdin := len(files) == 0
	if stdin {
		files = []string{"-"}
	}

	var total [4]int64

	status := 0

	print := func(c [4]int64, label string) {
		var parts []string

		for i, f := range []rune{'l', 'w', 'm', 'c'} {
			if flags.has(f) {
				parts = append(parts, fmt.Sprintf("%7d", c[i]))
			}
		}

		// one count of one input is printed without padding, as GNU wc does
		if len(parts) == 1 && len(files) == 1 {
			parts[0] = strings.TrimSpace(parts[0])
		}

		line := strings.Join(parts, " ")
		if !stdin {
			line += " " + label
		}

		fmt.Fprintln(e.Stdout, line)
	}

	for _, f := range files {
		r, err := open(e, f)
		if err != nil {
			status = fail(e, "wc", "%s: %s", f, cleanErr(err))
			continue
		}

		data, err := io.ReadAll(r)
		_ = r.Close()

		if err != nil {
			status = fail(e, "wc", "%s: %s", f, cleanErr(err))
			continue
		}

		c := [4]int64{
			int64(bytes.Count(data, []byte("\n"))),
			int64(len(bytes.Fields(data))),
			int64(utf8.RuneCount(data)),
			int64(len(data)),
		}

		for i := range c {
			total[i] += c[i]
		}

		print(c, f)
	}

	if len(files) > 1 {
		print(total, "total")
	}

	return status
}

func cmdBasename(_ context.Context, e *Env, args []string) int {
	if len(args) < 2 {
		return fail(e, "basename", "missing operand")
	}

	b := filepath.Base(args[1])
	if len(args) > 2 && b != args[2] {
		b = strings.TrimSuffix(b, args[2])
	}

	fmt.Fprintln(e.Stdout, b)

	return 0
}

func cmdDirname(_ context.Context, e *Env, args []string) int {
	if len(args) < 2 {
		return fail(e, "dirname", "missing operand")
	}

	for _, a := range args[1:] {
		fmt.Fprintln(e.Stdout, filepath.Dir(a))
	}

	return 0
}
