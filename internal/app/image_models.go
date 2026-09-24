package app

import "strings"

// ImageModel 描述 OpenRouter 画图模型在本地对比与正式生成时的适配方式。
type ImageModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Vendor   string `json:"vendor"`
	Family   string `json:"family"` // openai | seedream | banana | qwen | flux | mai | recraft | krea | riverflow | grok
	Latest   bool   `json:"latest"`
	Supports bool   `json:"supportsRef"`
}

const openRouterAPIBase = "https://openrouter.ai/api/v1"

// latestImageModels 每个系列取 OpenRouter 文生图能力最强的 2 个（来自 /api/v1/images/models）。
func latestImageModels() []ImageModel {
	return []ImageModel{
		{ID: "openai/gpt-5.4-image-2", Name: "GPT-5.4 Image 2", Vendor: "OpenAI", Family: "openai", Latest: true, Supports: true},
		{ID: "openai/gpt-image-2.5-sunburst", Name: "GPT Image 2.5 Sunburst", Vendor: "OpenAI", Family: "openai", Latest: true, Supports: true},

		{ID: "bytedance-seed/seedream-5-0-pro", Name: "Seedream 5.0 Pro", Vendor: "ByteDance", Family: "seedream", Latest: true, Supports: true},
		{ID: "bytedance-seed/seedream-5-0-lite", Name: "Seedream 5.0 Lite", Vendor: "ByteDance", Family: "seedream", Latest: true, Supports: true},

		{ID: "google/gemini-3-pro-image", Name: "Nano Banana Pro", Vendor: "Google", Family: "banana", Latest: true, Supports: true},
		{ID: "google/gemini-3.1-flash-image", Name: "Nano Banana 2", Vendor: "Google", Family: "banana", Latest: true, Supports: true},

		{ID: "qwen/qwen-image-3-pro", Name: "Qwen Image 3 Pro", Vendor: "Qwen", Family: "qwen", Latest: true, Supports: true},
		{ID: "qwen/qwen-image-3", Name: "Qwen Image 3", Vendor: "Qwen", Family: "qwen", Latest: true, Supports: true},

		{ID: "black-forest-labs/flux.2-max", Name: "FLUX.2 Max", Vendor: "Black Forest Labs", Family: "flux", Latest: true, Supports: true},
		{ID: "black-forest-labs/flux.2-pro", Name: "FLUX.2 Pro", Vendor: "Black Forest Labs", Family: "flux", Latest: true, Supports: true},

		{ID: "microsoft/mai-image-2.6", Name: "MAI-Image-2.6", Vendor: "Microsoft", Family: "mai", Latest: true, Supports: true},
		{ID: "microsoft/mai-image-2.6-flash", Name: "MAI-Image-2.6 Flash", Vendor: "Microsoft", Family: "mai", Latest: true, Supports: true},

		{ID: "recraft/recraft-v4.1-pro", Name: "Recraft V4.1 Pro", Vendor: "Recraft", Family: "recraft", Latest: true, Supports: true},
		{ID: "recraft/recraft-v4.1", Name: "Recraft V4.1", Vendor: "Recraft", Family: "recraft", Latest: true, Supports: true},

		{ID: "krea/krea-2-large", Name: "Krea 2 Large", Vendor: "Krea", Family: "krea", Latest: true, Supports: true},
		{ID: "krea/krea-2-medium", Name: "Krea 2 Medium", Vendor: "Krea", Family: "krea", Latest: true, Supports: true},

		{ID: "sourceful/riverflow-v2.5-pro", Name: "Riverflow V2.5 Pro", Vendor: "Sourceful", Family: "riverflow", Latest: true, Supports: true},
		{ID: "sourceful/riverflow-v2.5-fast", Name: "Riverflow V2.5 Fast", Vendor: "Sourceful", Family: "riverflow", Latest: true, Supports: true},

		{ID: "x-ai/grok-imagine-image-2.0", Name: "Grok Imagine Image 2.0", Vendor: "xAI", Family: "grok", Latest: true, Supports: true},
		{ID: "x-ai/grok-imagine-image-quality", Name: "Grok Imagine Image Quality", Vendor: "xAI", Family: "grok", Latest: true, Supports: true},
	}
}

func imageModelByID(id string) (ImageModel, bool) {
	for _, m := range latestImageModels() {
		if m.ID == id {
			return m, true
		}
	}
	return ImageModel{}, false
}

func allowedImageModelIDs() []string {
	out := make([]string, 0, len(latestImageModels())+8)
	for _, m := range latestImageModels() {
		out = append(out, m.ID)
	}
	// 兼容旧后台仍可能存着的 GeekAI 短 ID / OpenRouter 变体。
	for _, id := range []string{
		"openai/gpt-image-2.5-flare", "openai/gpt-image-2", "openai/gpt-5-image", "openai/gpt-5-image-mini",
		"gpt-image-2.5-sunburst", "gpt-image-2.5-flare", "gpt-image-2",
	} {
		out = append(out, id)
	}
	return out
}

func modelFamily(id string) string {
	if m, ok := imageModelByID(id); ok {
		return m.Family
	}
	switch {
	case stringsHasPrefix(id, "openai/gpt"):
		return "openai"
	case stringsHasPrefix(id, "gpt-image"):
		return "openai"
	case stringsHasPrefix(id, "bytedance-seed/") || stringsHasPrefix(id, "doubao-seedream"):
		return "seedream"
	case stringsHasPrefix(id, "google/gemini") || stringsHasPrefix(id, "nano-banana"):
		return "banana"
	case stringsHasPrefix(id, "qwen/"):
		return "qwen"
	case stringsHasPrefix(id, "black-forest-labs/") || stringsHasPrefix(id, "flux"):
		return "flux"
	case stringsHasPrefix(id, "microsoft/mai"):
		return "mai"
	case stringsHasPrefix(id, "recraft/"):
		return "recraft"
	case stringsHasPrefix(id, "krea/"):
		return "krea"
	case stringsHasPrefix(id, "sourceful/"):
		return "riverflow"
	case stringsHasPrefix(id, "x-ai/") || stringsHasPrefix(id, "xai/"):
		return "grok"
	default:
		return "generic"
	}
}

func stringsHasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func openRouterQuality(quality string) string {
	switch quality {
	case "low", "medium", "high":
		return quality
	case "xhigh", "max":
		return "high"
	default:
		return "high"
	}
}

// buildImagePayload 按模型族只填 OpenRouter 端点实际支持的字段。
func buildImagePayload(model, prompt, imageSize, quality string, images []string) map[string]any {
	_ = imageSize
	fam := modelFamily(model)
	payload := map[string]any{
		"model":        model,
		"prompt":       prompt,
		"aspect_ratio": "1:1",
	}
	switch fam {
	case "openai":
		// GPT Image 走 aspect_ratio，不接受 size=1K；5.4 系 background 无 transparent。
		payload["n"] = 1
		payload["quality"] = openRouterQuality(quality)
		if strings.Contains(model, "gpt-5.4") || strings.Contains(model, "/gpt-5-image") {
			payload["background"] = "auto"
		} else {
			payload["background"] = "transparent"
		}
	case "seedream":
		// 5.0 Lite 仅支持 2K/4K；Pro 支持 1K/2K。都不接受 output_format。
		payload["n"] = 1
		if strings.Contains(model, "lite") {
			payload["resolution"] = "2K"
		} else {
			payload["resolution"] = "1K"
		}
	case "banana", "qwen":
		payload["n"] = 1
		payload["resolution"] = "1K"
	case "flux":
		payload["n"] = 1
		payload["output_format"] = "png"
	case "mai", "recraft":
		payload["n"] = 1
	case "krea":
		// Krea 无 n 参数，resolution 仅 1K。
		payload["resolution"] = "1K"
	case "riverflow":
		payload["n"] = 1
		payload["resolution"] = "1K"
		payload["output_format"] = "png"
	case "grok":
		payload["n"] = 1
		payload["resolution"] = "1K"
		q := openRouterQuality(quality)
		if q == "high" {
			q = "medium"
		}
		payload["quality"] = q
	default:
		payload["n"] = 1
		payload["resolution"] = "1K"
	}
	if len(images) > 0 {
		refs := make([]map[string]any, 0, len(images))
		for _, img := range images {
			refs = append(refs, map[string]any{
				"type": "image_url",
				"image_url": map[string]string{
					"url": img,
				},
			})
		}
		payload["input_references"] = refs
	}
	return payload
}

// migrateToOpenRouter 把旧 GeekAI 默认接口/短模型 ID 迁移到 OpenRouter。
func migrateToOpenRouter(s Settings) Settings {
	if s.APIBase == "" || s.APIBase == "https://geekai.co/api/v1" {
		s.APIBase = openRouterAPIBase
	}
	needAssets := len(s.AssetHosts) == 0
	for _, h := range s.AssetHosts {
		if strings.Contains(h, "geekai") {
			needAssets = true
			break
		}
	}
	if needAssets {
		s.AssetHosts = []string{"openrouter.ai"}
	}
	legacy := map[string]string{
		"gpt-image-2.5-sunburst":     "openai/gpt-image-2.5-sunburst",
		"gpt-image-2.5-flare":        "openai/gpt-image-2.5-flare",
		"gpt-image-2":                "openai/gpt-image-2",
		"gpt-image-2.5-sunburst-all": "openai/gpt-image-2.5-sunburst",
		"gpt-image-2.5-flare-all":    "openai/gpt-image-2.5-flare",
		"doubao-seedream-5.0-pro":    "bytedance-seed/seedream-5-0-pro",
		"doubao-seedream-5.0-lite":   "bytedance-seed/seedream-5-0-lite",
		"nano-banana-pro":            "google/gemini-3-pro-image",
		"nano-banana-2":              "google/gemini-3.1-flash-image",
		"qwen-image-3.0-pro":         "qwen/qwen-image-3-pro",
		"qwen-image-3.0":             "qwen/qwen-image-3",
		"kling-image-v3-omni":        "black-forest-labs/flux.2-max",
		"jimeng_t2i_v40":             "krea/krea-2-large",
		"stable-image-ultra":         "black-forest-labs/flux.2-pro",
		"wan2.7-image":               "qwen/qwen-image-3",
	}
	if mapped, ok := legacy[s.Model]; ok {
		s.Model = mapped
	}
	if !contains(allowedImageModelIDs(), s.Model) {
		s.Model = "openai/gpt-image-2.5-sunburst"
	}
	return s
}
