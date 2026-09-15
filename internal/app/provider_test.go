package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGeekAIAdapterPayloadAndAsyncPolling(t *testing.T) {
	a := testApp(t)
	calls := 0
	fixture := sampleImage(false)
	a.providerClient = func() *http.Client {
		return &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Header.Get("Authorization") != "Bearer fake-only-test-key" {
				t.Error("missing provider credential")
			}
			body := ""
			if calls == 1 {
				if r.Method != "POST" || r.URL.String() != "https://geekai.co/api/v1/images/generations" {
					t.Error("wrong generation route")
				}
				var p map[string]any
				if json.NewDecoder(r.Body).Decode(&p) != nil {
					t.Fatal("invalid JSON")
				}
				images, ok := p["images"].([]any)
				if !ok || len(images) != 2 || images[0] != fixture || images[1] != fixture {
					t.Error("reference image ordering changed")
				}
				if p["model"] != "gpt-image-2.5-sunburst" || p["size"] != "2048x2048" || p["n"] != float64(1) || p["async"] != true || p["retries"] != float64(0) {
					t.Error("generation contract invalid")
				}
				body = `{"task_id":"test-task","task_status":"pending"}`
			} else {
				if r.Method != "GET" || r.URL.Path != "/api/v1/images/test-task" {
					t.Error("unexpected status route")
				}
				b, _ := json.Marshal(map[string]any{"task_status": "succeed", "data": []map[string]string{{"b64_json": strings.SplitN(fixture, ",", 2)[1]}}})
				body = string(b)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
	}
	cfg, _ := a.settings()
	result, err := a.callProvider(context.Background(), cfg, "保留本人特征", []string{fixture, fixture}, "2048x2048", nil)
	if err != nil || result != fixture || calls != 2 {
		t.Fatalf("adapter failed: calls=%d err=%v", calls, err)
	}
}

func TestProviderImageSizeByJobKindAndMotionGrid(t *testing.T) {
	for _, tc := range []struct {
		kind, grid, want string
	}{
		{"draft", "10x10", "1024x1024"},
		{"motion", "4x4", "1024x1024"},
		{"motion", "5x5", "1024x1024"},
		{"motion", "10x10", "2048x2048"},
	} {
		if got := providerImageSize(tc.kind, tc.grid); got != tc.want {
			t.Fatalf("providerImageSize(%q, %q)=%q, want %q", tc.kind, tc.grid, got, tc.want)
		}
	}
}
