// Package httplog records every HTTP transaction - inbound requests +
// their responses, and the process's own outbound calls - as one JSON
// line each, to a size/age-rotated file (or stdout/stderr).
//
// It's off unless HTTP_LOG_ENABLED=true. Wiring (std-http):
//   - http transitions' StartServer calls Init(), wraps the server
//     handler with Middleware (inbound) and calls WrapDefaultTransport
//     (outbound) - so every std-http consumer (the api server included)
//     gets it for free once the env switch is on.
//   - a binary that starts its own mux can wrap it with Middleware
//     directly, and call Transport() on any http.Client it builds.
//
// Everything is env-configurable - what to write, where, and how it
// rotates:
//
//	HTTP_LOG_ENABLED            master switch (default false)
//	HTTP_LOG_FILE               path, or "" / "stdout" / "stderr"
//	                            (default "runtime/log/http.log")
//	HTTP_LOG_MAX_SIZE_MB        rotate when the file passes this (default 50)
//	HTTP_LOG_MAX_BACKUPS        rotated files to keep (default 10)
//	HTTP_LOG_MAX_AGE_DAYS       delete rotated files older than this (default 14)
//	HTTP_LOG_COMPRESS           gzip rotated files (default true)
//	HTTP_LOG_INBOUND            log received requests/responses (default true)
//	HTTP_LOG_OUTBOUND           log the backend's own HTTP calls (default true)
//	HTTP_LOG_BODIES             include request/response bodies (default true)
//	HTTP_LOG_BODY_MAX           per-body cap in bytes (default 8192)
//	HTTP_LOG_HEADERS            include header maps (default true)
//	HTTP_LOG_SAMPLE             fraction of transactions to log, 0..1 (default 1)
//	HTTP_LOG_SKIP_PATHS         comma-sep path prefixes to never log
//	                            (default "/health,/favicon.ico,/robots.txt")
//	HTTP_LOG_REDACT_HEADERS     comma-sep header names to mask
//	HTTP_LOG_REDACT_FIELDS      comma-sep JSON/form keys to mask
//	HTTP_LOG_QUEUE              in-memory buffer of pending lines (default 4096)
package httplog

import (
	"os"
	"strconv"
	"strings"
)

// Config is the resolved env configuration. Read-only after Init().
type Config struct {
	Enabled    bool
	File       string
	MaxSize    int64 // bytes
	MaxBackups int
	MaxAgeDays int
	Compress   bool

	Inbound  bool
	Outbound bool

	Bodies  bool
	BodyMax int
	Headers bool

	Sample    float64
	SkipPaths []string
	QueueSize int

	redactHeaders map[string]bool
	redactFields  map[string]bool
}

var cfg Config

const (
	defaultFile          = "runtime/log/http.log"
	defaultRedactHeaders = "authorization,cookie,set-cookie,x-csrf-token,proxy-authorization,x-api-key"
	defaultRedactFields  = "password,newpassword,oldpassword,confirm,code,token,accesstoken,access_token,refreshtoken,refresh_token,secret,client_secret,apikey,api_key,codehash,jwt,sessionid,session_id"
	defaultSkipPaths     = "/health,/favicon.ico,/robots.txt"
)

func envStr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envInt(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func csvSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out[p] = true
		}
	}
	return out
}

func loadConfig() Config {
	c := Config{
		Enabled:    envBool("HTTP_LOG_ENABLED", false),
		File:       envStr("HTTP_LOG_FILE", defaultFile),
		MaxSize:    int64(envInt("HTTP_LOG_MAX_SIZE_MB", 50)) * 1024 * 1024,
		MaxBackups: envInt("HTTP_LOG_MAX_BACKUPS", 10),
		MaxAgeDays: envInt("HTTP_LOG_MAX_AGE_DAYS", 14),
		Compress:   envBool("HTTP_LOG_COMPRESS", true),
		Inbound:    envBool("HTTP_LOG_INBOUND", true),
		Outbound:   envBool("HTTP_LOG_OUTBOUND", true),
		Bodies:     envBool("HTTP_LOG_BODIES", true),
		BodyMax:    envInt("HTTP_LOG_BODY_MAX", 8192),
		Headers:    envBool("HTTP_LOG_HEADERS", true),
		Sample:     envFloat("HTTP_LOG_SAMPLE", 1.0),
		QueueSize:  envInt("HTTP_LOG_QUEUE", 4096),
	}
	for _, p := range strings.Split(envStr("HTTP_LOG_SKIP_PATHS", defaultSkipPaths), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.SkipPaths = append(c.SkipPaths, p)
		}
	}
	c.redactHeaders = csvSet(envStr("HTTP_LOG_REDACT_HEADERS", defaultRedactHeaders))
	c.redactFields = csvSet(envStr("HTTP_LOG_REDACT_FIELDS", defaultRedactFields))
	if c.BodyMax < 0 {
		c.BodyMax = 0
	}
	if c.Sample < 0 {
		c.Sample = 0
	}
	if c.Sample > 1 {
		c.Sample = 1
	}
	if c.QueueSize < 64 {
		c.QueueSize = 64
	}
	return c
}

// Enabled reports whether logging is active (post-Init).
func Enabled() bool { return cfg.Enabled }

// InboundEnabled / OutboundEnabled gate the two wiring points.
func InboundEnabled() bool  { return cfg.Enabled && cfg.Inbound }
func OutboundEnabled() bool { return cfg.Enabled && cfg.Outbound }
