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
const siteDescription = "拾光 GIF 将自拍制作成专属角色与动态表情包，上传自拍后可选择默认、Q版或水墨风格，支持古风铠甲、可爱日常和童趣造型。先确认人物定稿，再选择多个动作，生成循环 GIF 并下载保存。"

type faqItem struct {
	Question string
	Answer   string
}

var siteFAQs = []faqItem{
	{"如何用自拍生成 GIF 动图？", "上传一张清晰自拍，选择画风（默认、Q版或水墨风格）、人物分类、服装和配色，生成并确认角色定稿，再选择动作。每个动作生成连续画格，由服务器在后台合成可循环播放的 GIF，完成后可以下载。"},
	{"支持哪些人物造型和表情动作？", "画风提供默认（轻度Q版）、Q版与水墨风格三种。人物提供男生古代铠甲武将、女生可爱日常、小朋友童趣三类造型，可选择服装与配色，武将可选择武器；动作以创作页面当前提供的选项为准。"},
	{"照片会保留本人的特点吗？", "角色提示词优先保留脸型、五官关系、发型、肤色与清晰可见的个人特点。AI 生成可能出现差异，请先核对定稿的人物辨识度，再确认并制作动作。"},
	{"上传照片有什么要求？", "支持 JPG、PNG 和 WebP。不超过 5 MB 的照片按原文件上传，不压缩、不缩放或修改格式；仅超过 5 MB 时由浏览器压缩到限制内。建议使用光线均匀、面部清晰、无遮挡且不过度美颜的自拍。"},
	{"生成 GIF 如何计算创作次数？", "生成一次角色定稿和生成每个动作分别计算一次图像生成请求。查询进度、确认定稿、本地合成 GIF 和下载不扣次数；失败是否计次以创作页面显示的规则为准。"},
	{"GIF 保存在哪里？换设备能看到吗？", "生成任务在服务器后台执行，定稿和 GIF 会在服务器暂存最多 3 天，期间重新打开页面可继续查看或找回，到期自动删除；自拍和作品同时保存在当前设备的浏览器。邮箱登录同步账号与次数，不跨设备同步图片，请及时下载。创作时照片会发送给 GeekAI 图像服务处理，其数据处理以第三方服务约定为准。"},
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
