package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

func publicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2001::/32", "2002::/16", "fec0::/10"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}
func safeClient() *http.Client {
	return &http.Client{Timeout: 100 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 90 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("host has no addresses")
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, errors.New("non-public destination denied")
			}
		}
		d := net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}}
}

type providerResponse struct {
	TaskID  string          `json:"task_id"`
	Status  string          `json:"task_status"`
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Error   json.RawMessage `json:"error"`
	Data    []struct {
		URL    string `json:"url"`
		Base64 string `json:"b64_json"`
	} `json:"data"`
}

func (a *App) readProvider(ctx context.Context, res *http.Response, phase string) (providerResponse, error) {
	defer res.Body.Close()
	var p providerResponse
	limit := int64(29 * 1024 * 1024)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		limit = 64 * 1024
	}
	body, readErr := io.ReadAll(io.LimitReader(res.Body, limit+1))
	parseErr := json.Unmarshal(body, &p)
	code, message := providerErrorInfo(p)
	a.debug(ctx, "provider_response", map[string]any{"phase": phase, "http_status": res.StatusCode, "content_type": res.Header.Get("Content-Type"), "request_id": res.Header.Get("X-Request-Id"), "response_bytes": len(body), "provider_task_id": p.TaskID, "task_status": p.Status, "image_count": len(p.Data), "error_code": code, "error_message": message, "json_error": errorText(parseErr), "read_error": errorText(readErr), "body_truncated": int64(len(body)) > limit})
	if res.StatusCode < 200 || res.StatusCode >= 300 || p.Status == "failed" || p.Status == "canceled" {
		if message == "" {
			message = "provider request failed"
		}
		if parseErr != nil {
			message = "provider returned non-JSON or invalid JSON error response"
		}
		sensitive, _ := ctx.Value(debugSensitiveKey{}).([]string)
		return p, &providerFailure{Phase: phase, Status: res.StatusCode, Code: safeDebugText(code, sensitive), Message: safeDebugText(message, sensitive)}
	}
	if readErr != nil {
		return p, fmt.Errorf("%s: read provider response: %w", phase, readErr)
	}
	if int64(len(body)) > limit {
		return p, fmt.Errorf("%s: provider response too large", phase)
	}
	if parseErr != nil {
		return p, fmt.Errorf("%s: invalid provider JSON: %w", phase, parseErr)
	}
	return p, nil
}

func providerErrorInfo(p providerResponse) (string, string) {
	code, message := providerScalar(p.Code), p.Message
	if len(p.Error) > 0 && string(p.Error) != "null" {
		var nested struct {
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
			Type    string          `json:"type"`
		}
		if json.Unmarshal(p.Error, &nested) == nil {
			if c := providerScalar(nested.Code); c != "" {
				code = c
			} else if code == "" {
				code = nested.Type
			}
			if nested.Message != "" {
				message = nested.Message
			}
		} else {
			var text string
			if json.Unmarshal(p.Error, &text) == nil {
				message = text
			}
		}
	}
	return code, message
}

func providerScalar(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprint(v)
	}
	return ""
}

func (a *App) providerHTTP(ctx context.Context, client *http.Client, req *http.Request, phase string) (*http.Response, error) {
	started := time.Now()
	a.debug(ctx, "provider_http_start", map[string]any{"phase": phase, "method": req.Method, "host": req.URL.Hostname()})
	req = req.WithContext(a.networkTrace(ctx, phase))
	res, err := client.Do(req)
	status := 0
	if res != nil {
		status = res.StatusCode
	}
	a.debug(ctx, "provider_http_end", map[string]any{"phase": phase, "http_status": status, "elapsed_ms": time.Since(started).Milliseconds(), "error": errorText(err)})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", phase, err)
	}
	return res, nil
}

// upstreamTaskKey 用于在调试与续查之间传递上游任务号：提交成功后就写入上下文，
// 同一上下文继续运行时跳过“提交”阶段，直接按该任务号轮询结果。
type upstreamTaskKey struct{}

// providerWaitError 区分“等待超时”和“服务停止/请求作废”：只有前者值得留到下一轮继续认领上游结果。
func providerWaitError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("provider request aborted")
}

// continueProvider 只轮询已经提交过的上游任务号，不再提交新的生成请求：
// 用于任务超时后继续认领原上游任务的结果，不会产生第二次计费。
func (a *App) continueProvider(ctx context.Context, cfg Settings, taskID string) (string, error) {
	if !idPattern.MatchString(taskID) {
		return "", errors.New("invalid provider task ID")
	}
	ctx = context.WithValue(ctx, debugTraceKey{}, taskID)
	ctx = context.WithValue(ctx, upstreamTaskKey{}, taskID)
	key := a.secret("api_key")
	ctx = context.WithValue(ctx, debugSensitiveKey{}, []string{key, a.env.Secret})
	client := a.providerClient()
	defer client.CloseIdleConnections()
	a.debug(ctx, "provider_resume", map[string]any{"provider_task_id": taskID, "endpoint": "/api/v1/images/{task_id}"})
	p := providerResponse{TaskID: taskID, Status: "running"}
	return a.pollProvider(ctx, cfg, client, p, nil)
}

func (a *App) callProvider(ctx context.Context, cfg Settings, prompt string, images []string, onTaskID func(string)) (string, error) {
	if ctx.Value(debugTraceKey{}) == nil {
		ctx = context.WithValue(ctx, debugTraceKey{}, token(8))
	}
	key := a.secret("api_key")
	sensitive := []string{key, prompt, a.env.Secret}
	for _, data := range images {
		sensitive = append(sensitive, data)
		if _, body, ok := strings.Cut(data, ","); ok {
			sensitive = append(sensitive, body)
		}
	}
	ctx = context.WithValue(ctx, debugSensitiveKey{}, sensitive)
	client := a.providerClient()
	defer client.CloseIdleConnections()
	// 已经提交过的任务（超时后续查）直接沿用原上游任务号，不再提交新请求。
	if taskID, _ := ctx.Value(upstreamTaskKey{}).(string); taskID != "" {
		return a.pollProvider(ctx, cfg, client, providerResponse{TaskID: taskID, Status: "running"}, onTaskID)
	}
	payload := map[string]any{"model": cfg.Model, "prompt": prompt, "size": "1024x1024", "quality": cfg.Quality, "n": 1, "output_format": "png", "response_format": "b64_json", "background": "transparent", "async": true, "retries": 0}
	if len(images) == 1 {
		payload["image"] = images[0]
	} else {
		payload["images"] = images
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	a.debug(ctx, "provider_request", map[string]any{"model": cfg.Model, "endpoint": "/api/v1/images/generations", "quality": cfg.Quality, "size": "1024x1024", "output_format": "png", "response_format": "b64_json", "background": "transparent", "async": true, "retries": 0, "key_configured": key != "", "image_count": len(images), "images": imageSummaries(images), "prompt_chars": utf8.RuneCountInString(prompt), "request_bytes": len(b)})
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.APIBase+"/images/generations", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	// Never retry a generation POST: one dispatch is one user credit.
	res, err := a.providerHTTP(ctx, client, req, "submit")
	if err != nil {
		return "", err
	}
	p, err := a.readProvider(ctx, res, "submit")
	if err != nil {
		return "", err
	}
	return a.pollProvider(ctx, cfg, client, p, onTaskID)
}

// pollProvider 轮询上游任务直到拿到图片：提交响应与续查调用共用这段逻辑。
// 拿到上游任务号后除写入上下文外，还通过 onTaskID 通知调用方落盘，超时后仍能继续认领。
func (a *App) pollProvider(ctx context.Context, cfg Settings, client *http.Client, p providerResponse, onTaskID func(string)) (string, error) {
	key := a.secret("api_key")
	for len(p.Data) == 0 && p.TaskID != "" && p.Status != "failed" && p.Status != "canceled" {
		if p.Status == "succeed" {
			return "", errors.New("poll: task succeeded without an image")
		}
		if !idPattern.MatchString(p.TaskID) {
			return "", errors.New("invalid provider task ID")
		}
		ctx = context.WithValue(ctx, upstreamTaskKey{}, p.TaskID)
		if onTaskID != nil {
			onTaskID(p.TaskID)
		}
		select {
		case <-ctx.Done():
			return "", providerWaitError(ctx)
		case <-time.After(3 * time.Second):
		}
		task := p.TaskID
		req, err := http.NewRequestWithContext(ctx, "GET", cfg.APIBase+"/images/"+url.PathEscape(task), nil)
		if err != nil {
			return "", providerWaitError(ctx)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := a.providerHTTP(ctx, client, req, "poll")
		if err != nil {
			return "", err
		}
		p, err = a.readProvider(ctx, res, "poll")
		if err != nil {
			return "", err
		}
		if p.TaskID == "" {
			p.TaskID = task
		}
	}
	if p.Status == "failed" || p.Status == "canceled" || len(p.Data) != 1 {
		code, message := providerErrorInfo(p)
		sensitive, _ := ctx.Value(debugSensitiveKey{}).([]string)
		return "", &providerFailure{Phase: "result", Code: safeDebugText(code, sensitive), Message: safeDebugText("expected exactly one image; "+message, sensitive)}
	}
	item := p.Data[0]
	if item.Base64 != "" {
		a.debug(ctx, "provider_image", map[string]any{"format": "b64_json", "encoded_chars": len(item.Base64)})
		return "data:image/png;base64," + item.Base64, nil
	}
	u, err := url.Parse(item.URL)
	assetHost := ""
	if u != nil {
		assetHost = u.Hostname()
	}
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" && !strings.EqualFold(u.Port(), "443") || !contains(cfg.AssetHosts, u.Hostname()) {
		a.debug(ctx, "download_rejected", map[string]any{"host": assetHost, "reason": "HTTPS host not in configured allowlist or invalid URL"})
		return "", errors.New("image download host not allowed")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	res, err := a.providerHTTP(ctx, client, req, "download")
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("download: unexpected HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 20*1024*1024+1))
	if err != nil || len(b) > 20*1024*1024 {
		return "", errors.New("image response too large")
	}
	a.debug(ctx, "provider_image", map[string]any{"format": "url", "host": assetHost, "bytes": len(b), "content_type": http.DetectContentType(b)})
	return "data:" + http.DetectContentType(b) + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}
