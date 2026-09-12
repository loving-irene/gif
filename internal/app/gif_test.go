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
}
