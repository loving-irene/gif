package app

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBrowserGIFEncoderDecodes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node required for browser encoder test")
	}
	output := filepath.Join(t.TempDir(), "fixture.gif")
	cmd := exec.Command(node, "../../scripts/test-gif.mjs", output)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("encoder: %v %s", e, b)
	}
	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g, err := gif.DecodeAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Image) != gifOutputFrames || g.LoopCount != 0 || g.Config.Width != 256 || g.Config.Height != 256 {
		t.Fatal("GIF animation metadata invalid")
	}
	for i, frame := range g.Image {
		if g.Disposal[i] != gif.DisposalBackground {
			t.Fatal("transparent frame disposal invalid")
		}
		if g.Delay[i] != gifFrameDelay {
			t.Fatal("frame timing invalid")
		}
		// 第 i 帧对应源帧 i/2：检查该源帧色块中心始终不透明（补插帧取轮廓并集）。
		_, _, _, alpha := frame.At(30+(i/2)*8, 60).RGBA()
		if alpha == 0 {
			t.Fatal("moving subject was lost")
		}
		_, _, _, alpha = frame.At(0, 0).RGBA()
		if alpha != 0 {
			t.Fatal("transparent background lost")
		}
	}
}

func TestGIFInterpolationKeepsUnionAlpha(t *testing.T) {
	// 1024×1024 序列图：第 1 格纯红、第 2 格纯蓝，其余全透明。
	sheet := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	draw.Draw(sheet, image.Rect(0, 0, 256, 256), &image.Uniform{color.NRGBA{220, 40, 40, 255}}, image.Point{}, draw.Src)
	draw.Draw(sheet, image.Rect(256, 0, 512, 256), &image.Uniform{color.NRGBA{40, 40, 220, 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, sheet); err != nil {
		t.Fatal(err)
	}
	data, err := synthesizeGIF(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Image) != gifOutputFrames {
		t.Fatalf("expected %d frames, got %d", gifOutputFrames, len(g.Image))
	}
	r, gg, b, alpha := g.Image[0].At(10, 10).RGBA()
	if alpha == 0 || r <= b {
		t.Fatal("source frame 0 should be opaque red", r, gg, b, alpha)
	}
	r, gg, b, alpha = g.Image[2].At(10, 10).RGBA()
	if alpha == 0 || b <= r {
		t.Fatal("source frame 2 should be opaque blue", r, gg, b, alpha)
	}
	// 第 1 帧是红蓝两格的补插帧：轮廓并集必须整体不透明，不出现半透明闪烁。
	for y := 0; y < 256; y += 37 {
		for x := 0; x < 256; x += 41 {
			if _, _, _, alpha := g.Image[1].At(x, y).RGBA(); alpha == 0 {
				t.Fatalf("interpolated frame lost opaque coverage at %d,%d", x, y)
			}
		}
	}
}
