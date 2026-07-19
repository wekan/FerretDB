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

package pool

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultPragmas is the URL-encoded _pragma query that setDefaultValues adds when
// the operator supplies none, in insertion order. Kept in one place so adding a
// performance pragma is a single edit here (see setDefaultValues in uri.go).
const defaultPragmas = "_pragma=busy_timeout%2830000%29" +
	"&_pragma=journal_mode%28wal%29" +
	"&_pragma=synchronous%28normal%29" +
	"&_pragma=cache_size%28-16384%29" +
	"&_pragma=mmap_size%28134217728%29" +
	"&_pragma=temp_store%28memory%29" +
	"&_pragma=auto_vacuum%28none%29"

func TestParseURI(t *testing.T) {
	t.Parallel()

	// tests rely on the fact that both ./tmp and /tmp exist

	err := os.MkdirAll("tmp/dir", 0o777)
	require.NoError(t, err)

	_, err = os.Create("tmp/file")
	require.NoError(t, err)

	t.Cleanup(func() {
		err := os.RemoveAll("tmp")
		require.NoError(t, err)
	})

	testCases := map[string]struct {
		in  string
		uri *url.URL
		out string
		err string
	}{
		"LocalDirectory": {
			in: "file:./",
			uri: &url.URL{
				Scheme:   "file",
				Opaque:   "./",
				Path:     "./",
				OmitHost: true,
				RawQuery: defaultPragmas,
			},
			out: "file:./?" + defaultPragmas,
		},
		"LocalSubDirectory": {
			in: "file:./tmp/",
			uri: &url.URL{
				Scheme:   "file",
				Opaque:   "./tmp/",
				Path:     "./tmp/",
				OmitHost: true,
				RawQuery: defaultPragmas,
			},
			out: "file:./tmp/?" + defaultPragmas,
		},
		"LocalSubSubDirectory": {
			in: "file:./tmp/dir/",
			uri: &url.URL{
				Scheme:   "file",
				Opaque:   "./tmp/dir/",
				Path:     "./tmp/dir/",
				OmitHost: true,
				RawQuery: defaultPragmas,
			},
			out: "file:./tmp/dir/?" + defaultPragmas,
		},
		"LocalDirectoryWithParameters": {
			in: "file:./tmp/?mode=memory",
			uri: &url.URL{
				Scheme:   "file",
				Opaque:   "./tmp/",
				Path:     "./tmp/",
				OmitHost: true,
				RawQuery: defaultPragmas + "&mode=memory",
			},
			out: "file:./tmp/?" + defaultPragmas + "&mode=memory",
		},
		"AbsoluteDirectory": {
			in: "file:/tmp/",
			uri: &url.URL{
				Scheme:   "file",
				Opaque:   "/tmp/",
				Path:     "/tmp/",
				OmitHost: true,
				RawQuery: defaultPragmas,
			},
			out: "file:/tmp/?" + defaultPragmas,
		},
		"WithEmptyAuthority": {
			in: "file:///tmp/",
			uri: &url.URL{
				Scheme:   "file",
				Opaque:   "/tmp/",
				Path:     "/tmp/",
				OmitHost: true,
				RawQuery: defaultPragmas,
			},
			out: "file:/tmp/?" + defaultPragmas,
		},
		"WithEmptyAuthorityAndQuery": {
			in: "file:///tmp/?mode=memory",
			uri: &url.URL{
				Scheme:   "file",
				Opaque:   "/tmp/",
				Path:     "/tmp/",
				OmitHost: true,
				RawQuery: defaultPragmas + "&mode=memory",
			},
			out: "file:/tmp/?" + defaultPragmas + "&mode=memory",
		},
		"HostIsNotEmpty": {
			in:  "file://localhost/./tmp/?mode=memory",
			err: `expected empty host, got "localhost"`,
		},
		"UserIsNotEmpty": {
			in:  "file://user:pass@./tmp/?mode=memory",
			err: `expected empty user info, got "user:pass"`,
		},
		"NoDirectory": {
			in:  "file:./nodir/",
			err: `"./nodir/" should be an existing directory, got stat ./nodir/: no such file or directory`,
		},
		"PathIsNotEndsWithSlash": {
			in:  "file:./tmp/file",
			err: `expected path ending with "/", got "./tmp/file"`,
		},
		"FileInsteadOfDirectory": {
			in:  "file:./tmp/file/",
			err: `"./tmp/file/" should be an existing directory, got stat ./tmp/file/: not a directory`,
		},
		"MalformedURI": {
			in:  ":./tmp/",
			err: `parse ":./tmp/": missing protocol scheme`,
		},
		"NoScheme": {
			in:  "./tmp/",
			err: `expected "file:" schema, got ""`,
		},
		"Shared": {
			in:  "file:./?cache=shared",
			err: `shared cache is not supported`,
		},
		"SharedMemory": {
			in:  "file:./?mode=memory&cache=shared",
			err: `shared cache is not supported`,
		},
	}
	for name, tc := range testCases {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			u, err := parseURI(tc.in)
			if tc.err != "" {
				assert.EqualError(t, err, tc.err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.uri, u)
			assert.Equal(t, tc.out, u.String())
		})
	}
}

// TestSetDefaultValues checks that all performance/safety pragmas are applied by
// default, and — negative tests — that an operator-supplied _pragma of the same
// name is never duplicated by a default (the operator value wins). See #6480.
func TestSetDefaultValues(t *testing.T) {
	t.Parallel()

	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()

		v := url.Values{}
		setDefaultValues(v)

		for _, want := range []string{
			"busy_timeout(30000)",
			"journal_mode(wal)",
			"synchronous(normal)",
			"cache_size(-16384)",
			"mmap_size(134217728)",
			"temp_store(memory)",
			"auto_vacuum(none)",
		} {
			assert.Contains(t, v["_pragma"], want)
		}
	})

	overrides := map[string]string{
		"BusyTimeout": "busy_timeout(60000)",
		"JournalMode": "journal_mode(delete)",
		"Synchronous": "synchronous(full)",
		"CacheSize":   "cache_size(-1)",
		"MmapSize":    "mmap_size(0)",
		"TempStore":   "temp_store(file)",
	}
	for name, override := range overrides {
		name, override := name, override
		t.Run("Override"+name, func(t *testing.T) {
			t.Parallel()

			v := url.Values{"_pragma": {override}}
			setDefaultValues(v)

			key := override[:strings.IndexByte(override, '(')]

			var count int
			for _, p := range v["_pragma"] {
				if strings.HasPrefix(p, key) {
					count++
				}
			}

			assert.Equal(t, 1, count, "pragma %q must not be duplicated by a default", key)
			assert.Contains(t, v["_pragma"], override)
		})
	}
}
