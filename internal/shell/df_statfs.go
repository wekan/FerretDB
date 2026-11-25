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

//go:build linux || darwin || freebsd

package shell

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// mounts returns the mount points and their devices.
func mounts() [][2]string {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return [][2]string{{"rootfs", "/"}}
	}
	defer f.Close() //nolint:errcheck // read-only

	var res [][2]string

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}

		switch fields[2] {
		case "proc", "sysfs", "cgroup", "cgroup2", "devpts", "mqueue", "securityfs", "debugfs", "tracefs",
			"pstore", "bpf", "configfs", "fusectl", "binfmt_misc", "autofs", "hugetlbfs", "nsfs", "rpc_pipefs":
			continue
		}

		res = append(res, [2]string{fields[0], strings.ReplaceAll(fields[1], `\040`, " ")})
	}

	return res
}

func cmdDf(_ context.Context, e *Env, args []string) int {
	flags, operands, err := parseFlags("df", args[1:], "hkPTa", "")
	if err != nil {
		return usageErr(e, err, 1)
	}

	type row struct{ dev, mount string }

	var rows []row

	if len(operands) == 0 {
		for _, m := range mounts() {
			rows = append(rows, row{m[0], m[1]})
		}
	} else {
		all := mounts()

		for _, o := range operands {
			p, err := filepath.Abs(e.Path(o))
			if err != nil {
				return fail(e, "df", "%s: %s", o, cleanErr(err))
			}

			if _, err := os.Stat(p); err != nil {
				return fail(e, "df", "%s: %s", o, cleanErr(err))
			}

			best := row{"-", p}
			for _, m := range all {
				if (p == m[1] || strings.HasPrefix(p, strings.TrimSuffix(m[1], "/")+"/")) && len(m[1]) >= len(best.mount) || best.dev == "-" && m[1] == "/" {
					best = row{m[0], m[1]}
				}
			}

			rows = append(rows, row{best.dev, best.mount})
		}
	}

	size := func(n uint64) string {
		if flags.has('h') {
			return humanize(int64(n))
		}

		return strconv.FormatUint(n/1024, 10)
	}

	header := "1K-blocks"
	if flags.has('h') {
		header = "Size"
	}

	fmt.Fprintf(e.Stdout, "%-20s %10s %10s %10s %4s %s\n", "Filesystem", header, "Used", "Available", "Use%", "Mounted on")

	status := 0

	for _, r := range rows {
		var st syscall.Statfs_t
		if err := syscall.Statfs(r.mount, &st); err != nil {
			status = fail(e, "df", "%s: %s", r.mount, cleanErr(err))
			continue
		}

		bsize := uint64(st.Bsize)                                                                      //nolint:unconvert,gosec // the type differs between platforms
		total, free, avail := uint64(st.Blocks)*bsize, uint64(st.Bfree)*bsize, uint64(st.Bavail)*bsize //nolint:unconvert,lll // the types differ

		if total == 0 && !flags.has('a') && len(operands) == 0 {
			continue
		}

		used := total - free

		pct := "-"
		if used+avail > 0 {
			pct = strconv.FormatUint((used*100+used+avail-1)/(used+avail), 10) + "%"
		}

		fmt.Fprintf(e.Stdout, "%-20s %10s %10s %10s %4s %s\n", r.dev, size(total), size(used), size(avail), pct, r.mount)
	}

	return status
}
