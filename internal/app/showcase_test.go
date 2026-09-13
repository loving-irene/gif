package app

import (
	"bytes"
	"strings"
	"testing"
)

// 宽屏下示例卡片必须与同栏的标题正文左对齐：被居中时卡片会比左侧文字右移约 222px，
// 看起来像是浮在页面中间（曾经的 justify-content: center 就是这么写错的）。
func TestHomepageShowcaseLeftAligned(t *testing.T) {
	raw, err := web.ReadFile("web/style.v27.css")
	if err != nil {
		t.Fatal("homepage stylesheet not embedded", err)
	}
	css := string(raw)
	at := strings.Index(css, "@media (min-width: 801px)")
	if at < 0 {
		t.Fatal("宽屏断点缺失")
	}
	block := css[at:]
	if end := strings.Index(block, "\n}"); end >= 0 {
		block = block[:end]
	}
	card := strings.Index(block, ".showcase-list")
	if card < 0 {
		t.Fatal("宽屏断点里缺少 .showcase-list 规则")
	}
	rule := block[card:]
	if end := strings.Index(rule, "}"); end >= 0 {
		rule = rule[:end]
	}
	if !strings.Contains(rule, "repeat(2, minmax(0, 348px))") {
		t.Fatal("示例卡片应锁 256×256 画幅并排两列", rule)
	}
	if strings.Contains(rule, "justify-content") || strings.Contains(rule, "margin: auto") {
		t.Fatal("示例卡片被居中，与左侧标题正文不对齐", rule)
	}
}

// 示例动图是 position:absolute + inset:0，必须挂在卡片里唯一定位的 .showcase-media 上：
// 挂到未定位的 .showcase-card 上时，inset 会相对初始包含块解析，动画会被画到页面左上角
// （盖在页头与 hero 上），而不是盖在静态首帧上。
func TestHomepageShowcaseOverlayAnchored(t *testing.T) {
	loader, err := web.ReadFile("web/showcase.v2.js")
	if err != nil {
		t.Fatal("showcase loader not embedded", err)
	}
	js := string(loader)
	if !strings.Contains(js, "media.append(image)") {
		t.Fatal("动画层必须挂在 .showcase-media 上", js)
	}
	if strings.Contains(js, "card.append(image)") {
		t.Fatal("动画层挂在未定位的卡片上，会画到页面左上角")
	}
	if !strings.Contains(js, `card.querySelector(".showcase-media")`) {
		t.Fatal("加载器没有取 .showcase-media")
	}

	// 这个挂载点成立的前提：.showcase-media 自身是定位元素（inset:0 的包含块），
	// 且它锁了 1:1，所以动画尺寸与首帧完全重合。
	raw, err := web.ReadFile("web/style.v27.css")
	if err != nil {
		t.Fatal("homepage stylesheet not embedded", err)
	}
	css := string(raw)
	at := strings.Index(css, ".showcase-media {")
	if at < 0 {
		t.Fatal("缺少 .showcase-media 规则")
	}
	rule := css[at:]
	if end := strings.Index(rule, "}"); end >= 0 {
		rule = rule[:end]
	}
	for _, want := range []string{"position: relative", "aspect-ratio: 1"} {
		if !strings.Contains(rule, want) {
			t.Fatal("动画层的挂载点不再满足定位与画幅前提", want, rule)
		}
	}
}

// 首页示例作品直接用内嵌 web 资源；文件名写错或资源丢失时页面只会表现为空白，
// 所以这里把服务端能验证的部分全部钉住：资源真实存在、确实是 16 帧动画 WebP、
// 页面确实引用了它们，且首帧海报必须先于动画出现。
func TestHomepageShowcaseExamples(t *testing.T) {
	a := testApp(t)
	w := request(t, a, nil, "GET", "/", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	body := w.Body.String()
	// 页面引用的是当前版本的样式表与加载器；升版本号时必须同步改页面引用。
	for _, asset := range []string{"/assets/style.v27.css", "/assets/showcase.v2.js"} {
		if !strings.Contains(body, asset) {
			t.Fatal("homepage missing asset reference", asset)
		}
		if _, err := web.ReadFile("web/" + strings.TrimPrefix(asset, "/assets/")); err != nil {
			t.Fatal("referenced asset not embedded", asset, err)
		}
	}
	if !strings.Contains(body, `id="showcaseTitle"`) || strings.Count(body, `class="showcase-card"`) != 2 {
		t.Fatal("showcase section markup missing")
	}
	for _, name := range []string{"example-heart", "example-success"} {
		// 静态首帧直接写在页面里：没有脚本时也能看到作品。
		if !strings.Contains(body, `src="/assets/`+name+`-poster.webp"`) {
			t.Fatal("poster not rendered into homepage", name)
		}
		// 动画地址挂在 data 属性上，由 showcase.v2.js 在进入视口后挂载。
		if !strings.Contains(body, `data-anim-src="/assets/`+name+`.webp"`) {
			t.Fatal("animation source not referenced in homepage", name)
		}
		for _, asset := range []string{name + "-poster.webp", name + ".webp"} {
			raw, err := web.ReadFile("web/" + asset)
			if err != nil {
				t.Fatal("showcase asset not embedded", asset, err)
			}
			if len(raw) < 16 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WEBP" {
				t.Fatal("showcase asset is not a WebP", asset)
			}
			animated := bytes.Contains(raw, []byte("ANIM")) && bytes.Contains(raw, []byte("ANMF"))
			if strings.HasSuffix(asset, "-poster.webp") {
				// 海报是一张静态首帧，体积只有动图的十分之一，先显示它。
				if animated {
					t.Fatal("poster should be a still image", asset)
				}
				continue
			}
			if !animated || bytes.Count(raw, []byte("ANMF")) != 16 {
				t.Fatal("example should be a 16 frame animation", asset)
			}
			// 动图要明显小于原始 GIF，否则不如直接放 GIF，白折腾。
			if len(raw) > 150*1024 {
				t.Fatal("animation too heavy for the homepage", asset, len(raw))
			}
		}
	}
	// 页面 CSP 只允许同源脚本，内联脚本会被浏览器拦下，所以必须外链。
	if strings.Contains(body, "onload=") || strings.Contains(body, "<script>") {
		t.Fatal("homepage must not rely on inline scripts")
	}
	// 动图不做 preload：手机上应在卡片进入视口时再下载。
	if strings.Contains(body, `rel="preload"`) {
		t.Fatal("homepage should not preload images")
	}
}

// /assets/ 是长期缓存目录，示例资源必须按原样提供且带上正确的图片类型。
func TestShowcaseAssetsServed(t *testing.T) {
	a := testApp(t)
	for _, name := range []string{
		"example-heart.webp", "example-heart-poster.webp",
		"example-success.webp", "example-success-poster.webp",
	} {
		w := request(t, a, nil, "GET", "/assets/"+name, nil)
		if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "RIFF") {
			t.Fatal("asset not served", name, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "image/webp") {
			t.Fatal("unexpected content type", name, ct)
		}
	}
}
