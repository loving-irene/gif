package app

import "strings"

// ImageModel 描述画图模型在本地对比与正式生成时的适配方式。
type ImageModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Vendor   string `json:"vendor"`
	Family   string `json:"family"` // openai | seedream | banana | qwen | flux | mai | recraft | krea | riverflow | grok | generic
	Latest   bool   `json:"latest"`
	Supports bool   `json:"supportsRef"`
}

// ImageProvider 是后台「基础与接口」可选的画图中转供应商。
type ImageProvider struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	APIBase    string       `json:"apiBase"`
	AssetHosts []string     `json:"assetHosts"`
	Models     []ImageModel `json:"models"`
}

const (
	openRouterAPIBase = "https://openrouter.ai/api/v1"
	geekAIAPIBase     = "https://geekai.co/api/v1"
)

func imageProviders() []ImageProvider {
	return []ImageProvider{
		{
			ID: "openrouter", Name: "OpenRouter", APIBase: openRouterAPIBase,
			AssetHosts: []string{"openrouter.ai"}, Models: latestImageModels(),
		},
		{
			ID: "geekai", Name: "GeekAI", APIBase: geekAIAPIBase,
			AssetHosts: []string{"static.geekai.co", "geekai.co"}, Models: geekAIImageModels(),
		},
	}
}

func imageProviderByID(id string) (ImageProvider, bool) {
	for _, p := range imageProviders() {
		if p.ID == id {
			return p, true
		}
	}
	return ImageProvider{}, false
}

func imageProviderByAPIBase(apiBase string) (ImageProvider, bool) {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	for _, p := range imageProviders() {
		if strings.TrimRight(p.APIBase, "/") == base {
			return p, true
		}
	}
	return ImageProvider{}, false
}

func allowedAPIBases() []string {
	out := make([]string, 0, len(imageProviders()))
	for _, p := range imageProviders() {
		out = append(out, p.APIBase)
	}
	return out
}

// geekAIImageModels 是 GeekAI 中转常用的短 ID 模型列表（与 OpenRouter 的 vendor/model 路径不同）。
func geekAIImageModels() []ImageModel {
	return []ImageModel{
		{ID: "gpt-image-2.5-sunburst", Name: "GPT Image 2.5 Sunburst", Vendor: "OpenAI", Family: "openai", Latest: true, Supports: true},
		{ID: "gpt-image-2.5-flare", Name: "GPT Image 2.5 Flare", Vendor: "OpenAI", Family: "openai", Latest: true, Supports: true},
		{ID: "gpt-image-2", Name: "GPT Image 2", Vendor: "OpenAI", Family: "openai", Latest: true, Supports: true},
		{ID: "doubao-seedream-5.0-pro", Name: "Seedream 5.0 Pro", Vendor: "ByteDance", Family: "seedream", Latest: true, Supports: true},
		{ID: "doubao-seedream-5.0-lite", Name: "Seedream 5.0 Lite", Vendor: "ByteDance", Family: "seedream", Latest: true, Supports: true},
		{ID: "nano-banana-pro", Name: "Nano Banana Pro", Vendor: "Google", Family: "banana", Latest: true, Supports: true},
		{ID: "nano-banana-2", Name: "Nano Banana 2", Vendor: "Google", Family: "banana", Latest: true, Supports: true},
		{ID: "qwen-image-3.0-pro", Name: "Qwen Image 3.0 Pro", Vendor: "Qwen", Family: "qwen", Latest: true, Supports: true},
		{ID: "qwen-image-3.0", Name: "Qwen Image 3.0", Vendor: "Qwen", Family: "qwen", Latest: true, Supports: true},
		{ID: "kling-image-v3-omni", Name: "Kling Image V3 Omni", Vendor: "Kling", Family: "generic", Latest: true, Supports: true},
		{ID: "jimeng_t2i_v40", Name: "即梦 4.0", Vendor: "Jimeng", Family: "generic", Latest: true, Supports: true},
		{ID: "stable-image-ultra", Name: "Stable Image Ultra", Vendor: "Stability", Family: "generic", Latest: true, Supports: true},
		{ID: "wan2.7-image", Name: "万相 2.7", Vendor: "Wan", Family: "generic", Latest: true, Supports: true},
	}
}

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
	for _, p := range imageProviders() {
		for _, m := range p.Models {
			if m.ID == id {
				return m, true
			}
		}
	}
	return ImageModel{}, false
}

func allowedImageModelIDs() []string {
	out := make([]string, 0, 48)
	seen := map[string]bool{}
	for _, p := range imageProviders() {
		for _, m := range p.Models {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			out = append(out, m.ID)
		}
	}
	// 兼容旧后台仍可能存着的 OpenRouter 变体。
	for _, id := range []string{
		"openai/gpt-image-2.5-flare", "openai/gpt-image-2", "openai/gpt-5-image", "openai/gpt-5-image-mini",
		"gpt-image-2.5-sunburst-all", "gpt-image-2.5-flare-all",
	} {
		if !seen[id] {
			out = append(out, id)
		}
	}
	return out
}

func modelsForAPIBase(apiBase string) []ImageModel {
	if p, ok := imageProviderByAPIBase(apiBase); ok {
		return p.Models
	}
	return latestImageModels()
}

func modelAllowedForAPIBase(apiBase, model string) bool {
	for _, m := range modelsForAPIBase(apiBase) {
		if m.ID == model {
			return true
		}
	}
	// 兼容列表中的历史 ID：仅当当前供应商是 OpenRouter 时放行。
	if p, ok := imageProviderByAPIBase(apiBase); ok && p.ID == "openrouter" {
		return contains([]string{
			"openai/gpt-image-2.5-flare", "openai/gpt-image-2", "openai/gpt-5-image", "openai/gpt-5-image-mini",
		}, model)
	}
	return false
}

func modelFamily(id string) string {
	if m, ok := imageModelByID(id); ok {
		return m.Family
	}
	switch {
	case stringsHasPrefix(id, "openai/gpt") || stringsHasPrefix(id, "gpt-image"):
		return "openai"
	case stringsHasPrefix(id, "bytedance-seed/") || stringsHasPrefix(id, "doubao-seedream"):
		return "seedream"
	case stringsHasPrefix(id, "google/gemini") || stringsHasPrefix(id, "nano-banana"):
		return "banana"
	case stringsHasPrefix(id, "qwen/") || stringsHasPrefix(id, "qwen-image"):
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

// buildGeekAIPayload 使用 GeekAI 异步 generations 接口字段（size / image|images / async）。
func buildGeekAIPayload(model, prompt, imageSize, quality string, images []string) map[string]any {
	if imageSize == "" {
		imageSize = "1024x1024"
	}
	payload := map[string]any{
		"model":           model,
		"prompt":          prompt,
		"size":            imageSize,
		"quality":         quality,
		"n":               1,
		"output_format":   "png",
		"response_format": "b64_json",
		"background":      "transparent",
		"async":           true,
		"retries":         0,
	}
	if len(images) == 1 {
		payload["image"] = images[0]
	} else if len(images) > 1 {
		payload["images"] = images
	}
	return payload
}

// normalizeProviderSettings 规范化供应商地址与对应模型，不再强制把 GeekAI 迁走。
func normalizeProviderSettings(s Settings) Settings {
	s.APIBase = strings.TrimRight(strings.TrimSpace(s.APIBase), "/")
	if s.APIBase == "" {
		s.APIBase = openRouterAPIBase
	}
	legacyToOpenRouter := map[string]string{
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
	openRouterToGeekAI := map[string]string{}
	for short, full := range legacyToOpenRouter {
		if _, exists := openRouterToGeekAI[full]; !exists {
			openRouterToGeekAI[full] = short
		}
	}
	if p, ok := imageProviderByAPIBase(s.APIBase); ok {
		switch p.ID {
		case "openrouter":
			if mapped, hit := legacyToOpenRouter[s.Model]; hit {
				s.Model = mapped
			}
			if !modelAllowedForAPIBase(s.APIBase, s.Model) {
				s.Model = "openai/gpt-image-2.5-sunburst"
			}
			if len(s.AssetHosts) == 0 {
				s.AssetHosts = append([]string{}, p.AssetHosts...)
			}
		case "geekai":
			if mapped, hit := openRouterToGeekAI[s.Model]; hit {
				s.Model = mapped
			}
			if !modelAllowedForAPIBase(s.APIBase, s.Model) {
				s.Model = "gpt-image-2.5-sunburst"
			}
			if len(s.AssetHosts) == 0 {
				s.AssetHosts = append([]string{}, p.AssetHosts...)
			}
		}
	}
	return s
}

// migrateToOpenRouter 保留旧名供调用点兼容；实际改为规范化，允许继续使用 GeekAI。
func migrateToOpenRouter(s Settings) Settings {
	return normalizeProviderSettings(s)
}
