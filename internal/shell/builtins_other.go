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

//go:build !unix

package shell

import (
	"context"
	"fmt"
	"io/fs"
	"runtime"
)

// fileOwner returns the link count, owner and group of a file.
func fileOwner(fs.FileInfo) (uint64, string, string) { return 1, "-", "-" }

// diskUsage returns the space a file takes on disk.
func diskUsage(fi fs.FileInfo) int64 { return fi.Size() }

var (
	cmdPs   = unsupported("ps")
	cmdKill = unsupported("kill")
)

func cmdUname(_ context.Context, e *Env, _ []string) int {
	fmt.Fprintln(e.Stdout, runtime.GOOS)
	return 0
}
