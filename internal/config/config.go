// Package config provides a typed application configuration with
// deterministic precedence resolution: defaults are overridden by an
// environment variable, which is in turn overridden by a command-line
// flag (default -> env -> flag).
//
// Documented configuration keys:
//
//   - Bind address: env var VENOM_BIND, flag -bind, default "127.0.0.1:8081".
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

// Config is the typed application configuration.
type Config struct {
	// Bind is the HTTP listen address ("host:port"). Default
	// 127.0.0.1:8081; overridden by the VENOM_BIND environment variable,
	// which is overridden by the -bind command-line flag.
	Bind string
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

	fs := flag.NewFlagSet("venom", flag.ContinueOnError)
	flagVal := fs.String(flagBind, bind, "HTTP bind address (host:port)")
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("config: parse flags: %w", err)
	}
	bind = *flagVal

	if err := validateBind(bind); err != nil {
		return nil, err
	}

	return &Config{Bind: bind}, nil
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
