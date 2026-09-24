package app

import (
	"strings"
	"testing"
)

func TestNormalizeCategoriesAddsDailyAndShoot(t *testing.T) {
	legacy := defaultCategories()[:3] // male/female/child only
	male := legacy[0]
	filtered := male.Actions[:0]
	for _, a := range male.Actions {
		if a.ID != "shoot" {
			filtered = append(filtered, a)
		}
	}
	male.Actions = filtered
	legacy[0] = male

	got := normalizeCategories(legacy)
	if len(got) != 4 || got[3].ID != "daily" {
		t.Fatalf("expected daily as 4th category, got %+v", got)
	}
	foundShoot := false
	for _, a := range got[0].Actions {
		if a.ID == "shoot" {
			foundShoot = true
		}
	}
	if !foundShoot {
		t.Fatal("normalizeCategories should ensure male has shoot")
	}
	if err := validateSettings(Settings{
		DefaultCredits: 5, UserConcurrency: 5, APIBase: "https://openrouter.ai/api/v1",
		Model: "openai/gpt-image-2.5-sunburst", Quality: "high", AssetHosts: []string{"openrouter.ai"},
		IdentityPrompt: defaults(Env{}).IdentityPrompt, DraftPrompt: defaults(Env{}).DraftPrompt,
		MotionPrompt: defaults(Env{}).MotionPrompt, MotionGrid: "5x5",
		Styles: defaultStyles(), Categories: got,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsUse5x5AndDaily(t *testing.T) {
	cfg := defaults(Env{})
	if cfg.MotionGrid != "5x5" {
		t.Fatal(cfg.MotionGrid)
	}
	if len(cfg.Categories) != 4 || cfg.Categories[3].ID != "daily" {
		t.Fatalf("categories=%+v", cfg.Categories)
	}
	if !strings.Contains(cfg.IdentityPrompt, "面部辨识度最高优先") {
		t.Fatal("identity prompt not updated")
	}
	got := buildMotionPrompt("挥手两次", "5x5")
	if !strings.Contains(got, "图1是已确认角色定稿") || !strings.Contains(got, "挥手两次") {
		t.Fatal("built-in motion prompt missing", got)
	}
}
