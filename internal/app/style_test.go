package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func cloneSettings(t *testing.T, s Settings) Settings {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var out Settings
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// 画风目录固定为默认、Q版、水墨风格三项，前台只拿到名称与说明，不返回提示词内容。
func TestStyleCatalogHidesPrompts(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "style-catalog")
	w := request(t, a, s, "GET", "/api/catalog", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Styles  []Style  `json:"styles"`
		Actions []Action `json:"actions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Styles) != 3 {
		t.Fatal("catalog should expose three styles", w.Body.String())
	}
	if len(out.Actions) == 0 {
		t.Fatal("catalog should expose flattened actions", w.Body.String())
	}
	for i, id := range styleIDs {
		if out.Styles[i].ID != id || out.Styles[i].Name == "" || out.Styles[i].Subtitle == "" || out.Styles[i].Icon == "" {
			t.Fatal("style entry incomplete", i, out.Styles[i])
		}
		if out.Styles[i].Prompt != "" {
			t.Fatal("style prompt leaked to catalog", out.Styles[i])
		}
	}
	cfg, _ := a.settings()
	for _, st := range cfg.Styles {
		if strings.Contains(w.Body.String(), st.Prompt) {
			t.Fatal("style prompt text leaked to catalog")
		}
	}
}

// 简化流程后画风提示词不再拼入定稿/动作；空 selection 由服务端补默认，未知画风仍拒绝。
func TestGenerateAppliesSelectedStyle(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "style-generate")
	cfg, _ := a.settings()
	var prompts []string
	a.providerCall = asProviderCall(func(_ context.Context, _ Settings, prompt string, _ []string) (string, error) {
		prompts = append(prompts, prompt)
		return sampleImage(false), nil
	})

	empty := draftInput()
	empty.Selection = Selection{}
	id := jobID(t, request(t, a, s, "POST", "/api/generate", empty))
	if j := waitJob(t, a, s, id); j.Status != "succeeded" {
		t.Fatal(j)
	}
	if len(prompts) != 1 {
		t.Fatal("expected one draft prompt", prompts)
	}
	if !strings.Contains(prompts[0], "image1") && len(prompts[0]) < 20 {
		t.Fatal("draft prompt missing", prompts[0])
	}
	for _, st := range cfg.Styles {
		if strings.Contains(prompts[0], st.Prompt) {
			t.Fatal("style prompt should not be appended after simplify", st.ID)
		}
	}

	bad := draftInput()
	bad.Selection.Style = "oil-painting"
	if w := request(t, a, s, "POST", "/api/generate", bad); w.Code != 400 || !strings.Contains(w.Body.String(), "画风") {
		t.Fatal("unknown style accepted", w.Code, w.Body.String())
	}
}

// 画风仍绑定在定稿凭证中：默认补齐后动作可通过；换画风必须先重新定稿。
func TestStyleIsBoundToDraftReceipt(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "style-receipt")
	var prompts []string
	a.providerCall = asProviderCall(func(_ context.Context, _ Settings, prompt string, _ []string) (string, error) {
		prompts = append(prompts, prompt)
		return sampleImage(false), nil
	})
	input := draftInput()
	input.Selection = Selection{}
	id := jobID(t, request(t, a, s, "POST", "/api/generate", input))
	j := waitJob(t, a, s, id)
	if j.Status != "succeeded" {
		t.Fatal(j)
	}
	w := request(t, a, s, "POST", "/api/accept", map[string]string{"receipt": j.Receipt})
	var accepted map[string]string
	json.Unmarshal(w.Body.Bytes(), &accepted)
	motion := input
	motion.RequestID = token(16)
	motion.Kind = "motion"
	motion.Action = "attack"
	motion.Draft = j.Image
	motion.Receipt = accepted["receipt"]
	motion.Selection = Selection{}
	motionID := jobID(t, request(t, a, s, "POST", "/api/generate", motion))
	if j2 := waitJob(t, a, s, motionID); j2.Status != "succeeded" {
		t.Fatal(j2)
	}
	if len(prompts) != 2 {
		t.Fatal("unexpected prompt count", len(prompts))
	}
	cfg, _ := a.settings()
	attack := ""
	for _, c := range cfg.Categories {
		for _, ac := range c.Actions {
			if ac.ID == "attack" {
				attack = ac.Prompt
			}
		}
	}
	if attack == "" || !strings.Contains(prompts[1], attack) {
		t.Fatal("action text missing from motion prompt")
	}
	motion.RequestID = token(16)
	motion.Selection.Style = "chibi"
	if w = request(t, a, s, "POST", "/api/generate", motion); w.Code != 400 {
		t.Fatal("style change bypassed draft approval", w.Code, w.Body.String())
	}
}

// 旧数据库配置没有画风字段时自动补入三种画风，旧后台页面保存也不会清空或丢失画风。
func TestStyleBackfillAndAdminSave(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "style-admin"))
	cfg, _ := a.settings()
	cfg.Styles = nil
	raw, _ := json.Marshal(cfg)
	if _, err := a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw)); err != nil {
		t.Fatal(err)
	}
	got, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Styles) != 3 || got.Styles[0].ID != defaultStyleID || got.Styles[2].Prompt == "" {
		t.Fatal("legacy config not backfilled", got.Styles)
	}
	// 旧后台页面提交的配置不含画风：保存后仍应补入默认画风。
	if w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": got}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ := a.settings()
	if len(saved.Styles) != 3 || saved.Styles[2].ID != "ink" {
		t.Fatal("styles lost on admin save", saved.Styles)
	}
	// 后台可以修改画风提示词，修改立即生效；非法画风配置被拒绝。
	edited := cloneSettings(t, saved)
	edited.Styles[1].Prompt = "Q版测试画风提示词，头身比约1:3，保留本人五官结构、发型、发色与眼镜。"
	if w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": edited}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	stored, _ := a.settings()
	if stored.Styles[1].Prompt != edited.Styles[1].Prompt {
		t.Fatal("style prompt not saved", stored.Styles[1])
	}
	short := cloneSettings(t, stored)
	short.Styles[2].Prompt = "太短"
	if w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": short}); w.Code != 400 {
		t.Fatal("short style prompt accepted", w.Code, w.Body.String())
	}
	missing := cloneSettings(t, stored)
	missing.Styles = missing.Styles[:2]
	if w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": missing}); w.Code != 400 {
		t.Fatal("incomplete styles accepted", w.Code, w.Body.String())
	}
	unknown := cloneSettings(t, stored)
	unknown.Styles[0].ID = "other"
	if w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": unknown}); w.Code != 400 {
		t.Fatal("unknown style id accepted", w.Code, w.Body.String())
	}
}
