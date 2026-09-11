package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strings"
)

type debugTraceKey struct{}
type debugSensitiveKey struct{}

var debugDataURL = regexp.MustCompile(`(?i)data:image/[^\s"'<>]+`)
var debugURL = regexp.MustCompile(`https?://[^\s"'<>]+`)
var debugBearer = regexp.MustCompile(`(?i)Bearer\s+[^\s"'<>;,]+`)
var debugCredential = regexp.MustCompile(`(?i)(api[_-]?key|authorization|access[_-]?token|password|secret|signature)\s*[=:]\s*[^\s,;]+`)
var debugBase64 = regexp.MustCompile(`[A-Za-z0-9+/_-]{80,}={0,2}`)

func safeDebugText(text string, sensitive []string) string {
	for _, value := range sensitive {
		if value != "" {
			text = strings.ReplaceAll(text, value, "[REDACTED]")
		}
	}
	text = debugDataURL.ReplaceAllString(text, "[IMAGE_REDACTED]")
	text = debugURL.ReplaceAllStringFunc(text, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[URL_REDACTED]"
		}
		return "[URL_HOST:" + u.Hostname() + "]"
	})
	text = debugBearer.ReplaceAllString(text, "Bearer [REDACTED]")
	text = debugCredential.ReplaceAllString(text, "$1=[REDACTED]")
	text = debugBase64.ReplaceAllString(text, "[LONG_VALUE_REDACTED]")
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 800 {
		return string(runes[:800]) + "…"
	}
	return text
}

func (a *App) debug(ctx context.Context, event string, fields map[string]any) {
	if !a.env.Debug {
		return
	}
	record := make(map[string]any, len(fields)+2)
	sensitive, _ := ctx.Value(debugSensitiveKey{}).([]string)
	for key, value := range fields {
		if text, ok := value.(string); ok {
			value = safeDebugText(text, sensitive)
		}
		record[key] = value
	}
	record["event"] = event
	if id, ok := ctx.Value(debugTraceKey{}).(string); ok {
		record["job_id"] = id
	}
	b, err := json.Marshal(record)
	if err == nil {
		log.Printf("[gif-debug] %s", b)
	}
}

func (a *App) networkTrace(ctx context.Context, phase string) context.Context {
	if !a.env.Debug {
		return ctx
	}
	trace := &httptrace.ClientTrace{
		DNSDone: func(info httptrace.DNSDoneInfo) {
			addresses := make([]string, 0, len(info.Addrs))
			for _, ip := range info.Addrs {
				addresses = append(addresses, ip.IP.String())
			}
			a.debug(ctx, "network_dns", map[string]any{"phase": phase, "addresses": addresses, "error": errorText(info.Err)})
		},
		ConnectDone: func(network, addr string, err error) {
			a.debug(ctx, "network_connect", map[string]any{"phase": phase, "network": network, "address": addr, "error": errorText(err)})
		},
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			a.debug(ctx, "network_tls", map[string]any{"phase": phase, "tls_version": state.Version, "error": errorText(err)})
		},
	}
	return httptrace.WithClientTrace(ctx, trace)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func imageSummaries(images []string) []map[string]any {
	result := make([]map[string]any, 0, len(images))
	for _, value := range images {
		header, body, _ := strings.Cut(value, ",")
		mime := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
		if !contains([]string{"image/png", "image/jpeg", "image/webp"}, mime) {
			mime = "unknown"
		}
		size := len(body) * 3 / 4
		if strings.HasSuffix(body, "==") {
			size -= 2
		} else if strings.HasSuffix(body, "=") {
			size--
		}
		result = append(result, map[string]any{"mime": mime, "bytes": size})
	}
	return result
}

type providerFailure struct {
	Phase         string
	Status        int
	Code, Message string
}

func (e *providerFailure) Error() string {
	return fmt.Sprintf("%s: HTTP %d code=%s message=%s", e.Phase, e.Status, e.Code, e.Message)
}

type debugStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *debugStatusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
