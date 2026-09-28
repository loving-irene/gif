package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOutfitCatalogAndDraftPromptFill(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "outfit-catalog")
	w := request(t, a, s, "GET", "/api/catalog", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Outfits []Outfit `json:"outfits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Outfits) != 9 {
		t.Fatalf("catalog should expose 9 outfits, got %d", len(out.Outfits))
	}
	for _, o := range out.Outfits {
		if o.Prompt != "" {
			t.Fatal("catalog must hide outfit prompt text", o.ID)
		}
		if o.ID == "" || o.Name == "" {
			t.Fatal("outfit id/name missing", o)
		}
	}

	cfg := defaults(Env{})
	sel := Selection{Category: "daily", Clothes: "black_formal"}
	cat, err := normalizeSelection(cfg, &sel)
	if err != nil {
		t.Fatal(err)
	}
	prompt := buildDraftPrompt(cfg, sel, cat)
	if !strings.Contains(prompt, "【服装】") {
		t.Fatal("draft prompt missing clothing section")
	}
	if !strings.Contains(prompt, "黑色缎面翻领燕尾服") {
		t.Fatal("selected outfit text not filled into draft prompt", prompt)
	}
	if strings.Contains(prompt, "{{clothes}}") {
		t.Fatal("clothes placeholder not replaced")
	}

	sel = Selection{Category: "daily"}
	cat, err = normalizeSelection(cfg, &sel)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Clothes != defaultOutfitID {
		t.Fatal("empty clothes should default", sel.Clothes)
	}
	prompt = buildDraftPrompt(cfg, sel, cat)
	if !strings.Contains(prompt, "白色翻领短袖") {
		t.Fatal("default outfit text missing", prompt)
	}
}

func TestMigrateDraftPromptClothesPlaceholder(t *testing.T) {
	cases := []string{
		"前缀\n【服装】\n上衣沿用参考图的白色翻领短袖款式，简化为适合微型身体的轮廓和少量褶皱，省略胸前标志。\n照片未展示的下装设计为简洁深蓝色短裤和小白鞋。\n服装贴合小身体，不用宽大衣服增加身体体积。\n\n【画风】\n后文",
		"前缀\n【服装】\n上衣：白色翻领短袖，简化为适合微型身体的轮廓和少量褶皱，省略胸前标志。\n下装：简洁深蓝色短裤 + 小白鞋（照片未显示部分自行补全）。\n服装贴合小身体，不用宽大衣服增加体积。\n\n【画风】\n后文",
	}
	for _, old := range cases {
		got := migrateDraftClothesPlaceholder(old)
		if !strings.Contains(got, "{{clothes}}") {
			t.Fatal("hardcoded clothing should migrate to placeholder", got)
		}
		if strings.Contains(got, "白色翻领短袖") {
			t.Fatal("legacy clothing block still present", got)
		}
		if !strings.Contains(got, "【画风】") {
			t.Fatal("following sections must stay", got)
		}
	}
	kept := "【服装】\n{{clothes}}\n\n【画风】\nx"
	if migrateDraftClothesPlaceholder(kept) != kept {
		t.Fatal("existing placeholder must not change")
	}
}
