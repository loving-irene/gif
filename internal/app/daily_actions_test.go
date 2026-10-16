package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestDailyTimelineMigratesFullLegacyCatalog(t *testing.T) {
	cfg := defaults(Env{})
	legacyIDs := []string{"morning", "eat_ask", "eat", "hello_wave", "cry", "dance", "gift", "heart", "play", "tease"}
	oldPrompts := map[string]string{
		"morning": dailyActionMorningLegacy, "eat_ask": dailyActionEatAskLegacy,
		"play": dailyActionPlayLegacy, "tease": dailyActionTeaseLegacy,
	}
	defs := map[string]Action{}
	for _, a := range cfg.Categories[3].Actions {
		defs[a.ID] = a
	}
	cfg.Categories[3].Actions = nil
	cfg.Categories[3].Subtitle = "情侣与年轻人的聊天贴纸"
	for _, id := range legacyIDs {
		a := defs[id]
		if p, ok := oldPrompts[id]; ok {
			a.Prompt = p
		}
		cfg.Categories[3].Actions = append(cfg.Categories[3].Actions, a)
	}
	cfg.Categories = normalizeCategories(cfg.Categories)
	if !reflect.DeepEqual(cfg.Categories[3], defaults(Env{}).Categories[3]) {
		t.Fatal("full legacy daily catalog did not upgrade to the ordered timeline")
	}
	if err := validateSettings(cfg); err != nil {
		t.Fatal("expanded settings cannot be saved", err)
	}
	before, _ := json.Marshal(cfg.Categories)
	after, _ := json.Marshal(normalizeCategories(cfg.Categories))
	if string(before) != string(after) {
		t.Fatal("normalization must be idempotent")
	}
}

func TestDailyTimelinePreservesCustomActions(t *testing.T) {
	cfg := defaults(Env{})
	customMorning := Action{"morning", "我们的早安", "⭐", "这是管理员自定义的早安过程，不应被默认文案替换。"}
	customMiss := Action{"miss", "想你啦", "♡", "这是自定义的想念动作，与双手比心不同。"}
	cfg.Categories[3].Actions = []Action{customMorning, customMiss}
	cfg.Categories[3].Subtitle = "我们的日常"
	for i := 0; i < 10; i++ {
		cfg.Categories[3].Actions = append(cfg.Categories[3].Actions, Action{
			ID: fmt.Sprintf("custom_%d", i), Name: "自定义动作", Icon: "✦", Prompt: "管理员保留的动作过程。",
		})
	}
	customTail := append([]Action(nil), cfg.Categories[3].Actions[1:]...)
	cfg.Categories = normalizeCategories(cfg.Categories)
	got := cfg.Categories[3]
	if got.Subtitle != "我们的日常" || got.Actions[0] != customMorning {
		t.Fatal("custom category or built-in action content was overwritten")
	}
	if len(got.Actions) != 37 || !reflect.DeepEqual(got.Actions[26:], customTail) {
		t.Fatal("legacy catalog must retain its custom actions and gain all timeline actions")
	}
	if err := validateSettings(cfg); err != nil {
		t.Fatal("migrated custom catalog cannot be saved", err)
	}
}

func TestDailyMissMigrationKeepsExistingHeart(t *testing.T) {
	for _, prompt := range []string{dailyActionHeart, "管理员自定义的比心过程。"} {
		for _, missFirst := range []bool{false, true} {
			cfg := defaults(Env{})
			heart := Action{"heart", "爱你哟", "💕", prompt}
			miss := Action{"miss", "想你了", "♡", dailyActionMissLegacy}
			cfg.Categories[3].Actions = []Action{heart, miss}
			if missFirst {
				cfg.Categories[3].Actions = []Action{miss, heart}
			}
			cfg.Categories = normalizeCategories(cfg.Categories)
			for _, a := range cfg.Categories[3].Actions {
				if a.ID == "miss" || (a.ID == "heart" && a != heart) {
					t.Fatal("legacy miss must not duplicate or overwrite an existing heart")
				}
			}
			if err := validateSettings(cfg); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDailyTimelineSettingsRoundTrip(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "daily-timeline-admin"))
	cfg, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	if w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": cfg}); w.Code != 200 {
		t.Fatal("expanded settings request rejected", w.Code, w.Body.String())
	}
	stored, err := a.settings()
	if err != nil || !reflect.DeepEqual(stored.Categories[3], cfg.Categories[3]) {
		t.Fatal("expanded daily catalog changed after saving", err)
	}
	w := request(t, a, admin, "GET", "/api/catalog", nil)
	var catalog struct {
		Categories []Category `json:"categories"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &catalog) != nil || len(catalog.Categories) != 5 {
		t.Fatal("cannot read public catalog", w.Code)
	}
	if len(catalog.Categories[3].Actions) != 26 {
		t.Fatal("public catalog missing timeline actions")
	}
	for i, action := range catalog.Categories[3].Actions {
		if action.ID != cfg.Categories[3].Actions[i].ID || action.Prompt != "" {
			t.Fatal("public catalog must preserve order without exposing prompts")
		}
	}
}

func TestCategoryActionLimits(t *testing.T) {
	for _, tc := range []struct{ index, limit int }{{0, 12}, {3, 48}, {4, 12}} {
		cfg := defaults(Env{})
		cfg.Categories[tc.index].Actions = nil
		for i := 0; i < tc.limit; i++ {
			cfg.Categories[tc.index].Actions = append(cfg.Categories[tc.index].Actions, Action{
				ID: fmt.Sprintf("custom_%d", i), Name: "测试动作", Prompt: "测试过程",
			})
		}
		if err := validateSettings(cfg); err != nil {
			t.Fatal("category at limit rejected", tc.index, err)
		}
		cfg.Categories[tc.index].Actions = append(cfg.Categories[tc.index].Actions, Action{ID: "overflow", Name: "超额", Prompt: "测试过程"})
		if err := validateSettings(cfg); err == nil {
			t.Fatal("category over limit accepted", tc.index)
		}
	}
}
