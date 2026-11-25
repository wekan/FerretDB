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

//go:build !ferretdb_no_shell

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/FerretDB/FerretDB/internal/mongosh"
	"github.com/FerretDB/FerretDB/internal/shell"
)

// shellCommands are the commands the command shell adds to its own
// built-ins: mongosh runs in-process, and ferretdb runs this same binary.
var shellCommands map[string]shell.Command

func init() {
	shellCommands = map[string]shell.Command{
		"mongosh": func(ctx context.Context, e *shell.Env, args []string) int {
			return mongosh.Main(ctx, args[1:], mongosh.IO{
				Stdin:  e.Stdin,
				Stdout: e.Stdout,
				Stderr: e.Stderr,
				Getenv: e.Getenv,
			})
		},
		"ferretdb": func(ctx context.Context, e *shell.Env, args []string) int {
			// `ferretdb mongosh` and `ferretdb sh` stay in-process.
			if len(args) > 1 && multicallTools[args[1]] {
				if args[1] == "mongosh" {
					return mongosh.Main(ctx, args[2:], mongosh.IO{
						Stdin: e.Stdin, Stdout: e.Stdout, Stderr: e.Stderr, Getenv: e.Getenv,
					})
				}

				return shell.Main(ctx, args[1], args[2:], &shell.Options{
					Stdin: e.Stdin, Stdout: e.Stdout, Stderr: e.Stderr,
					Environ: e.Environ, Dir: e.Dir, Commands: shellCommands,
				})
			}

			// Anything else (the server, ping, --version) runs this binary.
			self, err := os.Executable()
			if err != nil {
				fmt.Fprintf(e.Stderr, "ferretdb: %s\n", err)
				return 127
			}

			cmd := exec.CommandContext(ctx, self, args[1:]...)
			cmd.Args[0] = "ferretdb"
			cmd.Stdin, cmd.Stdout, cmd.Stderr = e.Stdin, e.Stdout, e.Stderr
			cmd.Dir, cmd.Env = e.Dir, e.Environ

			if err := cmd.Run(); err != nil {
				if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec returns it unwrapped
					return ee.ExitCode()
				}

				fmt.Fprintf(e.Stderr, "ferretdb: %s\n", err)

				return 126
			}

			return 0
		},
	}
}

// runShell runs the command shell as name (sh or bash).
func runShell(ctx context.Context, name string, args []string) int {
	return shell.Main(ctx, name, args, &shell.Options{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Commands: shellCommands,
	})
}
