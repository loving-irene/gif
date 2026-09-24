package app

import (
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
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
	dir := t.TempDir()
	output := filepath.Join(dir, "fixture.gif")
	output25 := filepath.Join(dir, "fixture25.gif")
	output100 := filepath.Join(dir, "fixture100.gif")
	cmd := exec.Command(node, "../../scripts/test-gif.mjs", output, output25, output100)
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
	if len(g.Image) != 16 || g.LoopCount != 0 || g.Config.Width != 256 || g.Config.Height != 256 {
		t.Fatal("GIF animation metadata invalid")
	}
	for i, frame := range g.Image {
		if g.Disposal[i] != gif.DisposalBackground {
			t.Fatal("transparent frame disposal invalid")
		}
		if g.Delay[i] != gifFrameDelay {
			t.Fatal("frame timing invalid")
		}
		if !hasOpaquePixel(frame) {
			t.Fatal("moving subject was lost")
		}
		if _, _, _, alpha := frame.At(0, 0).RGBA(); alpha != 0 {
			t.Fatal("transparent background lost")
		}
	}
	// v7 编码器的 5×5 规格：25 帧每帧 128×128、70ms 帧间隔。
	f25, err := os.Open(output25)
	if err != nil {
		t.Fatal(err)
	}
	defer f25.Close()
	g25, err := gif.DecodeAll(f25)
	if err != nil {
		t.Fatal(err)
	}
	if len(g25.Image) != 25 || g25.LoopCount != 0 || g25.Config.Width != 128 || g25.Config.Height != 128 {
		t.Fatal("5x5 GIF animation metadata invalid")
	}
	for i, frame := range g25.Image {
		if g25.Disposal[i] != gif.DisposalBackground {
			t.Fatal("5x5 transparent frame disposal invalid")
		}
		if g25.Delay[i] != smoothishGifFrameDelay {
			t.Fatal("5x5 frame timing invalid")
		}
		if !hasOpaquePixel(frame) {
			t.Fatal("5x5 moving subject was lost")
		}
		if _, _, _, alpha := frame.At(0, 0).RGBA(); alpha != 0 {
			t.Fatal("5x5 transparent background lost")
		}
	}
	// v6 编码器的 10×10 规格：100 帧每帧 256×256、20ms 帧间隔。
	f100, err := os.Open(output100)
	if err != nil {
		t.Fatal(err)
	}
	defer f100.Close()
	g100, err := gif.DecodeAll(f100)
	if err != nil {
		t.Fatal(err)
	}
	if len(g100.Image) != 100 || g100.LoopCount != 0 || g100.Config.Width != 256 || g100.Config.Height != 256 {
		t.Fatal("10x10 GIF animation metadata invalid")
	}
	for i, frame := range g100.Image {
		if g100.Disposal[i] != gif.DisposalBackground || g100.Delay[i] != smoothGifFrameDelay {
			t.Fatal("10x10 frame metadata invalid")
		}
		if !hasOpaquePixel(frame) {
			t.Fatal("10x10 moving subject was lost")
		}
		if _, _, _, alpha := frame.At(0, 0).RGBA(); alpha != 0 {
			t.Fatal("10x10 transparent background lost")
		}
	}
}

func TestStabilizeFramesCorrectsSingleFrameJitter(t *testing.T) {
	frames := make([]*image.NRGBA, 5)
	for i := range frames {
		frames[i] = image.NewNRGBA(image.Rect(0, 0, 128, 128))
		x0, y0, width, height := 40+i, 50+i, 30, 50
		if i == 2 {
			x0, y0, width, height = 50, 62, 34, 48
		}
		draw.Draw(frames[i], image.Rect(x0, y0, x0+width, y0+height), &image.Uniform{C: color.NRGBA{R: 180, G: 90, B: 60, A: 255}}, image.Point{}, draw.Src)
	}
	before := measureSubject(frames[2])
	after := measureSubject(stabilizeFrames(frames, 128)[2])
	const expectedCenter = 58
	const expectedFoot = 103
	const expectedArea = 1500
	if math.Abs(after.centerX-expectedCenter) >= math.Abs(before.centerX-expectedCenter) {
		t.Fatal("horizontal center jitter was not reduced", before.centerX, after.centerX)
	}
	if math.Abs(after.foot-expectedFoot) >= math.Abs(before.foot-expectedFoot) {
		t.Fatal("foot-line jitter was not reduced", before.foot, after.foot)
	}
	if math.Abs(after.area-expectedArea) >= math.Abs(before.area-expectedArea) {
		t.Fatal("subject size jitter was not reduced", before.area, after.area)
	}
}

func TestStabilizeFramesPreservesContinuousJump(t *testing.T) {
	feet := []int{100, 94, 88, 82, 78, 82, 88, 94, 100}
	frames := make([]*image.NRGBA, len(feet))
	for i, foot := range feet {
		frames[i] = image.NewNRGBA(image.Rect(0, 0, 128, 128))
		draw.Draw(frames[i], image.Rect(49, foot-48, 79, foot), &image.Uniform{C: color.NRGBA{R: 180, G: 90, B: 60, A: 255}}, image.Point{}, draw.Src)
	}
	stable := stabilizeFrames(frames, 128)
	ground := measureSubject(stable[0]).foot
	apex := measureSubject(stable[4]).foot
	if ground-apex < 12 {
		t.Fatal("continuous jump was flattened", ground, apex)
	}
}

func hasOpaquePixel(frame image.Image) bool {
	for y := frame.Bounds().Min.Y; y < frame.Bounds().Max.Y; y++ {
		for x := frame.Bounds().Min.X; x < frame.Bounds().Max.X; x++ {
			if _, _, _, alpha := frame.At(x, y).RGBA(); alpha != 0 {
				return true
			}
		}
	}
	return false
}
