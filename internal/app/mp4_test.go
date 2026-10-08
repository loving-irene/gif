package app

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testAnimatedGIF(t *testing.T) []byte {
	t.Helper()
	out := &gif.GIF{LoopCount: 0}
	for i := 0; i < 3; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, 64, 64), color.Palette{
			color.RGBA{0, 0, 0, 255},
			color.RGBA{uint8(40 + i*70), 120, 200, 255},
		})
		for y := 10; y < 54; y++ {
			for x := 10 + i*4; x < 30+i*4; x++ {
				frame.SetColorIndex(x, y, 1)
			}
		}
		out.Image = append(out.Image, frame)
		out.Delay = append(out.Delay, 10)
		out.Disposal = append(out.Disposal, gif.DisposalBackground)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, out); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGifToMP4ConvertsWithFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mp4, err := gifToMP4(ctx, testAnimatedGIF(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(mp4) < 32 || string(mp4[4:8]) != "ftyp" {
		t.Fatalf("unexpected mp4 header: len=%d prefix=%q", len(mp4), mp4[:min(16, len(mp4))])
	}
	// 测试 GIF：3 帧 × 0.1s = 0.3s/循环；转存应完整播放 3 次 ≈ 0.9s。
	path := filepath.Join(t.TempDir(), "out.mp4")
	if err := os.WriteFile(path, mp4, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var duration float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &duration); err != nil {
		t.Fatalf("parse duration %q: %v", out, err)
	}
	want := 0.3 * float64(mp4LoopCount)
	if duration < want-0.15 || duration > want+0.25 {
		t.Fatalf("mp4 duration=%.3fs want about %.1fs (%d loops)", duration, want, mp4LoopCount)
	}
}

func TestAdminWorkMP4DownloadsConvertedVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	a := testApp(t)
	user := loginDevice(t, a, "mp4-user")
	if w := request(t, a, user, "GET", "/api/admin/gallery/works/mp4-work/mp4", nil); w.Code != 403 {
		t.Fatalf("non-admin mp4 status=%d", w.Code)
	}
	admin := codeAdmin(t, a, user)
	gifBytes := testAnimatedGIF(t)
	if _, err := a.db.Exec(
		"INSERT INTO works(id,user_id,created,name,category,action,gif,sheet) VALUES(?,?,?,?,?,?,?,?)",
		"mp4-work", user.User.ID, 200, "挥手打招呼", "female", "wave", gifBytes, []byte("sheet"),
	); err != nil {
		t.Fatal(err)
	}
	w := request(t, a, admin, "GET", "/api/admin/gallery/works/mp4-work/mp4", nil)
	if w.Code != 200 {
		t.Fatalf("admin mp4 status=%d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("content-type=%q", ct)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "挥手打招呼.mp4") {
		t.Fatalf("disposition=%q", w.Header().Get("Content-Disposition"))
	}
	body := w.Body.Bytes()
	if len(body) < 32 || string(body[4:8]) != "ftyp" {
		t.Fatalf("response is not mp4: len=%d", len(body))
	}
	if w = request(t, a, admin, "GET", "/api/admin/gallery/works/missing/mp4", nil); w.Code != 404 {
		t.Fatalf("missing work status=%d", w.Code)
	}
}

func TestDownloadBaseNameSanitizes(t *testing.T) {
	if got := downloadBaseName(`你好/世界?.gif`, "id1"); got != "你好_世界_.gif" {
		t.Fatalf("got %q", got)
	}
	if got := downloadBaseName("   ", "fallback"); got != "fallback" {
		t.Fatalf("empty name got %q", got)
	}
}
