package app

import (
	"strings"
	"testing"
)

// 切格合成后的“杂质”来自跨格像素、半透明光晕和漂浮装饰；规格段必须对这几类都有明确禁令，
// 且 4×4/5×5/10×10 三种规格都带上同一套约束。
func TestMotionSpecPromptForbidsCellBleedAndStrayEffects(t *testing.T) {
	for _, id := range motionGridIDs() {
		p := motionSpecPrompt(id)
		for _, want := range []string{
			"独立的小画布", "不得越过格线", "不得出现邻格", "外发光", "地面投影", "速度线",
			"零散的光点、星星、爱心", "平涂色块", "省略发丝", "最后一格自然衔接第一格",
		} {
			if !strings.Contains(p, want) {
				t.Fatal(id, "spec prompt missing", want)
			}
		}
		if strings.Contains(p, "HTML padding") {
			t.Fatal(id, "spec prompt still uses the web-only padding metaphor")
		}
	}
}

// 成片只表达动作感：默认动作文案不应再带触发原因、情绪段、帧号区间或漂浮装饰指令。
func TestDefaultActionPromptsAreActionOnly(t *testing.T) {
	banned := []string{"触发原因", "情绪：", "情绪目的", "第1—6格", "第7—12格", "逐格浮现", "小爱心", "小星星", "感叹号", "气流线", "光点环绕"}
	for _, c := range defaultCategories() {
		for _, a := range c.Actions {
			for _, w := range banned {
				if strings.Contains(a.Prompt, w) {
					t.Fatalf("%s/%s still contains %q", c.ID, a.ID, w)
				}
			}
			if !strings.Contains(a.Prompt, "起始") || !strings.Contains(a.Prompt, "结束") {
				t.Fatalf("%s/%s lost start/end poses", c.ID, a.ID)
			}
		}
	}
	for _, p := range compareMotionPresets() {
		for _, w := range banned {
			if strings.Contains(p.Prompt, w) {
				t.Fatalf("compare preset %s still contains %q", p.ID, w)
			}
		}
	}
}

func TestDefaultDraftPromptDropsMotionOnlyAndRedundantBlocks(t *testing.T) {
	for _, gone := range []string{"25 格", "头身比锁定", "绝对最高优先级", "违反整张作废"} {
		if strings.Contains(compareDraftPrompt, gone) {
			t.Fatal("draft prompt still contains", gone)
		}
	}
	for _, keep := range []string{"{{clothes}}", "5:1", "正面头部近景", "正面完整全身", "无动作序列"} {
		if !strings.Contains(compareDraftPrompt, keep) {
			t.Fatal("draft prompt lost", keep)
		}
	}
}

// 已有部署的数据库里存着上一版出厂提示词：原文未改的升级，管理员改过的保留。
func TestRetiredDefaultPromptsUpgradeButCustomPromptsStay(t *testing.T) {
	defs := defaultCategories()[0].Actions // male: bike 在其中
	var bike Action
	for _, a := range defs {
		if a.ID == "bike" {
			bike = a
		}
	}
	// 旧出厂原文已不在代码里：临时把一段文本登记为 bike 的“旧出厂摘要”，验证命中时升级为新版。
	const legacy = "上一版出厂的骑车过程原文。"
	saved := retiredActionPromptHashes["bike"]
	retiredActionPromptHashes["bike"] = promptDigest(legacy)
	upgraded := upgradeRetiredActionPrompts([]Action{{ID: "bike", Name: "骑车", Icon: "🚲", Prompt: "  " + legacy + "\n"}}, defs)
	retiredActionPromptHashes["bike"] = saved
	if upgraded[0].Prompt != bike.Prompt || upgraded[0].Name != "骑车" {
		t.Fatal("legacy default was not upgraded in place", upgraded[0])
	}
	custom := []Action{{ID: "bike", Name: "骑车", Icon: "🚲", Prompt: "管理员自己写的骑车过程。"}}
	got := upgradeRetiredActionPrompts(append([]Action(nil), custom...), defs)
	if got[0].Prompt != custom[0].Prompt {
		t.Fatal("custom prompt was overwritten")
	}
	for _, c := range defaultCategories() {
		for _, a := range c.Actions {
			if _, ok := retiredActionPromptHashes[a.ID]; !ok {
				t.Fatal("retired hash missing for", a.ID)
			}
		}
	}
	if promptDigest(bike.Prompt) == retiredActionPromptHashes["bike"] {
		t.Fatal("new bike prompt must differ from the retired one")
	}
	cfg := defaults(Env{})
	cfg.DraftPrompt = "不是旧出厂文案的自定义定稿提示词，长度足够通过校验。"
	if got := migratePromptsToV162(cfg).DraftPrompt; got != cfg.DraftPrompt {
		t.Fatal("custom draft prompt was overwritten")
	}
}
