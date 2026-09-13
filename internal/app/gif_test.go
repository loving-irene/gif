package app

import (
	"image/gif"
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
	cmd := exec.Command(node, "../../scripts/test-gif.mjs", output, output25)
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
		delay := 10
		if i >= 4 && i <= 7 {
			delay = 6
		}
		if g.Delay[i] != delay {
			t.Fatal("frame timing invalid")
		}
		_, _, _, alpha := frame.At(11+i*8, 40).RGBA()
		if alpha == 0 {
			t.Fatal("moving subject was lost")
		}
		_, _, _, alpha = frame.At(0, 0).RGBA()
		if alpha != 0 {
			t.Fatal("transparent background lost")
		}
	}
	// v3 编码器的 5×5 规格：25 帧每帧 128×128，第 7—12 帧稍快。
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
		delay := 10
		if i >= 6 && i <= 11 {
			delay = 6
		}
		if g25.Delay[i] != delay {
			t.Fatal("5x5 frame timing invalid")
		}
		_, _, _, alpha := frame.At(10+i*4, 20).RGBA()
		if alpha == 0 {
			t.Fatal("5x5 moving subject was lost")
		}
		_, _, _, alpha = frame.At(0, 0).RGBA()
		if alpha != 0 {
			t.Fatal("5x5 transparent background lost")
		}
	}
}
