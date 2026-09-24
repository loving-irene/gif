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

func TestOpenRouterAdapterPayloadAndSyncResult(t *testing.T) {
	a := testApp(t)
	calls := 0
	fixture := sampleImage(false)
	a.providerClient = func() *http.Client {
		return &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Header.Get("Authorization") != "Bearer fake-only-test-key" {
				t.Error("missing provider credential")
			}
			if r.Method != "POST" || r.URL.String() != "https://openrouter.ai/api/v1/images" {
				t.Error("wrong generation route", r.Method, r.URL.String())
			}
			var p map[string]any
			if json.NewDecoder(r.Body).Decode(&p) != nil {
				t.Fatal("invalid JSON")
			}
			refs, ok := p["input_references"].([]any)
			if !ok || len(refs) != 2 {
				t.Error("reference image ordering changed", p["input_references"])
			}
			if p["model"] != "openai/gpt-image-2.5-sunburst" || p["n"] != float64(1) {
				t.Error("generation contract invalid", p)
			}
			if p["size"] != nil || p["resolution"] != nil {
				t.Error("openai payload must not send size/resolution", p)
			}
			if p["aspect_ratio"] != "1:1" || p["background"] != "transparent" {
				t.Error("openai aspect/background invalid", p)
			}
			b, _ := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": strings.SplitN(fixture, ",", 2)[1], "media_type": "image/png"}}})
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
		})}
	}
	cfg, _ := a.settings()
	result, err := a.callProvider(context.Background(), cfg, "保留本人特征", []string{fixture, fixture}, "2048x2048", nil)
	if err != nil || result != fixture || calls != 1 {
		t.Fatalf("adapter failed: calls=%d err=%v result_prefix=%q", calls, err, truncate(result, 40))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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
