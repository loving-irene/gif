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

const (
	// mp4LoopCount 是转存 MP4 时 GIF 完整播放的次数（含首次）。三次足够让
	// 朋友圈中的短动作看清楚，同时避免把循环次数变成文件体积的主要来源。
	mp4LoopCount = 3
	// 微信聊天气泡和朋友圈预览都会缩放视频；360px 能保留贴纸轮廓，且比
	// 直接把 1024px 序列图带入视频小很多。小于该尺寸的 GIF 也会平滑放大，
	// 避免 128px/256px 视频被平台再次放大后出现明显锯齿。
	mp4CanvasSize = 360
	mp4FPS        = 20
)

// gifToMP4 用本机 ffmpeg 把 GIF 转成适合微信聊天和朋友圈分享的轻量 H.264 MP4。
// 输出会按 mp4LoopCount 完整循环，统一为 360×360、20fps、白色不透明背景，
// 并限制峰值码率，避免 ffmpeg 默认质量设置产生体积过大的视频。GIF 没有音轨，
// 因此输出也明确禁用音频。
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

	// -stream_loop N 表示额外再读 N 次输入，总播放次数为 N+1。透明 GIF
	// 先叠到白底再转 yuv420p；MP4/H.264 本身不保存 GIF 的透明通道，白底
	// 比 ffmpeg 默认的黑底更接近聊天里的贴纸预览。
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-stream_loop", fmt.Sprintf("%d", mp4LoopCount-1),
		"-i", inPath,
		"-an",
		"-filter_complex", fmt.Sprintf("[0:v]fps=%d,format=rgba,scale=%d:%d:flags=lanczos[fg];color=c=white:s=%dx%d:r=%d[bg];[bg][fg]overlay=shortest=1:format=auto,format=yuv420p[v]", mp4FPS, mp4CanvasSize, mp4CanvasSize, mp4CanvasSize, mp4CanvasSize, mp4FPS),
		"-map", "[v]",
		"-c:v", "libx264",
		"-preset", "medium",
		"-tune", "animation",
		"-profile:v", "baseline",
		"-level", "3.0",
		// CRF 22 keeps the small character edges clean; maxrate/bufsize still
		// bound the bitrate for the short clips used by the service.
		"-crf", "22",
		"-maxrate", "900k",
		"-bufsize", "1800k",
		"-g", fmt.Sprintf("%d", mp4FPS*2),
		"-movflags", "+faststart",
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
