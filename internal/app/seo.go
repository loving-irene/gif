package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strings"
)

const siteTitle = "自拍生成GIF动图与专属表情包｜拾光 GIF"
const siteDescription = "拾光 GIF 将自拍做成专属角色与动态表情包：上传自拍，按男生、女生、小朋友或日常挑选动作，系统自动生成定稿并合成循环 GIF；已有定稿时可换动作继续生成，完成后下载保存。"

type faqItem struct {
	Question string
	Answer   string
}

var siteFAQs = []faqItem{
	{"如何用自拍生成 GIF 动图？", "上传自拍，选好分类与动作。首次自动生成角色定稿并合成循环 GIF；已有定稿时换动作即可继续生成。"},
	{"支持哪些分类和动作？", "男生、女生、小朋友、日常四个分类，可一次多选、并行制作。"},
	{"会保留本人特点吗？", "会优先保留脸型、五官、发型与肤色等特征；不满意可换自拍重新生成。"},
	{"上传照片有什么要求？", "支持 JPG、PNG、WebP；5 MB 以内按原文件上传，超过则由浏览器压缩。建议正脸清晰、光线均匀、不过度美颜。"},
	{"创作次数怎么算？", "生成定稿 1 次，每个动作各 1 次；已有定稿只换动作时只算动作。查询进度、合成与下载不扣次。"},
	{"作品保存在哪里？", "同一账号可跨设备同步。云端定稿与作品各保留最近 30 份（永久、按数量淘汰）；社区分享独立保存，可取消。任务输入在结束后删除，残留最多 3 天。本机数据清理浏览器后可能丢失，请及时下载。"},
}

var homeTemplate = template.Must(template.ParseFS(web, "web/index.html"))

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	canonical := a.env.BaseURL + "/"
	questions := make([]map[string]any, 0, len(siteFAQs))
	for _, f := range siteFAQs {
		questions = append(questions, map[string]any{"@type": "Question", "name": f.Question, "acceptedAnswer": map[string]string{"@type": "Answer", "text": f.Answer}})
	}
	structured, err := json.Marshal(map[string]any{"@context": "https://schema.org", "@graph": []any{
		map[string]any{"@type": "WebSite", "@id": canonical + "#website", "name": "拾光 GIF", "url": canonical, "inLanguage": "zh-CN", "description": siteDescription},
		map[string]any{"@type": "WebApplication", "@id": canonical + "#app", "name": "拾光 GIF", "url": canonical, "applicationCategory": "MultimediaApplication", "operatingSystem": "Web browser", "description": siteDescription, "inLanguage": "zh-CN"},
		map[string]any{"@type": "FAQPage", "@id": canonical + "#faq", "mainEntity": questions},
	}})
	if err != nil {
		fail(w, 500, "页面暂时不可用")
		return
	}
	nonce := token(16)
	var body bytes.Buffer
	err = homeTemplate.Execute(&body, struct {
		Title, Description, Canonical, Nonce string
		StructuredData                       template.JS
		FAQ                                  []faqItem
	}{siteTitle, siteDescription, canonical, nonce, template.JS(structured), siteFAQs})
	if err != nil {
		fail(w, 500, "页面暂时不可用")
		return
	}
	policy := w.Header().Get("Content-Security-Policy")
	w.Header().Set("Content-Security-Policy", strings.Replace(policy, "script-src 'self';", "script-src 'self' 'nonce-"+nonce+"';", 1))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(body.Bytes())
}

func (a *App) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\nDisallow: /api/\n\nSitemap: %s/sitemap.xml\n", a.env.BaseURL)
}

func (a *App) sitemap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>%s/</loc></url></urlset>`, html.EscapeString(a.env.BaseURL))
}

func (a *App) llms(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# 拾光 GIF\n\n> %s\n\n## 公开页面\n- [自拍生成GIF动图与常见问题](%s/)\n\n## 功能与使用边界\n", siteDescription, a.env.BaseURL)
	for _, f := range siteFAQs {
		fmt.Fprintf(w, "\n### %s\n%s\n", f.Question, f.Answer)
	}
}
