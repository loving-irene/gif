package app

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image/gif"
	"strings"
)

func motionPromptForModel(cfg Settings, action string) string {
	if supportsDirectGIF(cfg) {
		return directGIFPrompt(builtInMotionPrefix + strings.TrimSpace(action))
	}
	return buildMotionPrompt(action, cfg.MotionGrid)
}

func directGIFPrompt(prompt string) string {
	for _, grid := range motionGridIDs() {
		prompt = strings.ReplaceAll(prompt, motionSpecPrompt(grid), "")
	}
	prompt = normalizeMotionPrompt(prompt)
	prompt = strings.NewReplacer("每一格", "每一帧", "入格", "入画").Replace(prompt)
	return strings.TrimSpace(prompt) + "\n【最终输出要求】直接输出一份真正可播放的多帧 GIF 动图，透明背景，无限循环，约2秒。每帧只有同一个完整角色，镜头固定，人物大小和位置稳定，动作连续、首尾自然衔接，四周保留安全边距。使用简洁平涂色块，不加文字、边框、外发光、残影或漂浮装饰。不要输出静态图片、PNG、拼图、网格或动作序列图；前文若有分格排列要求，一律以本段的连续动画要求为准。"
}

// animatedGIFData only accepts provider output, never uploads/reference photos.
// Bound compressed size, canvas and decoded frame pixels before DecodeAll allocates.
func animatedGIFData(raw string) ([]byte, error) {
	const maxBytes = 20 << 20
	header, body, ok := strings.Cut(raw, ",")
	if !ok || header != "data:image/gif;base64" {
		return nil, errors.New("图像服务未返回 GIF 动图")
	}
	if len(body) > base64.StdEncoding.EncodedLen(maxBytes) {
		return nil, errors.New("GIF 超过20MB")
	}
	b, err := base64.StdEncoding.DecodeString(body)
	if err != nil || len(b) > maxBytes {
		return nil, errors.New("GIF 编码无效或超过20MB")
	}
	if err = checkGIFBudget(b); err != nil {
		return nil, err
	}
	anim, err := gif.DecodeAll(bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("GIF 动图损坏")
	}
	if len(anim.Image) < 2 {
		return nil, errors.New("图像服务返回了静态 GIF，需要多帧动图")
	}
	totalDelay := 0
	for _, delay := range anim.Delay {
		totalDelay += delay
	}
	if totalDelay == 0 {
		return nil, errors.New("GIF 缺少动画帧时长")
	}
	return b, nil
}

func checkGIFBudget(b []byte) error {
	invalid := errors.New("GIF 动图结构无效或超出安全限制")
	if len(b) < 13 || (string(b[:6]) != "GIF89a" && string(b[:6]) != "GIF87a") {
		return invalid
	}
	u16 := func(offset int) int { return int(binary.LittleEndian.Uint16(b[offset : offset+2])) }
	w, h := u16(6), u16(8)
	if w < 1 || h < 1 || w > 2048 || h > 2048 {
		return invalid
	}
	i := 13
	if b[10]&0x80 != 0 {
		i += 3 * (1 << ((b[10] & 7) + 1))
	}
	skipBlocks := func() bool {
		for i < len(b) {
			n := int(b[i])
			i++
			if n == 0 {
				return true
			}
			i += n
		}
		return false
	}
	frames, pixels := 0, 0
	for i < len(b) {
		tag := b[i]
		i++
		switch tag {
		case 0x21:
			if i >= len(b) {
				return invalid
			}
			i++ // extension label; all extensions use data sub-blocks
			if !skipBlocks() {
				return invalid
			}
		case 0x2c:
			if i+9 > len(b) {
				return invalid
			}
			fw, fh := u16(i+4), u16(i+6)
			if fw < 1 || fh < 1 || u16(i)+fw > w || u16(i+2)+fh > h {
				return invalid
			}
			frames++
			pixels += fw * fh
			if frames > 300 || pixels > 32*1024*1024 || int64(frames)*int64(w)*int64(h) > 128*1024*1024 {
				return invalid
			}
			packed := b[i+8]
			i += 9
			if packed&0x80 != 0 {
				i += 3 * (1 << ((packed & 7) + 1))
			}
			if i >= len(b) {
				return invalid
			}
			i++ // LZW minimum code size
			if !skipBlocks() {
				return invalid
			}
		case 0x3b:
			if i != len(b) || frames < 1 {
				return invalid
			}
			return nil
		default:
			return invalid
		}
	}
	return invalid
}
