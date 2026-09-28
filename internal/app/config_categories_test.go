package app

import (
	"strings"
	"testing"
)

func TestNormalizeCategoriesAddsDailyAndMaleSports(t *testing.T) {
	legacy := defaultCategories()[:3] // male/female/child only
	male := legacy[0]
	male.Actions = append([]Action{
		{ID: "idle", Name: "护卫待机", Icon: "🛡", Prompt: "呼吸"},
		{ID: "attack", Name: "蓄力攻击", Icon: "⚔", Prompt: "出招"},
		{ID: "shoot", Name: "开枪射击", Icon: "🔫", Prompt: "射击"},
	}, male.Actions...)
	legacy[0] = male

	got := normalizeCategories(legacy)
	if len(got) != 4 || got[3].ID != "daily" {
		t.Fatalf("expected daily as 4th category, got %+v", got)
	}
	have := map[string]bool{}
	for _, a := range got[0].Actions {
		have[a.ID] = true
	}
	for _, id := range maleLegacyActionIDs {
		if have[id] {
			t.Fatal("legacy male warrior action should be removed", id)
		}
	}
	for _, id := range []string{"bike", "basketball", "billiards", "smoke", "fish"} {
		if !have[id] {
			t.Fatal("normalizeCategories should ensure male sport action", id)
		}
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
	daily := cfg.Categories[3]
	if len(daily.Actions) != 12 {
		t.Fatalf("daily should ship 12 handbook actions, got %d", len(daily.Actions))
	}
	wantIDs := []string{"eat", "hello_wave", "cry", "dance", "gift", "stomp", "shock", "heart"}
	have := map[string]Action{}
	for _, a := range daily.Actions {
		have[a.ID] = a
	}
	for _, id := range wantIDs {
		a, ok := have[id]
		if !ok || strings.TrimSpace(a.Prompt) == "" {
			t.Fatal("daily missing handbook action", id)
		}
		if !strings.Contains(a.Prompt, "【动作：") {
			t.Fatal("handbook action should keep structured action body", id, a.Prompt)
		}
	}
	if strings.Contains(have["eat"].Prompt, "允许饭山顶部被格边界裁切") {
		t.Fatal("eat prompt must keep rice mound inside the cell")
	}
	if !strings.Contains(cfg.IdentityPrompt, "面部辨识度最高优先") {
		t.Fatal("identity prompt not updated")
	}
	got := buildMotionPrompt(have["eat"].Prompt, "5x5")
	if !strings.Contains(got, "图1是已确认角色定稿") || !strings.Contains(got, "【动作：干饭】") {
		t.Fatal("built-in motion prompt missing", got)
	}
	for _, want := range []string{"水平垂直居中", "HTML padding", "8%–12%", "禁止越往后越放大", "约80%", "独立生成"} {
		if !strings.Contains(got, want) {
			t.Fatal("motion cell padding layout missing", want, got)
		}
	}
}

func TestNormalizeCategoriesAddsHandbookDailyActions(t *testing.T) {
	legacy := defaultCategories()
	daily := legacy[3]
	daily.Actions = []Action{
		{ID: "morning", Name: "早呀", Icon: "☀", Prompt: "挥手"},
		{ID: "eat", Name: "干饭", Icon: "🍜", Prompt: dailyActionEatLegacy},
		{ID: "miss", Name: "想你了", Icon: "♡", Prompt: dailyActionMissLegacy},
		{ID: "play", Name: "出去玩", Icon: "🏃", Prompt: "招手"},
	}
	legacy[3] = daily
	got := normalizeCategories(legacy)
	have := map[string]Action{}
	for _, a := range got[3].Actions {
		have[a.ID] = a
	}
	if _, ok := have["miss"]; ok {
		t.Fatal("legacy miss should migrate away when heart exists")
	}
	if have["heart"].Name != "双手比心" || !strings.Contains(have["heart"].Prompt, "【动作：双手比心】") {
		t.Fatal("miss should migrate to heart", have["heart"])
	}
	if !strings.Contains(have["eat"].Prompt, "必须完整处于本格内") {
		t.Fatal("eat legacy prompt should upgrade", have["eat"].Prompt)
	}
	for _, id := range []string{"hello_wave", "cry", "dance", "gift", "stomp", "shock"} {
		if _, ok := have[id]; !ok {
			t.Fatal("missing ensured handbook action", id)
		}
	}
	if len(got[3].Actions) > 12 {
		t.Fatal("daily actions exceeded 12", len(got[3].Actions))
	}
}

func TestDefaultsAndNormalizeFemaleHandbookActions(t *testing.T) {
	cfg := defaults(Env{})
	female := cfg.Categories[1]
	if female.ID != "female" || len(female.Actions) != 6 {
		t.Fatalf("female should ship 6 handbook actions, got id=%s n=%d", female.ID, len(female.Actions))
	}
	wantIDs := []string{"selfie_peace", "skirt_spin", "blush", "wish", "milk_tea", "petal"}
	have := map[string]Action{}
	for _, a := range female.Actions {
		have[a.ID] = a
	}
	for _, id := range femaleLegacyActionIDs {
		if _, ok := have[id]; ok {
			t.Fatal("female defaults still include legacy action", id)
		}
	}
	for _, id := range wantIDs {
		a, ok := have[id]
		if !ok || strings.TrimSpace(a.Prompt) == "" {
			t.Fatal("female missing handbook action", id)
		}
		if !strings.Contains(a.Prompt, "【动作：") {
			t.Fatal("female handbook action should keep structured body", id, a.Prompt)
		}
	}
	got := buildMotionPrompt(have["selfie_peace"].Prompt, "5x5")
	if !strings.Contains(got, "图1是已确认角色定稿") || !strings.Contains(got, "【动作：比耶自拍】") {
		t.Fatal("female motion prompt missing built-in wrapper", got)
	}

	legacy := defaultCategories()
	legacy[1].Actions = []Action{
		{ID: "wave", Name: "开心打招呼", Icon: "👋", Prompt: "挥手"},
		{ID: "heart", Name: "给你比心", Icon: "♡", Prompt: "比心"},
		{ID: "clap", Name: "开心鼓掌", Icon: "👏", Prompt: "鼓掌"},
		{ID: "cheer", Name: "加油打气", Icon: "✊", Prompt: "加油"},
		{ID: "shy", Name: "害羞开心", Icon: "🌸", Prompt: "害羞"},
		{ID: "sleep", Name: "晚安困困", Icon: "☾", Prompt: "打哈欠"},
	}
	normalized := normalizeCategories(legacy)
	have = map[string]Action{}
	for _, a := range normalized[1].Actions {
		have[a.ID] = a
	}
	for _, id := range femaleLegacyActionIDs {
		if _, ok := have[id]; ok {
			t.Fatal("normalizeCategories should remove female legacy action", id)
		}
	}
	for _, id := range wantIDs {
		if _, ok := have[id]; !ok {
			t.Fatal("normalizeCategories should ensure female handbook action", id)
		}
	}
	if len(normalized[1].Actions) != 6 {
		t.Fatalf("female should keep 6 handbook actions, got %d", len(normalized[1].Actions))
	}
}

func TestNormalizeCategoriesAddsMaleSportActions(t *testing.T) {
	legacy := defaultCategories()
	male := legacy[0]
	male.Actions = []Action{
		{ID: "idle", Name: "护卫待机", Icon: "🛡", Prompt: "呼吸"},
		{ID: "attack", Name: "蓄力攻击", Icon: "⚔", Prompt: "出招"},
	}
	legacy[0] = male
	got := normalizeCategories(legacy)
	have := map[string]Action{}
	for _, a := range got[0].Actions {
		have[a.ID] = a
	}
	for _, id := range maleLegacyActionIDs {
		if _, ok := have[id]; ok {
			t.Fatal("legacy male warrior action should be removed", id)
		}
	}
	for _, id := range []string{"bike", "basketball", "billiards", "smoke", "fish"} {
		a, ok := have[id]
		if !ok || strings.TrimSpace(a.Prompt) == "" {
			t.Fatal("male missing handbook action", id, a)
		}
		if !strings.Contains(a.Prompt, "【动作：") {
			t.Fatal("male sport action should keep structured body", id)
		}
	}
	if len(got[0].Actions) != 5 {
		t.Fatalf("male should keep 5 sport actions, got %d", len(got[0].Actions))
	}
	cfg := defaults(Env{})
	if len(cfg.Categories[0].Actions) != 5 {
		t.Fatalf("male defaults should ship 5 actions, got %d", len(cfg.Categories[0].Actions))
	}
}
