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
	"fmt"
	"net/url"
	"os"
	"strings"
)

// parseURI checks given SQLite URI and returns a parsed form.
//
// URI should contain 'file' scheme and point to an existing directory.
// Path should end with '/'. Authority should be empty or absent.
//
// Returned URL contains path in both Path and Opaque to make String() method work correctly.
// Callers should use Path.
func parseURI(u string) (*url.URL, error) {
	uri, err := url.Parse(u)
	if err != nil {
		return nil, err
	}

	if uri.Scheme != "file" {
		return nil, fmt.Errorf(`expected "file:" schema, got %q`, uri.Scheme)
	}

	if uri.User != nil {
		return nil, fmt.Errorf(`expected empty user info, got %q`, uri.User)
	}

	if uri.Host != "" {
		return nil, fmt.Errorf(`expected empty host, got %q`, uri.Host)
	}

	if uri.Path == "" {
		uri.Path = uri.Opaque
	}
	uri.Opaque = uri.Path
	uri.RawPath = ""
	uri.OmitHost = true

	values := uri.Query()

	// it is deprecated and interacts weirdly with database/sql.Pool
	if values.Get("cache") == "shared" {
		return nil, fmt.Errorf(`shared cache is not supported`)
	}

	setDefaultValues(values)
	uri.RawQuery = values.Encode()

	if !strings.HasSuffix(uri.Path, "/") {
		return nil, fmt.Errorf(`expected path ending with "/", got %q`, uri.Path)
	}

	fi, err := os.Stat(uri.Path)
	if err != nil {
		return nil, fmt.Errorf(`%q should be an existing directory, got %s`, uri.Path, err)
	}

	if !fi.IsDir() {
		return nil, fmt.Errorf(`%q should be an existing directory`, uri.Path)
	}

	return uri, nil
}

// setDefaultValue sets default query parameters.
//
// Every default is only added when the operator has NOT already supplied a
// _pragma with the same name, so an explicit operator setting always wins.
func setDefaultValues(values url.Values) {
	var autoVacuum, busyTimeout, journalMode, synchronous, cacheSize, mmapSize, tempStore bool

	for _, v := range values["_pragma"] {
		if strings.HasPrefix(v, "auto_vacuum") {
			autoVacuum = true
		}

		if strings.HasPrefix(v, "busy_timeout") {
			busyTimeout = true
		}

		if strings.HasPrefix(v, "journal_mode") {
			journalMode = true
		}

		if strings.HasPrefix(v, "synchronous") {
			synchronous = true
		}

		if strings.HasPrefix(v, "cache_size") {
			cacheSize = true
		}

		if strings.HasPrefix(v, "mmap_size") {
			mmapSize = true
		}

		if strings.HasPrefix(v, "temp_store") {
			tempStore = true
		}
	}

	// keep it in sync with docs

	// the order is important: busy handler must be set before WAL is enabled

	// 30s (was 10s): under a heavy concurrent write load — e.g. a large 6.09 ->
	// v1 migration inserting hundreds of thousands of cards/activities while users
	// log in — a login's UPDATE could not get the write lock within 10s and failed
	// with SQLITE_BUSY ("database is locked (5)"), so Sign In did nothing (#6480).
	// FerretDB relies on busy_timeout instead of explicit retries (see sqlite.go),
	// so give the busy handler more room to let contended writes through. An
	// operator-supplied busy_timeout _pragma still wins.
	if !busyTimeout {
		values.Add("_pragma", "busy_timeout(30000)")
	}

	if !journalMode {
		values.Add("_pragma", "journal_mode(wal)")
	}

	// WeKan #6480: SQLite performance remediation. Users reported FerretDB sitting
	// above 100% CPU with everything after the login screen extremely slow. These
	// defaults cut the disk I/O and fsync overhead that drives that CPU:
	//
	//   - synchronous=NORMAL is crash-safe under WAL (no corruption; at worst the
	//     last committed transaction is lost on power loss) and removes an fsync
	//     per commit — the single biggest write-path win.
	//   - a larger page cache and memory-mapped I/O keep WeKan's hot pages resident
	//     so repeated reads stop hitting the disk, cutting CPU on read-heavy loads.
	//   - temp tables/indexes in memory speed up sorts and index builds.
	//
	// Each is only a DEFAULT: an operator-supplied _pragma of the same name wins.
	if !synchronous {
		values.Add("_pragma", "synchronous(normal)")
	}

	if !cacheSize {
		// negative = size in KiB; -16384 = 16 MiB per connection (SQLite default ~2 MiB).
		values.Add("_pragma", "cache_size(-16384)")
	}

	if !mmapSize {
		values.Add("_pragma", "mmap_size(134217728)") // 128 MiB memory-mapped I/O
	}

	if !tempStore {
		values.Add("_pragma", "temp_store(memory)")
	}

	if !autoVacuum {
		values.Add("_pragma", "auto_vacuum(none)") // TODO https://github.com/FerretDB/FerretDB/issues/3612
	}
}
