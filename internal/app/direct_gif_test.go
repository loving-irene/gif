package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func useLegacySheetModel(t *testing.T, a *App) {
	t.Helper()
	// These tests specifically exercise PNG grid slicing, independently of the
	// default provider's native-animation capability.
	cfg, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model = "openai/gpt-image-2.5-sunburst"
	b, _ := json.Marshal(cfg)
	if _, err = a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(b)); err != nil {
		t.Fatal(err)
	}
}

func animationFixture(t *testing.T, frames int) []byte {
	t.Helper()
	a := &gif.GIF{LoopCount: 0}
	for i := 0; i < frames; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, 12, 12), color.Palette{color.Transparent, color.Black, color.White})
		frame.SetColorIndex(i, i, uint8(i%2+1))
		a.Image = append(a.Image, frame)
		a.Delay = append(a.Delay, 7+i)
		a.Disposal = append(a.Disposal, gif.DisposalBackground)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, a); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDirectGIFValidation(t *testing.T) {
	good := animationFixture(t, 3)
	got, err := animatedGIFData(dataURL("image/gif", good))
	if err != nil || !bytes.Equal(got, good) {
		t.Fatal("animation bytes changed", err)
	}
	oversize := append([]byte(nil), good...)
	oversize[6], oversize[7] = 0xff, 0xff
	for name, input := range map[string]string{
		"static":           dataURL("image/gif", animationFixture(t, 1)),
		"png":              sampleImage(false),
		"truncated":        dataURL("image/gif", good[:len(good)-1]),
		"large canvas":     dataURL("image/gif", oversize),
		"invalid encoding": "data:image/gif;base64,!!",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := animatedGIFData(input); err == nil {
				t.Fatal("invalid animation accepted")
			}
		})
	}
	if _, err := imageData(dataURL("image/gif", good), 5<<20); err == nil {
		t.Fatal("GIF allowed as a selfie")
	}
}

func TestDirectGIFHTTPWorkflow(t *testing.T) {
	for _, resultKind := range []string{"animated", "static", "png"} {
		t.Run(resultKind, func(t *testing.T) {
			a := testApp(t)
			cfg, _ := a.settings()
			if cfg.Model != defaultImageModel {
				t.Fatal("wrong default", cfg.Model)
			}
			s := loginDevice(t, a, "direct-gif-"+resultKind)
			fixture := animationFixture(t, 3)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.Path != "/api/v1/images" {
					t.Error("wrong route", r.Method, r.URL.Path)
				}
				var body struct {
					Model  string           `json:"model"`
					Format string           `json:"output_format"`
					Prompt string           `json:"prompt"`
					Refs   []map[string]any `json:"input_references"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != defaultImageModel {
					t.Error("wrong model", body.Model)
				}
				data := strings.SplitN(sampleImage(false), ",", 2)[1]
				if len(body.Refs) == 1 {
					if body.Format != "png" {
						t.Error("draft must stay PNG", body.Format)
					}
				} else {
					if body.Format != "gif" || !strings.Contains(body.Prompt, "多帧 GIF") || strings.Contains(body.Prompt, "严格5列") {
						t.Error("wrong animation request", body.Format)
					}
					data = base64.StdEncoding.EncodeToString(fixture)
					if resultKind == "static" {
						data = base64.StdEncoding.EncodeToString(animationFixture(t, 1))
					}
					if resultKind == "png" {
						data = strings.SplitN(sampleImage(false), ",", 2)[1]
					}
				}
				// media_type is optional in OpenRouter's contract; sniff the bytes.
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": data}}})
			}))
			defer server.Close()
			a.providerClient = func() *http.Client {
				return &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
					copy := r.Clone(r.Context())
					copy.URL.Scheme = "http"
					copy.URL.Host = strings.TrimPrefix(server.URL, "http://")
					return http.DefaultTransport.RoundTrip(copy)
				})}
			}
			input := draftInput()
			draft := waitJob(t, a, s, jobID(t, request(t, a, s, "POST", "/api/generate", input)))
			if draft.Status != "succeeded" {
				t.Fatal("draft failed", draft.Status)
			}
			var accepted map[string]string
			json.Unmarshal(request(t, a, s, "POST", "/api/accept", map[string]string{"receipt": draft.Receipt}).Body.Bytes(), &accepted)
			motion := motionInput(input.Selfie, draft.Image, accepted["receipt"], "bike")
			id := jobID(t, request(t, a, s, "POST", "/api/generate", motion))
			job := waitJob(t, a, s, id)
			if calls.Load() != 2 {
				t.Fatal("generation retried", calls.Load())
			}
			if resultKind != "animated" {
				if job.Status != "failed" || job.Gif != "" {
					t.Fatal("non-animation succeeded", job.Status)
				}
				return
			}
			want := dataURL("image/gif", fixture)
			if job.Status != "succeeded" || job.Gif != want {
				t.Fatal("native animation changed or missing", job.Status)
			}
			a.jobsMu.Lock()
			a.jobs = map[string]*Job{}
			a.jobsMu.Unlock()
			var restored Job
			json.Unmarshal(request(t, a, s, "GET", "/api/jobs/"+id, nil).Body.Bytes(), &restored)
			if restored.Gif != want {
				t.Fatal("animation lost on restore")
			}
			var stored []byte
			if err := a.db.QueryRow("SELECT gif FROM works WHERE id=?", id).Scan(&stored); err != nil || !bytes.Equal(stored, fixture) {
				t.Fatal("cloud animation changed", err)
			}
		})
	}
}

func TestDirectGIFMigrationAndPrompts(t *testing.T) {
	a := testApp(t)
	useLegacySheetModel(t, a)
	a.db.Exec("DELETE FROM settings WHERE key='image_default_26'")
	if err := a.migrateDefaultImageModel(); err != nil {
		t.Fatal(err)
	}
	cfg, _ := a.settings()
	if cfg.Model != defaultImageModel {
		t.Fatal("old default not upgraded")
	}
	useLegacySheetModel(t, a)
	if err := a.migrateDefaultImageModel(); err != nil {
		t.Fatal(err)
	}
	cfg, _ = a.settings()
	if cfg.Model != "openai/gpt-image-2.5-sunburst" {
		t.Fatal("explicit historical choice overwritten")
	}
	for _, model := range []string{defaultImageModel, "openai/gpt-image-2.6-flare"} {
		cfg.Model = model
		prompt := motionPromptForModel(cfg, "挥手")
		if !strings.Contains(prompt, "多帧 GIF") || strings.Contains(prompt, "严格5列") {
			t.Fatal(prompt)
		}
		if buildImagePayload(model, "p", "1024x1024", "max", nil)["quality"] != "max" {
			t.Fatal("2.6 quality downgraded")
		}
	}
	cfg.APIBase = geekAIAPIBase
	if supportsDirectGIF(cfg) {
		t.Fatal("GIF capability leaked to GeekAI")
	}
	// A native result must fail if persistence fails, rather than silently
	// reporting success and sending it through the browser's slicing fallback.
	a.files = filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(a.files, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := a.applyResult(context.Background(), "validid", "user", "motion", "", "", Selection{}, dataURL("image/gif", animationFixture(t, 2)), motionSpecOf("5x5"))
	if err == nil {
		t.Fatal("GIF persistence failure was swallowed")
	}
}

func TestCompareDirectGIF(t *testing.T) {
	a := testApp(t)
	fixture := dataURL("image/gif", animationFixture(t, 3))
	calls := 0
	a.providerCall = asProviderCall(func(ctx context.Context, cfg Settings, prompt string, refs []string) (string, error) {
		calls++
		if len(refs) == 1 {
			return sampleImage(false), nil
		}
		if cfg.OutputFormat != "gif" || strings.Contains(prompt, "严格5列") || !strings.Contains(prompt, "多帧 GIF") {
			t.Fatal("compare not using direct animation")
		}
		return fixture, nil
	})
	job := &compareJobItem{ID: "direct", Model: defaultImageModel}
	a.runComparePipeline("batch", job, sampleImage(false), compareDraftPrompt, compareMotionPresets()[0].Prompt, "high")
	if calls != 2 || job.AnimStatus != "succeeded" || job.AnimGif != fixture {
		t.Fatal("compare animation changed", job.AnimStatus)
	}
}
