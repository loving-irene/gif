package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdminActionPromptsExportMatchesGenerate(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "action-prompts-admin"))
	cfg, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	var daily Category
	var morning Action
	for _, c := range cfg.Categories {
		if c.ID != "daily" {
			continue
		}
		daily = c
		for _, ac := range c.Actions {
			if ac.ID == "morning" {
				morning = ac
			}
		}
	}
	if daily.ID == "" || morning.ID == "" {
		t.Fatal("daily morning action missing")
	}
	w := request(t, a, admin, "POST", "/api/admin/action-prompts", map[string]any{
		"category":     "daily",
		"actionName":   morning.Name,
		"actionPrompt": morning.Prompt,
		"draftPrompt":  cfg.DraftPrompt,
		"motionGrid":   "5x5",
	})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		DraftPrompt  string `json:"draftPrompt"`
		MotionPrompt string `json:"motionPrompt"`
		Export       string `json:"export"`
		MotionGrid   string `json:"motionGrid"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	sel := Selection{Category: "daily"}
	cat, err := normalizeSelection(cfg, &sel)
	if err != nil {
		t.Fatal(err)
	}
	wantDraft := buildDraftPrompt(cfg, sel, cat)
	wantMotion := buildMotionPrompt(morning.Prompt, "5x5")
	if out.DraftPrompt != wantDraft {
		t.Fatal("draft prompt mismatch")
	}
	if out.MotionPrompt != wantMotion {
		t.Fatal("motion prompt mismatch")
	}
	if out.MotionGrid != "5x5" {
		t.Fatal("motion grid", out.MotionGrid)
	}
	for _, want := range []string{
		"【定稿图请求】",
		"参考图：自拍（image1）",
		wantDraft,
		"【动作序列图请求】",
		"参考图：定稿（image1）、自拍（image2）",
		wantMotion,
		"日常 · " + morning.Name,
		"HTML padding",
	} {
		if !strings.Contains(out.Export, want) {
			t.Fatal("export missing", want)
		}
	}
}

func TestAdminActionPromptsRequiresActionTextAndAdmin(t *testing.T) {
	a := testApp(t)
	user := loginDevice(t, a, "action-prompts-user")
	if w := request(t, a, user, "POST", "/api/admin/action-prompts", map[string]any{
		"category": "daily", "actionPrompt": "挥手",
	}); w.Code != 403 {
		t.Fatal("non-admin accepted", w.Code)
	}
	admin := codeAdmin(t, a, loginDevice(t, a, "action-prompts-empty"))
	if w := request(t, a, admin, "POST", "/api/admin/action-prompts", map[string]any{
		"category": "daily", "actionPrompt": "   ",
	}); w.Code != 400 || !strings.Contains(w.Body.String(), "动作过程") {
		t.Fatal("empty action accepted", w.Code, w.Body.String())
	}
}

func TestFormatActionPromptExportLayout(t *testing.T) {
	got := formatActionPromptExport("日常", "早呀", "定稿正文", "动作正文")
	for _, want := range []string{
		"日常 · 早呀",
		"【定稿图请求】",
		"定稿正文",
		"【动作序列图请求】",
		"动作正文",
	} {
		if !strings.Contains(got, want) {
			t.Fatal("missing", want, got)
		}
	}
}
