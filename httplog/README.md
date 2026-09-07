# httplog

Records every HTTP transaction — inbound requests + their responses, and the
process's own outbound calls — as **one JSON line each**, to a size/age-rotated
file (or stdout/stderr). Zero external dependencies (stdlib only).

It is **off** unless `HTTP_LOG_ENABLED=true`.

## Wiring

`std-http`'s `StartServer` transition already calls `httplog.Init()`, wraps the
server handler with `httplog.Middleware` (inbound) and calls
`httplog.WrapDefaultTransport()` (outbound). So **any std-http consumer — the
`api` server included — gets it for free** once the env switch is on.

A binary that starts its own `http.ServeMux` can wrap it directly:

```go
httplog.Init()
srv := &http.Server{Handler: httplog.Middleware(mux)}
client := &http.Client{Transport: httplog.Transport(nil)}
defer httplog.Close()
```

## Configuration (env)

| Var | Default | |
|---|---|---|
| `HTTP_LOG_ENABLED` | `false` | master switch |
| `HTTP_LOG_FILE` | `runtime/log/http.log` | path, or `stdout` / `stderr` |
| `HTTP_LOG_MAX_SIZE_MB` | `50` | rotate when the file passes this |
| `HTTP_LOG_MAX_BACKUPS` | `10` | rotated files to keep |
| `HTTP_LOG_MAX_AGE_DAYS` | `14` | delete rotated files older than this |
| `HTTP_LOG_COMPRESS` | `true` | gzip rotated files |
| `HTTP_LOG_INBOUND` | `true` | log received requests/responses |
| `HTTP_LOG_OUTBOUND` | `true` | log the process's own HTTP calls |
| `HTTP_LOG_BODIES` | `true` | include request/response bodies |
| `HTTP_LOG_BODY_MAX` | `8192` | per-body cap in bytes |
| `HTTP_LOG_HEADERS` | `true` | include header maps |
| `HTTP_LOG_SAMPLE` | `1` | fraction of transactions to log, `0..1` |
| `HTTP_LOG_SKIP_PATHS` | `/health,/favicon.ico,/robots.txt` | path prefixes to never log |
| `HTTP_LOG_REDACT_HEADERS` | auth/cookie/api-key/… | header names to mask |
| `HTTP_LOG_REDACT_FIELDS` | password/token/secret/… | JSON/form keys to mask |
| `HTTP_LOG_QUEUE` | `4096` | in-memory buffer of pending lines |

## Behaviour notes

- **Never blocks a request.** Lines are queued to a buffered channel and written
  by one goroutine; a full queue drops the line and a synthetic
  `"dir":"meta"` line reports the drop count each minute.
- **Redaction** runs before anything is written: listed headers → `***`; listed
  JSON/form keys → `"***"`. Bodies are capped to `HTTP_LOG_BODY_MAX` with a
  `…Truncated` flag.
- **Streaming stays streaming** — request/response bodies are tapped for the
  first N bytes, not buffered whole.
- **WebSocket / hijacked** connections log the handshake only.
- **Rotation** is size + count + age, dependency-free: the active file is renamed
  to `<path>.<timestamp>` (optionally gzipped) and a fresh one opened.

## One line looks like

```json
{"time":"2026-09-07T10:15:04.123456Z","dir":"in","method":"POST","url":"/auth/login","path":"/auth/login","remoteIp":"10.0.0.4","status":200,"durationMs":12.4,"reqBytes":48,"respBytes":210,"reqHeaders":{"Authorization":"***","Content-Type":"application/json"},"reqBody":"{\"email\":\"a@b.co\",\"password\":\"***\"}","respBody":"{\"token\":\"***\",\"ok\":true}"}
```
