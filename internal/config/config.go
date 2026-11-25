// Package config loads and validates the process configuration once at start.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Level is how far a tool may change new-api. Levels are cumulative: a
// process allowed to operate may also read.
type Level int

const (
	LevelRead    Level = iota // reads only
	LevelOperate              // reversible operations: enable, disable, test, reset counters
	LevelWrite                // configuration changes, always previewed then confirmed
)

func (l Level) String() string {
	switch l {
	case LevelRead:
		return "read"
	case LevelOperate:
		return "operate"
	case LevelWrite:
		return "write"
	}
	return "unknown"
}

// ParseLevel parses the MCP_ALLOW value.
func ParseLevel(raw string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "read":
		return LevelRead, nil
	case "operate":
		return LevelOperate, nil
	case "write":
		return LevelWrite, nil
	}
	return 0, fmt.Errorf("MCP_ALLOW must be read, operate or write, got %q", raw)
}

const (
	TransportHTTP  = "http"
	TransportStdio = "stdio"

	minConfirmSecretBytes = 32
)

// Config is immutable after Load and passed by value.
type Config struct {
	BaseURL         *url.URL
	Transport       string
	PAT             string // stdio only; HTTP callers bring their own
	Listen          string
	MaxLevel        Level
	Toolsets        []string // empty means every toolset
	ConfirmSecret   []byte
	UpstreamRPS     float64
	UpstreamBurst   int
	UpstreamTimeout time.Duration
	LogLevel        string
}

// Load reads the configuration through getenv, so tests can pass a map.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	cfg := Config{
		Transport:       strings.ToLower(strings.TrimSpace(getenv("MCP_TRANSPORT"))),
		PAT:             strings.TrimSpace(getenv("NEWAPI_PAT")),
		Listen:          strings.TrimSpace(getenv("MCP_LISTEN")),
		ConfirmSecret:   []byte(getenv("MCP_CONFIRM_SECRET")),
		UpstreamRPS:     1.5,
		UpstreamBurst:   10,
		UpstreamTimeout: 30 * time.Second,
		LogLevel:        strings.ToLower(strings.TrimSpace(getenv("MCP_LOG_LEVEL"))),
	}

	base, err := parseBaseURL(getenv("NEWAPI_BASE_URL"))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.BaseURL = base

	if cfg.Transport == "" {
		cfg.Transport = TransportHTTP
	}
	switch cfg.Transport {
	case TransportHTTP:
		if cfg.PAT != "" {
			errs = append(errs, errors.New("NEWAPI_PAT must not be set in http mode; each caller sends its own token"))
		}
	case TransportStdio:
		if !strings.HasPrefix(cfg.PAT, "nap_") {
			errs = append(errs, errors.New("NEWAPI_PAT is required in stdio mode and must be a new-api access token (nap_...)"))
		}
	default:
		errs = append(errs, fmt.Errorf("MCP_TRANSPORT must be http or stdio, got %q", cfg.Transport))
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}

	cfg.MaxLevel, err = ParseLevel(getenv("MCP_ALLOW"))
	if err != nil {
		errs = append(errs, err)
	}
	// An unset secret is generated at start; a set one must be strong.
	if len(cfg.ConfirmSecret) > 0 && len(cfg.ConfirmSecret) < minConfirmSecretBytes {
		errs = append(errs, fmt.Errorf("MCP_CONFIRM_SECRET must be at least %d bytes when set", minConfirmSecretBytes))
	}

	for name := range strings.SplitSeq(getenv("MCP_TOOLSETS"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			cfg.Toolsets = append(cfg.Toolsets, name)
		}
	}

	if raw := strings.TrimSpace(getenv("MCP_UPSTREAM_RPS")); raw != "" {
		rps, err := strconv.ParseFloat(raw, 64)
		if err != nil || rps <= 0 {
			errs = append(errs, fmt.Errorf("MCP_UPSTREAM_RPS must be a positive number, got %q", raw))
		}
		cfg.UpstreamRPS = rps
	}
	if raw := strings.TrimSpace(getenv("MCP_UPSTREAM_BURST")); raw != "" {
		burst, err := strconv.Atoi(raw)
		if err != nil || burst < 1 {
			errs = append(errs, fmt.Errorf("MCP_UPSTREAM_BURST must be a positive integer, got %q", raw))
		}
		cfg.UpstreamBurst = burst
	}
	if raw := strings.TrimSpace(getenv("MCP_UPSTREAM_TIMEOUT")); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil || timeout <= 0 {
			errs = append(errs, fmt.Errorf("MCP_UPSTREAM_TIMEOUT must be a positive duration, got %q", raw))
		}
		cfg.UpstreamTimeout = timeout
	}
	switch cfg.LogLevel {
	case "":
		cfg.LogLevel = "info"
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("MCP_LOG_LEVEL must be debug, info, warn or error, got %q", cfg.LogLevel))
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("NEWAPI_BASE_URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("NEWAPI_BASE_URL must be an http(s) URL, got %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("NEWAPI_BASE_URL must not carry credentials, query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}
