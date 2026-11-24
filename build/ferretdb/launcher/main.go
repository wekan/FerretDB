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

// Command launcher starts a packaged Node.js program in an image without a shell.
//
// A package laid out as
//
//	<dir>/<name>                   this launcher
//	<dir>/bin/node                 the Node.js runtime
//	<dir>/lib/<name>/<name>.js     the program
//
// is usually started by a "#!/bin/sh" script in place of the launcher. The
// distroless runtime image has no /bin/sh, so this static binary replaces that
// script: it execs bin/node with the program and passes every argument and the
// environment through unchanged, as the script's "exec" did.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// command returns the Node.js runtime path and its argv for the launcher at exe.
func command(exe string, args []string) (string, []string) {
	dir := filepath.Dir(exe)
	name := filepath.Base(exe)
	node := filepath.Join(dir, "bin", "node")
	script := filepath.Join(dir, "lib", name, name+".js")

	return node, append([]string{node, script}, args...)
}

func main() {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}

	node, argv := command(exe, os.Args[1:])

	err = syscall.Exec(node, argv, os.Environ())
	fmt.Fprintf(os.Stderr, "%s: cannot start %s: %v\n", filepath.Base(exe), node, err)
	os.Exit(127)
}
