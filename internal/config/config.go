// Package config provides a typed application configuration with
// deterministic precedence resolution: defaults are overridden by an
// environment variable, which is in turn overridden by a command-line
// flag (default -> env -> flag).
//
// Documented configuration keys:
//
//   - Bind address: env var VENOM_BIND, flag -bind, default "127.0.0.1:8081".
//   - Data-plane bind: env var VENOM_DATA_PLANE_BIND, flag -data-plane-bind,
//     default "" (empty = the public /v1 API shares the control listener).
package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
)

// defaultBind is used when neither VENOM_BIND nor -bind is set.
const defaultBind = "127.0.0.1:8081"

// envBind is the environment variable that overrides defaultBind.
// os.LookupEnv is called only within this package; forbidigo enforces
// that no other package reads environment variables directly.
const envBind = "VENOM_BIND"

// flagBind is the command-line flag that overrides both defaultBind and
// envBind.
const flagBind = "bind"

// envDataPlaneBind / flagDataPlaneBind configure the OPTIONAL public data-plane
// bind (01 §6b). Unlike Bind, this is empty by default: an empty value means
// the public /v1 API shares the control listener (the common local-only case),
// and it MAY be a non-loopback address (the data plane is the one surface the
// owner may expose off-host — the control plane never may).
const (
	envDataPlaneBind  = "VENOM_DATA_PLANE_BIND"
	flagDataPlaneBind = "data-plane-bind"
)

// Config is the typed application configuration.
type Config struct {
	// Bind is the HTTP listen address ("host:port"). Default
	// 127.0.0.1:8081; overridden by the VENOM_BIND environment variable,
	// which is overridden by the -bind command-line flag.
	Bind string

	// DataPlaneBind is the OPTIONAL public data-plane listen address
	// ("host:port"). Empty (the default) means the public /v1 API shares the
	// control listener. When set and different from Bind, Boot opens a second,
	// public-only listener there. It MAY be non-loopback (the control Bind may
	// never be). Overridden by VENOM_DATA_PLANE_BIND, then -data-plane-bind.
	DataPlaneBind string
}

// ErrInvalidBind is returned by Load when the resolved bind address is not
// a valid "host:port" value.
var ErrInvalidBind = errors.New("config: invalid bind address")

// Load resolves Config using default -> env -> flag precedence. args is
// typically os.Args[1:]; Load parses only the -bind flag from it and does
// not interpret subcommands.
func Load(args []string) (*Config, error) {
	bind := defaultBind
	if v, ok := os.LookupEnv(envBind); ok && v != "" {
		bind = v
	}

	dataPlaneBind := ""
	if v, ok := os.LookupEnv(envDataPlaneBind); ok && v != "" {
		dataPlaneBind = v
	}

	fs := flag.NewFlagSet("venom", flag.ContinueOnError)
	flagVal := fs.String(flagBind, bind, "HTTP bind address (host:port)")
	dataPlaneFlagVal := fs.String(flagDataPlaneBind, dataPlaneBind, "public data-plane bind (host:port); empty shares the control listener")
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("config: parse flags: %w", err)
	}
	bind = *flagVal
	dataPlaneBind = *dataPlaneFlagVal

	if err := validateBind(bind); err != nil {
		return nil, err
	}
	// The data-plane bind is optional; validate it only when set. It is
	// validated EXACTLY as Bind is (host:port shape) — but it is NOT subject to
	// any loopback requirement, since exposing the data plane off-host is a
	// supported owner choice (01 §6b).
	if dataPlaneBind != "" {
		if err := validateBind(dataPlaneBind); err != nil {
			return nil, err
		}
	}

	return &Config{Bind: bind, DataPlaneBind: dataPlaneBind}, nil
}

func validateBind(bind string) error {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrInvalidBind, bind, err)
	}
	if port == "" {
		return fmt.Errorf("%w: %q: empty port", ErrInvalidBind, bind)
	}
	if host != "" && net.ParseIP(host) == nil {
		return fmt.Errorf("%w: %q: host is not a valid IP", ErrInvalidBind, bind)
	}
	return nil
}
