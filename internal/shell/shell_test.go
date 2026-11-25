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
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// run runs `sh -c script` in dir and returns stdout, stderr and the status.
func run(t *testing.T, dir, script string, extra map[string]Command) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	code := Main(context.Background(), "sh", []string{"-c", script}, &Options{
		Stdin:    strings.NewReader(""),
		Stdout:   &out,
		Stderr:   &errOut,
		Environ:  []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TZ=UTC"},
		Dir:      dir,
		Commands: extra,
	})

	return out.String(), errOut.String(), code
}

func TestShellLanguage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for script, want := range map[string]string{
		`echo hello`: "hello\n",
		`x=1; if [ "$x" = 1 ]; then echo yes; else echo no; fi`: "yes\n",
		`for i in a b c; do printf '%s,' "$i"; done; echo`:      "a,b,c,\n",
		`f() { echo "arg:$1"; }; f one`:                         "arg:one\n",
		`echo one two three | wc -w`:                            "3\n",
		`printf 'b\na\nb\n' | grep -c b`:                        "2\n",
		`test -d . && echo dir`:                                 "dir\n",
		`true && false || echo recovered`:                       "recovered\n",
		`echo $((6 * 7))`:                                       "42\n",
		`pwd`:                                                   dir + "\n",
		`/bin/echo via-path 2>/dev/null; echo $?`:               "via-path\n0\n",
	} {
		out, errOut, code := run(t, dir, script, nil)
		assert.Equal(t, 0, code, "%s: %s", script, errOut)
		assert.Equal(t, want, out, script)
	}

	_, _, code := run(t, dir, "exit 7", nil)
	assert.Equal(t, 7, code)

	_, errOut, code := run(t, dir, "if then", nil)
	assert.Equal(t, 2, code, "a syntax error")
	assert.NotEmpty(t, errOut)
}

// TestNotAvailable is the negative test: nothing outside the built-in
// commands runs, whatever is installed on the host.
func TestNotAvailable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, cmd := range []string{"apt-get install -y curl", "curl http://example.com", "vi", "/usr/bin/env-nope", "./x"} {
		out, errOut, code := run(t, dir, cmd, nil)
		assert.Equal(t, 127, code, cmd)
		assert.Empty(t, out, cmd)
		assert.Equal(t, strings.Fields(cmd)[0]+": not available in this image\n", errOut, cmd)
	}

	// a real program on the host, by absolute path, is not run either
	if _, err := os.Stat("/usr/bin/true"); err == nil {
		_, errOut, code := run(t, dir, "/usr/bin/nproc", nil)
		assert.Equal(t, 127, code)
		assert.Contains(t, errOut, "not available in this image")
	}

	out, _, code := run(t, dir, "vi; echo status=$?", nil)
	assert.Equal(t, 0, code)
	assert.Equal(t, "status=127\n", out)
}

func TestFileCommands(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	script := `
set -e
mkdir -p a/b/c
echo one > a/f.txt
printf 'l1\nl2\nl3\n' >> a/f.txt
cp a/f.txt a/g.txt
cp -r a copy
mv copy moved
touch empty
ls
echo --
ls -a a
echo --
cat moved/g.txt | head -n 2
tail -n 1 a/f.txt
tail -n +3 a/f.txt
wc -l a/f.txt
rm a/g.txt
rm -r moved
rmdir a/b/c
ls a a/b
`
	out, errOut, code := run(t, dir, script, nil)
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, strings.Join([]string{
		"a", "empty", "moved", "--",
		".", "..", "b", "f.txt", "g.txt", "--",
		"one", "l1", "l3", "l2", "l3",
		"4 a/f.txt",
		"a:", "b", "f.txt", "", "a/b:", "",
	}, "\n"), out)

	out, _, code = run(t, dir, "ls -l a", nil)
	assert.Equal(t, 0, code)
	assert.Regexp(t, `(?m)^total \d+\n^d[rwx-]{9} +\d+ \S+ +\S+ +\d+ \w{3} [ \d]\d [\d :]{5} b\n^-rw`, out)

	out, _, code = run(t, dir, "du -s a; du -sh a", nil)
	assert.Equal(t, 0, code)
	assert.Regexp(t, `^\d+\ta\n\S+\ta\n$`, out)

	// negative: errors are reported, with the exit status of the real tools
	for script, want := range map[string]struct {
		code int
		err  string
	}{
		"cat missing":                           {1, "cat: missing: No such file or directory"},
		"rm missing":                            {1, "rm: cannot remove 'missing': No such file or directory"},
		"rm a":                                  {1, "rm: cannot remove 'a': Is a directory"},
		"rm -rf /":                              {1, "rm: it is dangerous to operate recursively on '/'"},
		"mkdir a":                               {1, "mkdir: cannot create directory 'a': File exists"},
		"cp a x":                                {1, "cp: cannot copy 'a': -r not specified; omitting directory"},
		"cp onlyone":                            {1, "cp: missing destination file operand"},
		"ls missing":                            {2, "ls: cannot access 'missing': No such file or directory"},
		"ls -Z":                                 {2, "ls: invalid option -- 'Z'"},
		"rmdir a/f.txt":                         {1, "rmdir: failed to remove 'a/f.txt': Not a directory"},
		"head -n x a/f.txt":                     {1, "head: invalid number of lines: 'x'"},
		"tail -f a/f.txt":                       {1, "tail: -f is not supported by this shell"},
		"grep -q zzz a/f.txt":                   {1, ""},
		"grep x missing":                        {2, "grep: missing: No such file or directory"},
		"grep '(' a/f.txt; grep -E '(' a/f.txt": {2, "grep: invalid regular expression"},
		"sleep abc":                             {1, "sleep: invalid time interval 'abc'"},
		"date 0101":                             {1, "date: setting the date is not supported"},
	} {
		_, errOut, code := run(t, dir, script, nil)
		assert.Equal(t, want.code, code, script)
		assert.Contains(t, errOut, want.err, script)
	}

	_, _, code = run(t, dir, "rm -f missing", nil)
	assert.Equal(t, 0, code, "rm -f ignores a missing file")

	// rm -rf / was refused, and nothing outside was touched
	_, err := os.Stat(filepath.Join(dir, "a", "f.txt"))
	assert.NoError(t, err)
}

func TestGrep(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), []byte("apple\nBanana\ncherry pie\napple pie\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "g"), []byte("pie chart\n"), 0o600))

	for script, want := range map[string]string{
		"grep apple f":            "apple\napple pie\n",
		"grep -i banana f":        "Banana\n",
		"grep -v pie f":           "apple\nBanana\n",
		"grep -n pie f":           "3:cherry pie\n4:apple pie\n",
		"grep -c pie f":           "2\n",
		"grep -x apple f":         "apple\n",
		"grep -w pie f":           "cherry pie\napple pie\n",
		"grep -o 'p[a-z]*e' f":    "pple\npie\npple\npie\n",
		"grep -E 'app|Ban' f":     "apple\nBanana\napple pie\n",
		`grep 'a\(p\)\+le' f`:     "apple\napple pie\n",
		"grep -F 'e p' f":         "apple pie\n",
		"grep -l pie f sub/g":     "f\nsub/g\n",
		"grep -r chart .":         "sub/g:pie chart\n",
		"grep -e cherry -e Ban f": "Banana\ncherry pie\n",
		"cat f | grep -m1 pie":    "cherry pie\n",
	} {
		out, errOut, code := run(t, dir, script, nil)
		assert.Equal(t, 0, code, "%s: %s", script, errOut)
		assert.Equal(t, want, out, script)
	}
}

func TestSystemCommands(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	out, _, code := run(t, dir, "id; id -u; whoami; hostname", nil)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "uid="+strconv.Itoa(os.Getuid())+"(")
	assert.Contains(t, out, "\n"+strconv.Itoa(os.Getuid())+"\n")

	out, _, code = run(t, dir, "date -u +%Y; date -d @0 -u +'%F %T %s'; date -u -I", nil)
	assert.Equal(t, 0, code)
	assert.Equal(t, strconv.Itoa(time.Now().UTC().Year())+"\n1970-01-01 00:00:00 0\n"+time.Now().UTC().Format("2006-01-02")+"\n", out)

	start := time.Now()
	_, _, code = run(t, dir, "sleep 0.2", nil)
	assert.Equal(t, 0, code)
	assert.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond)

	out, _, code = run(t, dir, "export FOO=bar; env | grep '^FOO='; env -i X=1; env A=b sh -c 'echo $A'", nil)
	assert.Equal(t, 0, code)
	assert.Equal(t, "FOO=bar\nX=1\nb\n", out)

	out, _, code = run(t, dir, "basename /a/b.txt .txt; dirname /a/b.txt", nil)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b\n/a\n", out)

	if runtime.GOOS == "linux" {
		out, errOut, code := run(t, dir, "ps; df /; df -h; uname; uname -m; kill -0 "+strconv.Itoa(os.Getpid()), nil)
		assert.Equal(t, 0, code, errOut)
		assert.Regexp(t, regexp.MustCompile(`(?m)^ *PID USER +TIME COMMAND\n(?s:.*)^ *`+strconv.Itoa(os.Getpid())+` `), out)
		assert.Contains(t, out, "Filesystem")
		assert.Contains(t, out, "Linux\n")

		_, errOut, code = run(t, dir, "kill -9 abc", nil)
		assert.Equal(t, 1, code)
		assert.Contains(t, errOut, "arguments must be process IDs")
	}
}

func TestCommandsAndScripts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var gotArgs []string

	extra := map[string]Command{
		"mongosh": func(_ context.Context, e *Env, args []string) int {
			gotArgs = args
			_, _ = e.Stdout.Write([]byte("in-process " + e.Getenv("MARK") + "\n"))

			return 3
		},
	}

	out, _, code := run(t, dir, "MARK=x mongosh --quiet --eval 1; echo $?", extra)
	assert.Equal(t, 0, code)
	assert.Equal(t, "in-process x\n3\n", out)
	assert.Equal(t, []string{"mongosh", "--quiet", "--eval", "1"}, gotArgs)

	// nested shells, and a #!/bin/sh script run by name
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s.sh"), []byte("#!/bin/sh\necho script \"$1\" $0\n"), 0o700))

	out, errOut, code := run(t, dir, `sh -c 'echo nested $0 $1' name arg; bash -c 'exit 5'; echo $?; ./s.sh one; sh s.sh two`, extra)
	assert.Equal(t, 0, code, errOut)
	assert.Equal(t, "nested name arg\n5\nscript one ./s.sh\nscript two s.sh\n", out)

	// a script file given to the shell, and its positional parameters
	var stdout, stderr bytes.Buffer
	code = Main(context.Background(), "bash", []string{filepath.Join(dir, "s.sh"), "p1"}, &Options{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Dir: dir, Environ: []string{},
	})
	assert.Equal(t, 0, code, stderr.String())
	assert.Equal(t, "script p1 "+filepath.Join(dir, "s.sh")+"\n", stdout.String())

	// commands from standard input, as `docker exec -i ... sh < script` does
	stdout.Reset()
	code = Main(context.Background(), "sh", nil, &Options{
		Stdin: strings.NewReader("echo from stdin\nexit 9\necho never\n"), Stdout: &stdout, Stderr: &stderr,
		Dir: dir, Environ: []string{},
	})
	assert.Equal(t, 9, code)
	assert.Equal(t, "from stdin\n", stdout.String())

	// --help lists the built-in commands; a missing script is status 127
	stdout.Reset()
	code = Main(context.Background(), "sh", []string{"--help"}, &Options{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Dir: dir, Environ: []string{}, Commands: extra,
	})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "grep")
	assert.Contains(t, stdout.String(), "mongosh")

	stderr.Reset()
	code = Main(context.Background(), "sh", []string{"missing.sh"}, &Options{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Dir: dir, Environ: []string{},
	})
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr.String(), "missing.sh: No such file or directory")

	code = Main(context.Background(), "sh", []string{"-c"}, &Options{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Dir: dir, Environ: []string{},
	})
	assert.Equal(t, 2, code, "-c without a command")
}

func TestStrftimeAndHelpers(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)
	assert.Equal(t, "2026-03-04 05:06:07 Wed Mar  4 063 % %x", strftime(ts, "%F %T %a %b %e %j %% %x"))
	assert.Equal(t, "4.0K", humanize(4096))
	assert.Equal(t, "12M", humanize(12*1024*1024))
	assert.Equal(t, "drwxr-x---", modeString(os.ModeDir|0o750))
	assert.Equal(t, `\(a\)b+`, breToERE(`(a)b\+`))

	d, err := parseDuration("1.5m")
	require.NoError(t, err)
	assert.Equal(t, 90*time.Second, d)

	_, err = parseDuration("-1")
	assert.Error(t, err)
}
