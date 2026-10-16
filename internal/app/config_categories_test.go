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
	if len(got) != 5 || got[3].ID != "daily" || got[4].ID != "emotion" {
		t.Fatalf("expected daily and emotion as the last categories, got %+v", got)
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
	if len(cfg.Categories) != 5 || cfg.Categories[3].ID != "daily" || cfg.Categories[4].ID != "emotion" {
		t.Fatalf("categories=%+v", cfg.Categories)
	}
	daily := cfg.Categories[3]
	if len(daily.Actions) != 26 {
		t.Fatalf("daily should ship 26 handbook actions, got %d", len(daily.Actions))
	}
	wantIDs := []string{"eat", "hello_wave", "cry", "dance", "gift", "heart"}
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
	emotion := cfg.Categories[4]
	wantEmotion := []string{"happy", "confused", "shock", "stomp", "contempt"}
	if len(emotion.Actions) != len(wantEmotion) {
		t.Fatalf("emotion should ship %d handbook actions, got %d", len(wantEmotion), len(emotion.Actions))
	}
	for i, id := range wantEmotion {
		if emotion.Actions[i].ID != id || emotion.Actions[i].Name == "" || !strings.Contains(emotion.Actions[i].Prompt, "【动作：") {
			t.Fatal("emotion action malformed", i, emotion.Actions[i])
		}
	}
	got := buildMotionPrompt(have["eat"].Prompt, "5x5")
	if !strings.Contains(got, "图1是已确认角色定稿") || !strings.Contains(got, "【动作：干饭】") {
		t.Fatal("built-in motion prompt missing", got)
	}
	for _, want := range []string{"水平垂直居中", "8%–12%", "禁止越往后越放大", "约80%", "独立的小画布", "不得越过格线", "外发光"} {
		if !strings.Contains(got, want) {
			t.Fatal("motion cell padding layout missing", want, got)
		}
	}
}

func TestNormalizeCategoriesMovesLegacyEmotionActions(t *testing.T) {
	legacy := defaultCategories()[:4]
	legacy[3].Actions = append(legacy[3].Actions,
		Action{"shock", "惊讶后仰", "😲", dailyActionShock},
		Action{"stomp", "生气跺脚", "💢", dailyActionStomp},
	)
	got := normalizeCategories(legacy)
	if got[3].ID != "daily" || got[4].ID != "emotion" {
		t.Fatalf("unexpected category order: %+v", got)
	}
	for _, a := range got[3].Actions {
		if a.ID == "shock" || a.ID == "stomp" {
			t.Fatal("legacy emotion action remained in daily", a)
		}
	}
	seen := map[string]Action{}
	for _, a := range got[4].Actions {
		seen[a.ID] = a
	}
	if seen["shock"].Name != "惊讶" || seen["stomp"].Name != "愤怒" || seen["shock"].Prompt != emotionActionSurprised || seen["stomp"].Prompt != emotionActionAngry {
		t.Fatal("legacy emotion actions were not upgraded", seen)
	}
	if err := validateSettings(defaults(Env{})); err != nil {
		t.Fatal("emotion defaults should validate", err)
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
	if !strings.Contains(have["eat"].Prompt, "完整在格内") {
		t.Fatal("eat legacy prompt should upgrade", have["eat"].Prompt)
	}
	for _, id := range []string{"hello_wave", "cry", "dance", "gift"} {
		if _, ok := have[id]; !ok {
			t.Fatal("missing ensured handbook action", id)
		}
	}
	emotion := map[string]Action{}
	for _, a := range got[4].Actions {
		emotion[a.ID] = a
	}
	for _, id := range []string{"stomp", "shock"} {
		if _, ok := emotion[id]; !ok {
			t.Fatal("missing ensured emotion action", id)
		}
	}
	if len(got[3].Actions) > categoryActionLimit("daily") {
		t.Fatal("daily actions exceeded capacity", len(got[3].Actions))
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

func TestDefaultsAndNormalizeChildHandbookActions(t *testing.T) {
	wantIDs := []string{"bubbles", "lollipop", "peekaboo", "blocks", "airplane", "carry_me"}
	child := defaults(Env{}).Categories[2]
	if child.ID != "child" || len(child.Actions) != len(wantIDs) {
		t.Fatalf("child should ship %d handbook actions, got id=%s n=%d", len(wantIDs), child.ID, len(child.Actions))
	}
	for i, a := range child.Actions {
		if a.ID != wantIDs[i] || !strings.Contains(a.Prompt, "【动作：") || !strings.Contains(a.Prompt, "主动作只有一条") {
			t.Fatal("child handbook action malformed", a.ID)
		}
	}

	legacy := defaultCategories()
	legacy[2].Actions = []Action{
		{ID: "wave", Name: "你好呀", Icon: "👋", Prompt: "挥手"},
		{ID: "clap", Name: "好棒好棒", Icon: "👏", Prompt: "拍手"},
		{ID: "jump", Name: "耶！成功啦", Icon: "🎉", Prompt: "跳"},
		{ID: "hug", Name: "抱抱玩偶", Icon: "🧸", Prompt: "抱"},
		{ID: "curious", Name: "好奇看看", Icon: "🔍", Prompt: "看"},
		{ID: "sleep", Name: "困了晚安", Icon: "☾", Prompt: "睡"},
	}
	got := normalizeCategories(legacy)[2].Actions
	if len(got) != len(wantIDs) {
		t.Fatalf("child legacy actions should be replaced, got %+v", got)
	}
	for i, a := range got {
		if a.ID != wantIDs[i] {
			t.Fatal("unexpected child action after normalize", a.ID)
		}
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
