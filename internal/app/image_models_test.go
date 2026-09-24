package app

import "testing"

func TestBuildImagePayloadFamilies(t *testing.T) {
	gpt54 := buildImagePayload("openai/gpt-5.4-image-2", "p", "1024x1024", "high", nil)
	if gpt54["size"] != nil || gpt54["output_format"] != nil || gpt54["background"] != "auto" || gpt54["aspect_ratio"] != "1:1" {
		t.Fatalf("gpt-5.4 payload=%v", gpt54)
	}
	sunburst := buildImagePayload("openai/gpt-image-2.5-sunburst", "p", "1024x1024", "high", nil)
	if sunburst["size"] != nil || sunburst["background"] != "transparent" || sunburst["quality"] != "high" {
		t.Fatalf("sunburst payload=%v", sunburst)
	}
	seedPro := buildImagePayload("bytedance-seed/seedream-5-0-pro", "p", "1024x1024", "high", []string{"data:image/png;base64,xx"})
	refs, _ := seedPro["input_references"].([]map[string]any)
	if seedPro["resolution"] != "1K" || seedPro["output_format"] != nil || seedPro["size"] != nil || len(refs) != 1 {
		t.Fatalf("seedream pro payload=%v", seedPro)
	}
	seedLite := buildImagePayload("bytedance-seed/seedream-5-0-lite", "p", "1024x1024", "high", nil)
	if seedLite["resolution"] != "2K" || seedLite["output_format"] != nil {
		t.Fatalf("seedream lite payload=%v", seedLite)
	}
	flux := buildImagePayload("black-forest-labs/flux.2-max", "p", "2048x2048", "high", nil)
	if flux["resolution"] != nil || flux["size"] != nil || flux["output_format"] != "png" {
		t.Fatalf("flux payload=%v", flux)
	}
	krea := buildImagePayload("krea/krea-2-large", "p", "", "high", nil)
	if krea["n"] != nil || krea["resolution"] != "1K" {
		t.Fatalf("krea payload=%v", krea)
	}
}

func TestBuildGeekAIPayload(t *testing.T) {
	p := buildGeekAIPayload("gpt-image-2.5-sunburst", "hello", "1024x1024", "high", []string{"data:image/png;base64,xx"})
	if p["size"] != "1024x1024" || p["async"] != true || p["image"] == nil || p["aspect_ratio"] != nil {
		t.Fatalf("geekai payload=%v", p)
	}
}

func TestNormalizeProviderKeepsGeekAI(t *testing.T) {
	s := Settings{APIBase: geekAIAPIBase, Model: "openai/gpt-image-2.5-sunburst", AssetHosts: nil}
	got := normalizeProviderSettings(s)
	if got.APIBase != geekAIAPIBase || got.Model != "gpt-image-2.5-sunburst" {
		t.Fatalf("got api=%s model=%s", got.APIBase, got.Model)
	}
	if len(got.AssetHosts) == 0 || got.AssetHosts[0] != "static.geekai.co" {
		t.Fatalf("asset hosts=%v", got.AssetHosts)
	}
}

func TestImageProvidersExposeBoth(t *testing.T) {
	list := imageProviders()
	if len(list) != 2 {
		t.Fatal(len(list))
	}
	if !contains(allowedAPIBases(), openRouterAPIBase) || !contains(allowedAPIBases(), geekAIAPIBase) {
		t.Fatal(allowedAPIBases())
	}
	if !modelAllowedForAPIBase(geekAIAPIBase, "gpt-image-2.5-sunburst") {
		t.Fatal("geekai model rejected")
	}
	if modelAllowedForAPIBase(geekAIAPIBase, "openai/gpt-image-2.5-sunburst") {
		t.Fatal("openrouter model should not be valid for geekai without mapping")
	}
}

func TestLatestImageModelsNonEmpty(t *testing.T) {
	list := latestImageModels()
	if len(list) < 16 {
		t.Fatal(len(list))
	}
	if !contains(allowedImageModelIDs(), "openai/gpt-image-2.5-sunburst") {
		t.Fatal("sunburst missing")
	}
	vendors := map[string]int{}
	for _, m := range list {
		vendors[m.Vendor]++
	}
	for v, n := range vendors {
		if n != 2 {
			t.Fatalf("vendor %s has %d models, want 2", v, n)
		}
	}
}
