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

package mongosh

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
)

// options are the parsed command-line arguments.
type cliOptions struct {
	address        string // positional: a URI, host[:port][/db] or a database name
	host           string
	port           string
	username       string
	password       string
	passwordPrompt bool
	authDB         string
	authMechanism  string
	evals          []string
	files          []string
	quiet          bool
	json           string // "", "relaxed" or "canonical"
	nodb           bool
	shell          bool
	version        bool
	help           bool
	tls            bool
	tlsCAFile      string
	tlsCertKeyFile string
	tlsInsecure    bool
	tlsNoHostCheck bool
}

// usage is printed by --help.
const usage = `Usage:
  mongosh [options] [URI | host[:port][/db] | db] [file.js ...]

A MongoDB-compatible shell built into FerretDB: JavaScript (no Node.js
modules) and the common mongosh commands and methods.

Options:
  --host <host>                     server host (default: from FERRETDB_LISTEN_ADDR)
  --port <port>                     server port
  -u, --username <name>             user name
  -p, --password [<password>]       password (prompted for when not given)
  --authenticationDatabase <db>     database the user is defined in
  --authenticationMechanism <mech>  SCRAM-SHA-256, SCRAM-SHA-1 or PLAIN
  --tls, --tlsCAFile <file>, --tlsCertificateKeyFile <file>,
  --tlsAllowInvalidCertificates, --tlsAllowInvalidHostnames
  --eval <javascript>               evaluate, print the result and exit
  --json[=relaxed|canonical]        print the --eval result as Extended JSON
  -f, --file <file.js>              run a script file
  --shell                           stay interactive after --eval and files
  --nodb                            do not connect to a server
  --quiet                           do not print the connection banner
  --norc                            accepted and ignored
  --version                         print the version and exit
  -h, --help                        print this help and exit
`

// parseArgs parses mongosh arguments (without the program name).
func parseArgs(args []string) (*cliOptions, error) {
	o := new(cliOptions)

	for i := 0; i < len(args); i++ {
		arg := args[i]

		name, value, hasValue := arg, "", false
		if strings.HasPrefix(arg, "--") {
			if n, v, ok := strings.Cut(arg, "="); ok {
				name, value, hasValue = n, v, true
			}
		}

		next := func() (string, error) {
			if hasValue {
				return value, nil
			}

			if i+1 >= len(args) {
				return "", fmt.Errorf("option %s requires a value", name)
			}

			i++

			return args[i], nil
		}

		var err error

		switch name {
		case "--host":
			o.host, err = next()
		case "--port":
			o.port, err = next()
		case "-u", "--username":
			o.username, err = next()
		case "-p", "--password":
			switch {
			case hasValue:
				o.password = value
			case i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
				i++
				o.password = args[i]
			default:
				o.passwordPrompt = true
			}
		case "--authenticationDatabase":
			o.authDB, err = next()
		case "--authenticationMechanism":
			o.authMechanism, err = next()
		case "--eval":
			var e string
			e, err = next()
			o.evals = append(o.evals, e)
		case "-f", "--file":
			var f string
			f, err = next()
			o.files = append(o.files, f)
		case "--quiet":
			o.quiet = true
		case "--json":
			o.json = "relaxed"
			if hasValue {
				if value != "relaxed" && value != "canonical" {
					return nil, fmt.Errorf("--json must be relaxed or canonical, not %q", value)
				}

				o.json = value
			}
		case "--nodb":
			o.nodb = true
		case "--shell":
			o.shell = true
		case "--norc", "--verbose":
		case "--version":
			o.version = true
		case "-h", "--help":
			o.help = true
		case "--tls", "--ssl":
			o.tls = true
		case "--tlsCAFile":
			o.tlsCAFile, err = next()
		case "--tlsCertificateKeyFile":
			o.tlsCertKeyFile, err = next()
		case "--tlsAllowInvalidCertificates":
			o.tlsInsecure = true
		case "--tlsAllowInvalidHostnames":
			o.tlsNoHostCheck = true
		default:
			switch {
			case strings.HasPrefix(arg, "-"):
				return nil, fmt.Errorf("unknown option %s", arg)
			case strings.HasSuffix(arg, ".js") && !strings.HasPrefix(arg, "mongodb"):
				o.files = append(o.files, arg)
			case o.address == "":
				o.address = arg
			default:
				return nil, fmt.Errorf("unexpected argument %q: only one connection address is accepted", arg)
			}
		}

		if err != nil {
			return nil, err
		}
	}

	if o.json != "" && len(o.evals) == 0 {
		return nil, fmt.Errorf("--json requires --eval")
	}

	return o, nil
}

// defaultHostPort is where a bare mongosh connects: the server's own
// listen address (FERRETDB_LISTEN_ADDR), with a wildcard host replaced by
// the loopback address.
func defaultHostPort(getenv func(string) string) string {
	addr := getenv("FERRETDB_LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:27017"
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}

	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}

	return net.JoinHostPort(host, port)
}

// connectionURI returns the URI to connect to and the database to use.
func (o *cliOptions) connectionURI(getenv func(string) string) (string, string, error) {
	var uri string

	db := "test"

	switch addr := o.address; {
	case strings.HasPrefix(addr, "mongodb://") || strings.HasPrefix(addr, "mongodb+srv://"):
		if o.host != "" || o.port != "" {
			return "", "", fmt.Errorf("--host and --port cannot be used with a connection URI")
		}

		uri = addr
	default:
		hostPort := defaultHostPort(getenv)

		if getenv("FERRETDB_LISTEN_ADDR") == "" && getenv("FERRETDB_LISTEN_UNIX") != "" {
			hostPort = url.PathEscape(getenv("FERRETDB_LISTEN_UNIX"))
		}

		if addr != "" {
			hp, d, hasSlash := strings.Cut(addr, "/")

			switch {
			case hasSlash:
				if hp != "" {
					hostPort = hp
				}

				if d != "" {
					db = d
				}
			case strings.Contains(addr, ":") || strings.Contains(addr, "."):
				hostPort = addr
			default:
				db = addr
			}
		}

		if o.host != "" || o.port != "" {
			host, port, err := net.SplitHostPort(hostPort)
			if err != nil {
				host, port = hostPort, "27017"
			}

			if o.host != "" {
				host = o.host
				if h, p, err := net.SplitHostPort(o.host); err == nil {
					host, port = h, p
				}
			}

			if o.port != "" {
				port = o.port
			}

			hostPort = net.JoinHostPort(host, port)
		}

		uri = "mongodb://" + hostPort + "/" + url.PathEscape(db)
	}

	cs, err := connstring.Parse(uri)
	if err != nil {
		return "", "", err
	}

	if cs.Database != "" {
		db = cs.Database
	}

	var params []string

	if !cs.DirectConnectionSet && cs.ReplicaSet == "" && cs.Scheme != "mongodb+srv" && len(cs.Hosts) == 1 {
		params = append(params, "directConnection=true")
	}

	if cs.AppName == "" {
		params = append(params, "appName=mongosh")
	}

	if o.tls {
		params = append(params, "tls=true")
	}

	if o.tlsCAFile != "" {
		params = append(params, "tlsCAFile="+url.QueryEscape(o.tlsCAFile))
	}

	if o.tlsCertKeyFile != "" {
		params = append(params, "tlsCertificateKeyFile="+url.QueryEscape(o.tlsCertKeyFile))
	}

	if o.tlsInsecure {
		params = append(params, "tlsAllowInvalidCertificates=true")
	}

	if o.tlsNoHostCheck {
		params = append(params, "tlsAllowInvalidHostnames=true")
	}

	if len(params) > 0 {
		sep := "?"
		if strings.Contains(uri, "?") {
			sep = "&"
		} else if !strings.Contains(strings.TrimPrefix(strings.TrimPrefix(uri, "mongodb+srv://"), "mongodb://"), "/") {
			sep = "/?"
		}

		uri += sep + strings.Join(params, "&")
	}

	if _, err := connstring.ParseAndValidate(uri); err != nil {
		return "", "", err
	}

	return uri, db, nil
}

// redact removes the password from a URI for printing.
func redact(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return "<invalid URI>"
	}

	return u.Redacted()
}
