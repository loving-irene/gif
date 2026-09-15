package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
)

func TestDebugLogsExplainProviderFailureWithoutSecrets(t *testing.T) {
	a := testApp(t)
	a.env.Debug = true
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	key := "fake-only-test-key"
	prompt := "本人的私人照片处理指令，不应写入日志"
	photo := sampleImage(false)
	body, _ := json.Marshal(map[string]any{"code": "validation_error", "message": "Invalid request; " + key + " " + prompt + " " + photo + " https://static.geekai.co/private/path?token=secret-value"})
	a.providerClient = func() *http.Client {
		return &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"upstream-request-123"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
		})}
	}
	cfg, _ := a.settings()
	_, err := a.callProvider(context.WithValue(context.Background(), debugTraceKey{}, "job-test"), cfg, prompt, []string{photo}, "1024x1024", nil)
	var failure *providerFailure
	if !errors.As(err, &failure) || failure.Status != 400 || failure.Code != "validation_error" {
		t.Fatal("lost structured error", err)
	}
	text := output.String()
	for _, secret := range []string{key, prompt, strings.SplitN(photo, ",", 2)[1], "secret-value", "/private/path"} {
		if strings.Contains(text, secret) || strings.Contains(err.Error(), secret) {
			t.Fatal("sensitive content leaked")
		}
	}
	for _, expected := range []string{"provider_request", "gpt-image-2.5-sunburst", "provider_response", "validation_error", "upstream-request-123", "job-test", "\"http_status\":400"} {
		if !strings.Contains(text, expected) {
			t.Fatal("missing debug evidence", expected)
		}
	}
}

func TestProviderTaskFailureAndHTMLResponse(t *testing.T) {
	a := testApp(t)
	for _, tc := range []struct {
		body   string
		status int
		want   string
	}{
		{`{"task_status":"failed","task_id":"task-123","error":{"code":"insufficient_quota","message":"Balance is insufficient"}}`, 200, "insufficient_quota"},
		{`<html>secret-html-payload</html>`, 502, "non-JSON"},
	} {
		response := &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}
		_, err := a.readProvider(context.Background(), response, "poll")
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret-html-payload") {
			t.Fatal("task error not explained safely", err)
		}
	}
}

func TestDebugOffAndNetworkCausePreserved(t *testing.T) {
	a := testApp(t)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	a.debug(context.Background(), "test", map[string]any{"state": "ready"})
	if output.Len() != 0 {
		t.Fatal("debug should be disabled by default")
	}
	a.env.Debug = true
	a.providerClient = func() *http.Client {
		return &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed: connection refused")
		})}
	}
	cfg, _ := a.settings()
	_, err := a.callProvider(context.Background(), cfg, "prompt", []string{sampleImage(false)}, "1024x1024", nil)
	if err == nil || !strings.Contains(err.Error(), "connection refused") || !strings.Contains(output.String(), "provider_http_end") {
		t.Fatal("network cause disappeared", err)
	}
}
