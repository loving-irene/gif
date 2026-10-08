package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

var errFFmpegMissing = errors.New("ffmpeg missing")

// mp4LoopCount 是转存 MP4 时 GIF 完整播放的次数（含首次）。
const mp4LoopCount = 3

// gifToMP4 用本机 ffmpeg 把 GIF 转成适合网页播放的 H.264 MP4。
// 输出会按 mp4LoopCount 完整循环；宽高取偶数以满足 yuv420p。
// 失败时返回可读错误，不把 ffmpeg 原始输出直接给前端。
func gifToMP4(ctx context.Context, gifBytes []byte) ([]byte, error) {
	if len(gifBytes) == 0 {
		return nil, errors.New("empty gif")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, errFFmpegMissing
	}
	dir, err := os.MkdirTemp("", "gif-mp4-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	inPath := filepath.Join(dir, "input.gif")
	outPath := filepath.Join(dir, "output.mp4")
	if err := os.WriteFile(inPath, gifBytes, 0o600); err != nil {
		return nil, err
	}

	// -stream_loop N 表示额外再读 N 次输入，总播放次数为 N+1。
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-stream_loop", fmt.Sprintf("%d", mp4LoopCount-1),
		"-i", inPath,
		"-an",
		"-movflags", "+faststart",
		"-pix_fmt", "yuv420p",
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2",
		outPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("ffmpeg: %s", detail)
	}
	out, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("empty mp4")
	}
	return out, nil
}

func (a *App) adminWorkMP4(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var gifBytes []byte
	var name string
	if a.db.QueryRow("SELECT gif,name FROM works WHERE id=?", id).Scan(&gifBytes, &name) != nil || gifBytes == nil {
		fail(w, 404, "作品不存在或没有 GIF")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	mp4, err := gifToMP4(ctx, gifBytes)
	if err != nil {
		if errors.Is(err, errFFmpegMissing) {
			respond(w, 503, map[string]string{"error": "本机未安装 ffmpeg，无法转换 MP4"})
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			respond(w, 504, map[string]string{"error": "GIF 转 MP4 超时"})
			return
		}
		respond(w, 502, map[string]string{"error": "GIF 转 MP4 失败"})
		return
	}

	filename := downloadBaseName(name, id) + ".mp4"
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(mp4)
}

// downloadBaseName 把作品名收成适合做附件文件名的安全片段；空名时回退到编号。
func downloadBaseName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = fallback
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 32 || r == 127 || strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteByte('_')
		case unicode.IsSpace(r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return fallback
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}
