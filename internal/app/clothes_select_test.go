package app

import (
	"regexp"
	"strings"
	"testing"
)

// 移动端服装下拉框：全局 select 的固定 41px 高度装不下 .clothes-select 的 12px 内边距 + 14px 字号，
// 真机渲染时文字会被上下裁掉（黑色礼服的底部笔画缺失）。这里锁定「自己撑开高度 + 最小点击区域」的约定，
// 并顺带提醒：通用 select 的高度约定一旦变化，需要重新核对服装下拉框。
func TestClothesSelectNotClippedByFixedHeight(t *testing.T) {
	a := testApp(t)
	body := request(t, a, nil, "GET", "/", nil).Body.String()
	match := regexp.MustCompile(`/assets/(style\.v\d+\.css)`).FindStringSubmatch(body)
	if match == nil {
		t.Fatal("homepage missing stylesheet reference")
	}
	raw, err := web.ReadFile("web/" + match[1])
	if err != nil {
		t.Fatal("homepage stylesheet not embedded", err)
	}
	css := string(raw)
	if !strings.Contains(css, "select {\n  height: 41px;\n}") {
		t.Fatal("通用 select 的 41px 高度约定已变化，请重新核对服装下拉框是否仍会被裁切")
	}
	rule := cssRuleBlock(t, css, ".clothes-select")
	for _, want := range []string{"height: auto", "min-height: 44px", "font-size: 14px"} {
		if !strings.Contains(rule, want) {
			t.Fatal("服装下拉框必须自己决定高度，避免固定高度裁掉文字", want, rule)
		}
	}
}
