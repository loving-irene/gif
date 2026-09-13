package app

import (
	"strings"
	"testing"
)

// cssRuleBlock 从一段 CSS 文本里取出指定选择器的规则体，便于逐条断言。
func cssRuleBlock(t *testing.T, block, selector string) string {
	t.Helper()
	at := strings.Index(block, selector+" {")
	if at < 0 {
		t.Fatal("缺少样式规则", selector)
	}
	rule := block[at:]
	if end := strings.Index(rule, "}"); end >= 0 {
		rule = rule[:end]
	}
	return rule
}

// 窄屏页头改双行：≤800px（手机与竖屏平板）第一行只放品牌标识，第二行从「社区」起排列全部入口。
// 此前品牌与入口挤在同一条 flex 行里：手机（≤600px）只能隐藏「我的作品」「增加次数」来腾地方，
// 601–800px 则把入口文字挤成两行（「分享」「反馈」「我的作品」「增加次数」「次创作」）；
// 改双行后品牌独占一行，入口在第二行整行内两端分布。
func TestNarrowHeaderStacksLogoAboveEntries(t *testing.T) {
	a := testApp(t)
	body := request(t, a, nil, "GET", "/", nil).Body.String()
	// /assets/ 是 immutable 长期缓存，改样式必须换文件名，页面引用要同步更新。
	if !strings.Contains(body, "/assets/style.v29.css") {
		t.Fatal("homepage should reference the stacked-header stylesheet")
	}
	// 双行的前提：品牌是页头的直接子元素、入口都装在 nav 里——品牌若在 nav 内，
	// 只让品牌独占一行就只能靠 CSS 拆行，做不到「从社区起另起一行」。
	start := strings.Index(body, `class="header wrap"`)
	if start < 0 {
		t.Fatal("homepage header missing")
	}
	header := body[start:]
	brand := strings.Index(header, `class="brand"`)
	nav := strings.Index(header, "<nav>")
	if brand < 0 || nav < 0 || brand > nav {
		t.Fatal("页头结构应为：品牌在前、入口 nav 在后，且品牌不在 nav 里")
	}

	raw, err := web.ReadFile("web/style.v29.css")
	if err != nil {
		t.Fatal("homepage stylesheet not embedded", err)
	}
	css := string(raw)
	at := strings.Index(css, "/* 窄屏页头改双行")
	if at < 0 {
		t.Fatal("缺少窄屏页头双行规则")
	}
	mobile := css[at:]
	if end := strings.Index(mobile, "\n}\n"); end >= 0 {
		mobile = mobile[:end+2]
	}
	// 断点是 800px：手机与竖屏平板都走双行，601–800px 不再把入口文字挤成两行。
	if !strings.Contains(mobile, "@media (max-width: 800px)") {
		t.Fatal("窄屏页头断点应为 800px", mobile)
	}

	head := cssRuleBlock(t, mobile, ".header")
	// 纵向排列 + 高度自适应：原来的 104px/83px 单行高度会把两行内容压扁。
	for _, want := range []string{"flex-direction: column", "height: auto"} {
		if !strings.Contains(head, want) {
			t.Fatal("页头没有改成品牌与入口上下两行", want, head)
		}
	}
	// 只调纵向内边距：改写 padding 简写会连带清掉 .wrap 的左右内边距（≤800px 为 20px、
	// 手机为 17px），页头就会与下方正文左右错位。
	if strings.Contains(mobile, "padding:") {
		t.Fatal("窄屏页头只应调 padding-top/padding-bottom，不能覆盖 .wrap 的左右内边距", mobile)
	}

	navRule := cssRuleBlock(t, mobile, ".header nav")
	if !strings.Contains(navRule, "width: 100%") || !strings.Contains(navRule, "justify-content: space-between") {
		t.Fatal("入口行应占满整行并在行内两端分布", navRule)
	}
	if strings.Contains(navRule, "display: none") {
		t.Fatal("入口行不能整行隐藏", navRule)
	}
}
