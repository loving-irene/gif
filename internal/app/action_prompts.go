package app

import (
	"net/http"
	"strings"
)

// actionPromptExportInput 供管理后台按当前表单（可未保存）导出两次上游请求的完整提示词。
type actionPromptExportInput struct {
	Category     string `json:"category"`
	ActionName   string `json:"actionName"`
	ActionPrompt string `json:"actionPrompt"`
	DraftPrompt  string `json:"draftPrompt"`
	MotionGrid   string `json:"motionGrid"`
	Clothes      string `json:"clothes"`
	Color        string `json:"color"`
	Weapon       string `json:"weapon"`
}

// adminActionPrompts 导出定稿图请求与动作序列图请求的完整提示词，逻辑与正式生成一致。
func (a *App) adminActionPrompts(w http.ResponseWriter, r *http.Request) {
	var in actionPromptExportInput
	if err := decode(w, r, &in, 64000); err != nil {
		fail(w, 400, err.Error())
		return
	}
	actionText := strings.TrimSpace(in.ActionPrompt)
	if actionText == "" {
		fail(w, 400, "请先填写动作过程")
		return
	}
	if len(actionText) > 12000 {
		fail(w, 400, "动作过程过长")
		return
	}
	cfg, err := a.settings()
	if err != nil {
		fail(w, 500, "配置读取失败")
		return
	}
	if draft := strings.TrimSpace(in.DraftPrompt); draft != "" {
		cfg.DraftPrompt = draft
	}
	if grid := strings.TrimSpace(in.MotionGrid); grid != "" {
		cfg.MotionGrid = grid
	}
	sel := Selection{
		Category: strings.TrimSpace(in.Category),
		Clothes:  strings.TrimSpace(in.Clothes),
		Color:    strings.TrimSpace(in.Color),
		Weapon:   strings.TrimSpace(in.Weapon),
	}
	cat, err := normalizeSelection(cfg, &sel)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	draftPrompt := buildDraftPrompt(cfg, sel, cat)
	motionPrompt := buildMotionPrompt(actionText, cfg.MotionGrid)
	export := formatActionPromptExport(cat.Name, in.ActionName, draftPrompt, motionPrompt)
	respond(w, 200, map[string]any{
		"category":      cat.ID,
		"categoryName":  cat.Name,
		"actionName":    strings.TrimSpace(in.ActionName),
		"selection":     sel,
		"motionGrid":    motionSpecOf(cfg.MotionGrid).id,
		"draftPrompt":   draftPrompt,
		"motionPrompt":  motionPrompt,
		"export":        export,
		"draftImages":   []string{"selfie"},
		"motionImages":  []string{"draft", "selfie"},
	})
}
