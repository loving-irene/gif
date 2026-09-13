package app

import (
	"bytes"
	"strings"
	"testing"
)

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
	for _, asset := range []string{"/assets/style.v23.css", "/assets/showcase.v1.js"} {
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
		// 动画地址挂在 data 属性上，由 showcase.v1.js 在进入视口后挂载。
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
