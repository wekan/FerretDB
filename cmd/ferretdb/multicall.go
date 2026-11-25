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
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"os/signal"

	"github.com/FerretDB/FerretDB/internal/mongosh"
)

// The ferretdb binary is also a MongoDB-compatible shell and a command
// shell, the way BusyBox is many programs: what it does depends on the name
// it was started as (argv[0]), so an image can link /usr/bin/mongosh,
// /bin/sh and /bin/bash to it, or on its first argument
// (`ferretdb mongosh ...`, `ferretdb sh ...`).

// multicallTools are the names that select something other than the server.
var multicallTools = map[string]bool{
	"mongosh": true,
	"sh":      true,
	"bash":    true,
}

// selectTool returns the tool to run for the command line and its
// arguments, or "" for the server itself.
func selectTool(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}

	// filepath.Base does not split on '\' outside Windows; argv[0] does not
	// contain one there.
	name := strings.TrimSuffix(filepath.Base(args[0]), ".exe")
	if multicallTools[name] {
		return name, args[1:]
	}

	if len(args) > 1 && multicallTools[args[1]] {
		return args[1], args[2:]
	}

	return "", nil
}

// runTool runs a tool selected by selectTool and returns its exit code.
func runTool(tool string, args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	switch tool {
	case "mongosh":
		return mongosh.Main(ctx, args, mongosh.IO{
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			Getenv: os.Getenv,
		})
	default:
		return runShell(ctx, tool, args)
	}
}
