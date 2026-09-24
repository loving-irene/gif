package app

import (
	"bytes"
	"strings"
	"testing"
)

// 流程展示必须始终单行：手机也不能换行，否则「自拍照 → 定稿 → GIF」会折成两行。
func TestHomepageShowcaseFlowSingleRow(t *testing.T) {
	raw, err := web.ReadFile("web/style.v36.css")
	if err != nil {
		t.Fatal("homepage stylesheet not embedded", err)
	}
	css := string(raw)
	at := strings.Index(css, ".showcase-flow {")
	if at < 0 {
		t.Fatal("缺少 .showcase-flow 规则")
	}
	rule := css[at:]
	if end := strings.Index(rule, "}"); end >= 0 {
		rule = rule[:end]
	}
	for _, want := range []string{"display: flex", "flex-wrap: nowrap"} {
		if !strings.Contains(rule, want) {
			t.Fatal("流程条必须 flex 且禁止换行", want, rule)
		}
	}
	if strings.Contains(rule, "overflow-x: auto") || strings.Contains(rule, "grid-auto-flow: column") {
		t.Fatal("流程条不应再做成横向滑动卡片", rule)
	}
	if !strings.Contains(rule, "overflow: hidden") {
		t.Fatal("流程条应锁在一行内完整展示，避免横滑", rule)
	}
	if !strings.Contains(css, ".showcase-stage-draft .showcase-media") {
		t.Fatal("transparent draft needs a paper-colored media backdrop")
	}
}

// 首页固定展示「自拍照 → 定稿图 → GIF动画」；定稿为透明底 WebP，GIF 用原始文件。
func TestHomepageShowcaseExamples(t *testing.T) {
	a := testApp(t)
	w := request(t, a, nil, "GET", "/", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, `class="hero"`) {
		t.Fatal("homepage should not render the removed hero section")
	}
	if !strings.Contains(body, "/assets/style.v36.css") {
		t.Fatal("homepage missing stylesheet")
	}
	if !strings.Contains(body, `id="showcaseTitle"`) || !strings.Contains(body, `class="showcase-flow"`) {
		t.Fatal("showcase section markup missing")
	}
	if strings.Count(body, `class="showcase-stage`) != 3 {
		t.Fatal("showcase should have three stages", strings.Count(body, `class="showcase-stage`))
	}
	if !strings.Contains(body, "showcase-stage-draft") {
		t.Fatal("draft stage should mark transparent backdrop class")
	}
	for _, label := range []string{">自拍照</b>", ">定稿图</b>", ">GIF动画</b>"} {
		if !strings.Contains(body, label) {
			t.Fatal("missing flow label", label)
		}
	}
	if !strings.Contains(body, `src="/assets/example-flow-selfie.v1.webp"`) {
		t.Fatal("selfie asset missing")
	}
	if !strings.Contains(body, `src="/assets/example-flow-draft.v2.webp"`) {
		t.Fatal("draft must use transparent webp")
	}
	if !strings.Contains(body, `src="/assets/example-flow-gif.v1.gif"`) {
		t.Fatal("gif must use original GIF file")
	}
	if strings.Contains(body, "example-flow-draft.v1.jpg") ||
		strings.Contains(body, "example-flow-gif.v1.webp") ||
		strings.Contains(body, "example-flow-gif-poster") ||
		strings.Contains(body, `data-anim-src=`) {
		t.Fatal("old draft jpeg or gif overlay pipeline must not be used")
	}

	draft, err := web.ReadFile("web/example-flow-draft.v2.webp")
	if err != nil {
		t.Fatal(err)
	}
	if len(draft) < 16 || string(draft[:4]) != "RIFF" || string(draft[8:12]) != "WEBP" {
		t.Fatal("draft is not a WebP")
	}
	if !bytes.Contains(draft, []byte("ALPH")) && !bytes.Contains(draft, []byte("VP8L")) {
		t.Fatal("draft webp should carry transparency")
	}
	gif, err := web.ReadFile("web/example-flow-gif.v1.gif")
	if err != nil {
		t.Fatal(err)
	}
	if len(gif) < 6 || string(gif[:6]) != "GIF89a" {
		t.Fatal("gif is not raw GIF89a")
	}

	if strings.Contains(body, "onload=") || strings.Contains(body, "<script>") {
		t.Fatal("homepage must not rely on inline scripts")
	}
	if strings.Contains(body, "<picture") {
		t.Fatal("示例挂载点不能用 <picture>")
	}
	if strings.Contains(body, `rel="preload"`) {
		t.Fatal("homepage should not preload images")
	}
}

func TestShowcaseAssetsServed(t *testing.T) {
	a := testApp(t)
	cases := []struct {
		name string
		sig  string
		ct   string
	}{
		{"example-flow-selfie.v1.webp", "RIFF", "image/webp"},
		{"example-flow-draft.v2.webp", "RIFF", "image/webp"},
		{"example-flow-gif.v1.gif", "GIF89a", "image/gif"},
	}
	for _, c := range cases {
		w := request(t, a, nil, "GET", "/assets/"+c.name, nil)
		if w.Code != 200 {
			t.Fatal("asset not served", c.name, w.Code)
		}
		body := w.Body.String()
		if !strings.HasPrefix(body, c.sig) {
			t.Fatal("unexpected file signature", c.name)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, c.ct) {
			t.Fatal("unexpected content type", c.name, ct)
		}
	}
}
