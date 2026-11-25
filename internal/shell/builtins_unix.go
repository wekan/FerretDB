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

//go:build unix

package shell

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// fileOwner returns the link count, owner and group of a file.
func fileOwner(fi fs.FileInfo) (uint64, string, string) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 1, "?", "?"
	}

	owner, group := strconv.FormatUint(uint64(st.Uid), 10), strconv.FormatUint(uint64(st.Gid), 10)

	if u, err := user.LookupId(owner); err == nil {
		owner = u.Username
	}

	if g, err := user.LookupGroupId(group); err == nil {
		group = g.Name
	}

	return uint64(st.Nlink), owner, group //nolint:unconvert // the type differs between platforms
}

// diskUsage returns the space a file takes on disk.
func diskUsage(fi fs.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512 //nolint:unconvert // the type differs between platforms
	}

	return fi.Size()
}

// clockTicks is the kernel's USER_HZ, 100 on every Linux architecture.
const clockTicks = 100

func cmdPs(_ context.Context, e *Env, args []string) int {
	if runtime.GOOS != "linux" {
		return fail(e, "ps", "not supported on %s", runtime.GOOS)
	}

	_ = args // -e, -f, aux: every process is always listed

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return fail(e, "ps", "%s", cleanErr(err))
	}

	var pids []int

	for _, de := range entries {
		if pid, err := strconv.Atoi(de.Name()); err == nil {
			pids = append(pids, pid)
		}
	}

	sort.Ints(pids)

	fmt.Fprintf(e.Stdout, "%5s %-8s %8s %s\n", "PID", "USER", "TIME", "COMMAND")

	users := map[string]string{}

	for _, pid := range pids {
		dir := "/proc/" + strconv.Itoa(pid)

		stat, err := os.ReadFile(dir + "/stat")
		if err != nil {
			continue
		}

		// pid (comm) state ppid ... utime(14) stime(15)
		s := string(stat)
		open, closing := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')

		if open < 0 || closing < open {
			continue
		}

		comm := s[open+1 : closing]
		fields := strings.Fields(s[closing+1:])

		var cpu time.Duration

		if len(fields) > 12 {
			ut, _ := strconv.ParseInt(fields[11], 10, 64)
			st, _ := strconv.ParseInt(fields[12], 10, 64)
			cpu = time.Duration(ut+st) * time.Second / clockTicks
		}

		uid := "?"
		if status, err := os.ReadFile(dir + "/status"); err == nil {
			for _, line := range strings.Split(string(status), "\n") {
				if f := strings.Fields(line); len(f) > 1 && f[0] == "Uid:" {
					uid = f[1]
				}
			}
		}

		name, ok := users[uid]
		if !ok {
			name = uid
			if u, err := user.LookupId(uid); err == nil {
				name = u.Username
			}

			users[uid] = name
		}

		cmd := comm
		if b, err := os.ReadFile(dir + "/cmdline"); err == nil && len(b) > 0 {
			cmd = strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
		} else {
			cmd = "[" + comm + "]"
		}

		mins := int(cpu.Minutes())
		fmt.Fprintf(e.Stdout, "%5d %-8s %5d:%02d %s\n", pid, name, mins, int(cpu.Seconds())%60, cmd)
	}

	return 0
}

// signals are the names kill accepts.
var signals = map[string]syscall.Signal{
	"HUP": syscall.SIGHUP, "INT": syscall.SIGINT, "QUIT": syscall.SIGQUIT, "KILL": syscall.SIGKILL,
	"USR1": syscall.SIGUSR1, "USR2": syscall.SIGUSR2, "TERM": syscall.SIGTERM, "CONT": syscall.SIGCONT,
	"STOP": syscall.SIGSTOP, "TSTP": syscall.SIGTSTP,
}

func cmdKill(_ context.Context, e *Env, args []string) int {
	sig := syscall.SIGTERM
	rest := args[1:]

	if len(rest) > 0 && rest[0] == "-l" {
		var names []string
		for n := range signals {
			names = append(names, n)
		}

		sort.Strings(names)
		fmt.Fprintln(e.Stdout, strings.Join(names, " "))

		return 0
	}

	if len(rest) > 1 && rest[0] == "-s" {
		rest = append([]string{"-" + rest[1]}, rest[2:]...)
	}

	if len(rest) > 0 && strings.HasPrefix(rest[0], "-") && rest[0] != "--" {
		name := strings.TrimPrefix(strings.ToUpper(rest[0][1:]), "SIG")
		if n, err := strconv.Atoi(name); err == nil {
			sig = syscall.Signal(n)
		} else if s, ok := signals[name]; ok {
			sig = s
		} else {
			return fail(e, "kill", "invalid signal '%s'", rest[0][1:])
		}

		rest = rest[1:]
	}

	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}

	if len(rest) == 0 {
		return fail(e, "kill", "usage: kill [-s sigspec | -signum | -sigspec] pid ... or kill -l")
	}

	status := 0

	for _, p := range rest {
		pid, err := strconv.Atoi(p)
		if err != nil {
			status = fail(e, "kill", "%s: arguments must be process IDs", p)
			continue
		}

		if err := syscall.Kill(pid, sig); err != nil {
			status = fail(e, "kill", "(%d) - %s", pid, cleanErr(err))
		}
	}

	return status
}

func cmdUname(_ context.Context, e *Env, args []string) int {
	flags, _, err := parseFlags("uname", args[1:], "asnrvmpio", "")
	if err != nil {
		return usageErr(e, err, 1)
	}

	host, _ := os.Hostname()
	kernel := strings.ToUpper(runtime.GOOS[:1]) + runtime.GOOS[1:]

	release := ""
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		release = strings.TrimSpace(string(b))
	}

	ver := ""
	if b, err := os.ReadFile("/proc/sys/kernel/version"); err == nil {
		ver = strings.TrimSpace(string(b))
	}

	machine := map[string]string{
		"amd64": "x86_64", "arm64": "aarch64", "386": "i686", "arm": "armv7l",
	}[runtime.GOARCH]
	if machine == "" {
		machine = runtime.GOARCH
	}

	all := flags.has('a')

	var parts []string

	add := func(f rune, v string, def bool) {
		if all || flags.has(f) || (def && len(flags) == 0) {
			if v != "" {
				parts = append(parts, v)
			}
		}
	}

	add('s', kernel, true)
	add('n', host, false)
	add('r', release, false)
	add('v', ver, false)
	add('m', machine, false)
	add('o', map[string]string{"linux": "GNU/Linux"}[runtime.GOOS], false)

	fmt.Fprintln(e.Stdout, strings.Join(parts, " "))

	return 0
}
