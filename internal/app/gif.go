package app

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"sort"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// normalizeMotionPrompt 删除旧配置中常见的固定网格句。规格说明由
// motionSpecPrompt 统一追加，避免切换规格后上游同时看到互相冲突的尺寸和格数要求。
func normalizeMotionPrompt(prompt string) string {
	for _, phrase := range []string{
		"输出一张1024×1024透明PNG，按后台动作序列图规格排列连续帧。",
		"输出一张1024×1024透明PNG，严格4列×4行共16格，每格256×256。从左到右、从上到下排列同一次完整动作：1—4准备，5—8展开，9—12动作重点，13—16收势回位。",
		"输出一张1024×1024透明PNG，严格5列×5行共25格，合成后每帧128×128。从左到右、从上到下排列同一次完整动作：1—6准备，7—12展开，13—18动作重点，19—25收势回位。25格必须是按时间等间隔采样的连续动作，相邻格只允许小步长变化，不得跳过中间姿态或重复静止帧。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定。",
		"输出一张1024×1024透明PNG，严格10列×10行共100格，合成后每帧128×128。从左到右、从上到下排列同一次完整动作：1—25准备，26—50展开，51—75动作重点，76—100收势回位。100格必须覆盖同一个完整动作周期并按时间等间隔采样，相邻格只允许极小步长变化，不得跳过中间姿态、重复静止帧或把多个动作拼在一起。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定。",
		"输出一张2048×2048透明PNG，严格10列×10行共100格，合成后每帧256×256。从左到右、从上到下排列同一次完整动作：1—25准备，26—50展开，51—75动作重点，76—100收势回位。100格必须覆盖同一个完整动作周期并按时间等间隔采样，相邻格只允许极小步长变化，不得跳过中间姿态、重复静止帧或把多个动作拼在一起。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定。",
		"输出一张1024×1024透明PNG，严格4列×4行共16格，每格256×256。",
		"严格4列×4行共16格，每格256×256。",
		"输出一张1024x1024透明PNG，严格4列x4行共16格，每格256x256。",
		"严格4列x4行共16格，每格256x256。",
		"输出一张1024*1024透明PNG，严格4列*4行共16格，每格256*256。",
		"严格4列*4行共16格，每格256*256。",
		"1—4准备，5—8展开，9—12动作重点，13—16收势回位。",
		"1-4准备，5-8展开，9-12动作重点，13-16收势回位。",
		"输出一张1024×1024透明PNG，严格5列×5行共25格，每格128×128。",
		"严格5列×5行共25格，每格128×128。",
		"输出一张1024×1024透明PNG，严格5列×5行共25格，合成后每帧128×128。",
		"严格5列×5行共25格，合成后每帧128×128。",
		"输出一张1024*1024透明PNG，严格5列*5行共25格，每格128*128。",
		"严格5列*5行共25格，每格128*128。",
		"1—6准备，7—12展开，13—18动作重点，19—25收势回位。",
		"1-6准备，7-12展开，13-18动作重点，19-25收势回位。",
		"25格必须是按时间等间隔采样的连续动作，相邻格只允许小步长变化，不得跳过中间姿态或重复静止帧。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定；除动作本身必需的连续位移、起跳和落地外，不得左右漂移、上下抖动或忽大忽小。",
		"输出一张1024×1024透明PNG，严格10列×10行共100格，合成后每帧128×128。",
		"严格10列×10行共100格，合成后每帧128×128。",
		"输出一张1024x1024透明PNG，严格10列x10行共100格，合成后每帧128x128。",
		"严格10列x10行共100格，合成后每帧128x128。",
		"输出一张1024*1024透明PNG，严格10列*10行共100格，合成后每帧128*128。",
		"严格10列*10行共100格，合成后每帧128*128。",
		"1—25准备，26—50展开，51—75动作重点，76—100收势回位。",
		"1-25准备，26-50展开，51-75动作重点，76-100收势回位。",
		"100格必须覆盖同一个完整动作周期并按时间等间隔采样，相邻格只允许极小步长变化，不得跳过中间姿态、重复静止帧或把多个动作拼在一起。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定；除动作本身必需的连续位移、起跳和落地外，不得左右漂移、上下抖动或忽大忽小。",
		"输出一张2048×2048透明PNG，严格10列×10行共100格，合成后每帧128×128。",
		"严格10列×10行共100格，源图2048×2048，合成后每帧128×128。",
		"输出一张2048x2048透明PNG，严格10列x10行共100格，合成后每帧128x128。",
		"输出一张2048*2048透明PNG，严格10列*10行共100格，合成后每帧128*128。",
		"输出一张2048×2048透明PNG，严格10列×10行共100格，合成后每帧256×256。",
		"严格10列×10行共100格，源图2048×2048，合成后每帧256×256。",
		"输出一张2048x2048透明PNG，严格10列x10行共100格，合成后每帧256x256。",
		"输出一张2048*2048透明PNG，严格10列*10行共100格，合成后每帧256*256。",
	} {
		prompt = strings.ReplaceAll(prompt, phrase, "")
	}
	return strings.TrimSpace(prompt)
}

// motionSpec 是动作序列图的网格规格，由后台配置选择：
// 4x4 共16格、每帧输出256×256；5x5 共25格、每帧输出128×128；10x10 共100格、每帧输出256×256。
// 4×4/5×5 使用 1024×1024 源图，10×10 使用 2048×2048 源图；切格按比例取整划分边界，
// 因此三种规格都兼容不能整除格数的图宽。
type motionSpec struct {
	id         string // 配置编号（4x4 / 5x5 / 10x10），用于提示词与日志
	cols       int    // 每边格数
	frames     int    // cols×cols 总帧数
	sourceSize int    // 上游方形序列图边长
	size       int    // 输出 GIF 每帧边长
	delay      int    // GIF 帧延时，单位为 1/100 秒
}

// motionSpecs 固定可选规格：4×4（1—4准备、5—8展开、9—12重点、13—16收势）
// 与 5×5（1—6准备、7—12展开、13—18重点、19—25收势）、
// 10×10（每阶段25帧）。
var motionSpecs = map[string]motionSpec{
	"4x4":   {id: "4x4", cols: 4, frames: 16, sourceSize: 1024, size: 256, delay: gifFrameDelay},
	"5x5":   {id: "5x5", cols: 5, frames: 25, sourceSize: 1024, size: 128, delay: smoothishGifFrameDelay},
	"10x10": {id: "10x10", cols: 10, frames: 100, sourceSize: 2048, size: 256, delay: smoothGifFrameDelay},
}

const gifFrameDelay = 8           // GIF 延时单位为 1/100 秒；8 即统一 80ms（12.5 FPS）。
const smoothishGifFrameDelay = 7  // 25帧规格使用70ms，总时长约1.75秒，比16帧更顺、比100帧更稳。
const smoothGifFrameDelay = 2     // 100帧规格使用20ms（50 FPS），总时长仍约2秒。

// motionGridIDs 返回后台可选的动作序列图规格编号。
func motionGridIDs() []string { return []string{"4x4", "5x5", "10x10"} }

// motionSpecOf 解析规格编号，空值或未知编号回落到默认 5×5。
func motionSpecOf(id string) motionSpec {
	if s, ok := motionSpecs[id]; ok {
		return s
	}
	return motionSpecs["5x5"]
}

// motionSpecPrompt 是追加在动作提示词末尾的规格说明：后台切换网格规格后，
// 以本段覆盖提示词模板里写死的格数与阶段划分，保证生成与合成两侧始终一致。
func motionSpecPrompt(id string) string {
	s := motionSpecOf(id)
	cell := ""
	if s.cols == 4 {
		cell = "，每格256×256"
	} else {
		cell = fmt.Sprintf("，合成后每帧%d×%d", s.size, s.size)
	}
	prompt := fmt.Sprintf("动作序列图规格（以此为准，前文若出现其他图片尺寸、格数、每格尺寸或阶段划分描述，以本段为准）：一张%d×%d透明PNG，严格%d列×%d行共%d格%s。从左到右、从上到下排列同一次完整动作：%s。每格无边框无间隙无编号无文字；角色完整缩在单格内，四周留透明安全边距，禁止邻格内容渗入（尤其下一格头顶不得出现在本格脚底），武器特效不跨格、不裁切。",
		s.sourceSize, s.sourceSize, s.cols, s.cols, s.frames, cell, s.phases())
	if s.frames == 25 {
		prompt += " 25格必须是按时间等间隔采样的连续动作，相邻格只允许小步长变化，不得跳过中间姿态或重复静止帧。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定；除动作本身必需的连续位移、起跳和落地外，不得左右漂移、上下抖动或忽大忽小。"
	} else if s.frames == 100 {
		prompt += " 100格必须覆盖同一个完整动作周期并按时间等间隔采样，相邻格只允许极小步长变化，不得跳过中间姿态、重复静止帧或把多个动作拼在一起。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定；除动作本身必需的连续位移、起跳和落地外，不得左右漂移、上下抖动或忽大忽小。"
	}
	return prompt
}

// phases 返回 1 起的动作阶段划分文案。
func (s motionSpec) phases() string {
	switch s.frames {
	case 100:
		return "1—25准备，26—50展开，51—75动作重点，76—100收势回位"
	case 25:
		return "1—6准备，7—12展开，13—18动作重点，19—25收势回位"
	default:
		return "1—4准备，5—8展开，9—12动作重点，13—16收势回位"
	}
}

// synthesizeGIF 在服务器端把动作序列图按规格切格并合成为循环 GIF。
// 算法与浏览器端 gif-worker 保持一致：先按相邻帧的局部中位轨迹轻量稳定主体，
// 再使用 15-bit 颜色直方图 + 中位切分共享调色板；0 号索引透明，帧延时由规格决定，
// 逐帧恢复背景以正确呈现透明。
func synthesizeGIF(sheet []byte, spec motionSpec) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(sheet))
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width != height || width < spec.cols || height < spec.cols {
		return nil, errors.New("motion sheet is not a divisible square grid")
	}
	if spec.id == "10x10" && (width != spec.sourceSize || height != spec.sourceSize) {
		return nil, fmt.Errorf("motion sheet size %dx%d does not match requested %dx%d", width, height, spec.sourceSize, spec.sourceSize)
	}
	size := spec.size
	frames := make([]*image.NRGBA, spec.frames)
	for n := 0; n < spec.frames; n++ {
		// 按比例取整划分格边界：可整除时与原逐格切分完全一致，
		// 不可整除时每格相差不超过 1 像素，随后统一缩放到输出尺寸。
		x0, y0 := (n%spec.cols)*width/spec.cols, (n/spec.cols)*height/spec.cols
		x1, y1 := ((n%spec.cols)+1)*width/spec.cols, ((n/spec.cols)+1)*height/spec.cols
		cell := image.Rect(x0, y0, x1, y1)
		// 清掉脚下邻格渗边后，按主体包围盒适配到输出帧：头顶与脚底都留边，脚略靠下。
		frames[n] = fitCellFrame(img, cell, size)
	}
	frames = stabilizeFrames(frames, size)
	histogram := make([]uint32, 32768)
	for _, frame := range frames {
		for i := 0; i+3 < len(frame.Pix); i += 4 {
			if frame.Pix[i+3] >= 128 {
				histogram[int(frame.Pix[i]>>3)<<10|int(frame.Pix[i+1]>>3)<<5|int(frame.Pix[i+2]>>3)]++
			}
		}
	}
	palette, mapping := adaptivePalette(histogram)
	out := &gif.GIF{LoopCount: 0, Config: image.Config{ColorModel: palette, Width: size, Height: size}}
	for _, frame := range frames {
		p := image.NewPaletted(image.Rect(0, 0, size, size), palette)
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				i := frame.PixOffset(x, y)
				if frame.Pix[i+3] < 128 {
					p.SetColorIndex(x, y, 0)
					continue
				}
				p.SetColorIndex(x, y, mapping[int(frame.Pix[i]>>3)<<10|int(frame.Pix[i+1]>>3)<<5|int(frame.Pix[i+2]>>3)])
			}
		}
		out.Image = append(out.Image, p)
		out.Delay = append(out.Delay, spec.delay)
		out.Disposal = append(out.Disposal, gif.DisposalBackground)
	}
	var buf bytes.Buffer
	if err = gif.EncodeAll(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// insetCell 从格四周内缩约 5%，裁掉邻格渗边；至少保留半格可用区域。
// 保留给测试与无法识别主体时的回退路径。
func insetCell(cell image.Rectangle) image.Rectangle {
	dx, dy := cell.Dx(), cell.Dy()
	ix, iy := dx/20, dy/20
	if ix < 1 {
		ix = 1
	}
	if iy < 1 {
		iy = 1
	}
	if ix*2 >= dx {
		ix = dx / 4
	}
	if iy*2 >= dy {
		iy = dy / 4
	}
	if ix < 1 || iy < 1 {
		return cell
	}
	return image.Rect(cell.Min.X+ix, cell.Min.Y+iy, cell.Max.X-ix, cell.Max.Y-iy)
}

// fitCellFrame 从动作序列的一格提取主体并适配到 size×size 输出帧。
// 先清除脚下与主体分离的邻格头顶渗边，再按包围盒缩放：水平居中、脚底略靠下，头顶与鞋底都留边。
func fitCellFrame(src image.Image, cell image.Rectangle, size int) *image.NRGBA {
	cw, ch := cell.Dx(), cell.Dy()
	if cw < 1 || ch < 1 || size < 1 {
		return image.NewNRGBA(image.Rect(0, 0, size, size))
	}
	raw := image.NewNRGBA(image.Rect(0, 0, cw, ch))
	draw.Draw(raw, raw.Rect, src, cell.Min, draw.Src)
	clearBottomBleed(raw)
	bbox, ok := subjectBounds(raw)
	if !ok {
		return scaleCell(src, insetCell(cell), size)
	}
	pad := max(2, min(cw, ch)/16)
	bbox = image.Rect(bbox.Min.X-pad, bbox.Min.Y-pad, bbox.Max.X+pad, bbox.Max.Y+pad).Intersect(raw.Bounds())
	if bbox.Empty() {
		return scaleCell(src, insetCell(cell), size)
	}
	margin := float64(size) * 0.04
	avail := float64(size) - 2*margin
	if avail < 8 {
		avail = float64(size)
		margin = 0
	}
	// 脚略靠下：可用高度略多于等边距，让鞋底更贴近底边。
	bw, bh := float64(bbox.Dx()), float64(bbox.Dy())
	footBias := float64(size) * 0.02
	availH := avail + footBias
	scale := math.Min(avail/bw, availH/bh)
	if scale > 1.12 {
		scale = 1.12
	}
	if scale < 0.2 {
		scale = 0.2
	}
	tw := max(1, int(math.Round(bw*scale)))
	th := max(1, int(math.Round(bh*scale)))
	tmp := image.NewNRGBA(image.Rect(0, 0, tw, th))
	xdraw.CatmullRom.Scale(tmp, tmp.Rect, raw, bbox, xdraw.Src, nil)
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	x0 := (size - tw) / 2
	y0 := size - int(math.Round(margin)) - th
	if y0 < int(math.Round(margin)) {
		y0 = int(math.Round(margin))
	}
	if y0+th > size {
		y0 = size - th
	}
	if y0 < 0 {
		y0 = 0
	}
	draw.Draw(out, image.Rect(x0, y0, x0+tw, y0+th), tmp, image.Point{}, draw.Src)
	return out
}

func scaleCell(src image.Image, rect image.Rectangle, size int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	if rect.Empty() {
		return out
	}
	if rect.Dx() == size && rect.Dy() == size {
		draw.Draw(out, out.Rect, src, rect.Min, draw.Src)
		return out
	}
	xdraw.CatmullRom.Scale(out, out.Rect, src, rect, xdraw.Src, nil)
	return out
}

// clearBottomBleed 清除格底邻格头顶渗边。
// 优先：底部分离色块（上方有空白行）；否则：在底部 30% 内找行密度谷底切开。
func clearBottomBleed(frame *image.NRGBA) {
	w, h := frame.Rect.Dx(), frame.Rect.Dy()
	if h < 8 {
		return
	}
	rowCount := make([]int, h)
	for y := 0; y < h; y++ {
		n := 0
		for x := 0; x < w; x++ {
			if isSubjectPixel(frame, x, y) {
				n++
			}
		}
		rowCount[y] = n
	}
	cutFrom := -1
	// 路径 1：底带与主体之间有 ≥2 行空白。
	bottom := -1
	for y := h - 1; y >= 0; y-- {
		if rowCount[y] > 0 {
			bottom = y
			break
		}
	}
	if bottom >= 0 {
		bleedTop := bottom
		for y := bottom; y >= 0 && rowCount[y] > 0; y-- {
			bleedTop = y
		}
		gapEnd := bleedTop - 1
		for gapEnd >= 0 && rowCount[gapEnd] == 0 {
			gapEnd--
		}
		gapRows := bleedTop - 1 - gapEnd
		bleedH := bottom - bleedTop + 1
		if gapRows >= 2 && bleedH <= h*28/100 && gapEnd >= 0 {
			cutFrom = bleedTop
		}
	}
	// 路径 2：脚下与邻格头顶几乎贴住时，在底部 30% 找行密度谷底。
	if cutFrom < 0 {
		lo := h * 70 / 100
		bestY, bestCnt := -1, w+1
		for y := lo; y < h-1; y++ {
			if rowCount[y] < bestCnt {
				bestCnt = rowCount[y]
				bestY = y
			}
		}
		if bestY > lo {
			peakAbove, peakBelow := 0, 0
			for y := lo; y < bestY; y++ {
				if rowCount[y] > peakAbove {
					peakAbove = rowCount[y]
				}
			}
			for y := bestY + 1; y < h; y++ {
				if rowCount[y] > peakBelow {
					peakBelow = rowCount[y]
				}
			}
			// 谷底明显低于两侧高峰，且下方仍有一团内容 → 视为邻格头顶。
			thresh := max(3, max(peakAbove, peakBelow)*15/100)
			if peakAbove >= 12 && peakBelow >= 12 && bestCnt <= thresh && (h-1-bestY) <= h*28/100 {
				cutFrom = bestY + 1
			}
		}
	}
	if cutFrom < 0 || cutFrom >= h {
		return
	}
	for y := cutFrom; y < h; y++ {
		for x := 0; x < w; x++ {
			i := frame.PixOffset(x, y)
			frame.Pix[i+3] = 0
		}
	}
}

func subjectBounds(frame *image.NRGBA) (image.Rectangle, bool) {
	w, h := frame.Rect.Dx(), frame.Rect.Dy()
	minX, minY, maxX, maxY := w, h, -1, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !isSubjectPixel(frame, x, y) {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < minX || maxY < minY {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), true
}

func isSubjectPixel(frame *image.NRGBA, x, y int) bool {
	i := frame.PixOffset(x, y)
	a := frame.Pix[i+3]
	if a < 128 {
		return false
	}
	r, g, b := frame.Pix[i], frame.Pix[i+1], frame.Pix[i+2]
	// 近似纯白当作背景（上游常输出不透明白底）。
	if r > 245 && g > 245 && b > 245 {
		return false
	}
	return true
}

type subjectMetrics struct {
	valid         bool
	centerX, foot float64
	area          float64
}

// stabilizeFrames 用循环五帧时间窗过滤单帧位置和尺寸抖动。目标值来自局部中位数，
// 因而连续的横移、起跳和落地仍会保留；平移和缩放另有限幅，避免误把武器或特效当成人物后过度校正。
func stabilizeFrames(frames []*image.NRGBA, size int) []*image.NRGBA {
	if len(frames) < 3 {
		return frames
	}
	metrics := make([]subjectMetrics, len(frames))
	for i, frame := range frames {
		metrics[i] = measureSubject(frame)
	}
	out := make([]*image.NRGBA, len(frames))
	maxShift := float64(size) * 0.08
	for i, frame := range frames {
		current := metrics[i]
		if !current.valid {
			out[i] = frame
			continue
		}
		centers, feet, areas := make([]float64, 0, 5), make([]float64, 0, 5), make([]float64, 0, 5)
		for offset := -2; offset <= 2; offset++ {
			near := metrics[(i+offset+len(frames))%len(frames)]
			if near.valid {
				centers = append(centers, near.centerX)
				feet = append(feet, near.foot)
				areas = append(areas, near.area)
			}
		}
		targetX, targetFoot, targetArea := median(centers), median(feet), median(areas)
		dx := clamp(targetX-current.centerX, -maxShift, maxShift)
		dy := clamp(targetFoot-current.foot, -maxShift, maxShift)
		scale := clamp(math.Sqrt(targetArea/current.area), 0.94, 1.06)
		if dx == 0 && dy == 0 && scale == 1 {
			out[i] = frame
			continue
		}
		scaledSize := int(math.Round(float64(size) * scale))
		tmp := image.NewNRGBA(image.Rect(0, 0, scaledSize, scaledSize))
		xdraw.CatmullRom.Scale(tmp, tmp.Rect, frame, frame.Rect, xdraw.Src, nil)
		x0 := int(math.Round(current.centerX + dx - scale*current.centerX))
		y0 := int(math.Round(current.foot + dy - scale*current.foot))
		stable := image.NewNRGBA(image.Rect(0, 0, size, size))
		draw.Draw(stable, image.Rect(x0, y0, x0+scaledSize, y0+scaledSize), tmp, image.Point{}, draw.Src)
		out[i] = stable
	}
	return out
}

func measureSubject(frame *image.NRGBA) subjectMetrics {
	minX, maxX, maxY, area := frame.Rect.Dx(), -1, -1, 0
	for y := 0; y < frame.Rect.Dy(); y++ {
		for x := 0; x < frame.Rect.Dx(); x++ {
			if !isSubjectPixel(frame, x, y) {
				continue
			}
			area++
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if area == 0 {
		return subjectMetrics{}
	}
	return subjectMetrics{valid: true, centerX: float64(minX+maxX+1) / 2, foot: float64(maxY + 1), area: float64(area)}
}

func median(values []float64) float64 {
	sort.Float64s(values)
	mid := len(values) / 2
	if len(values)%2 == 0 {
		return (values[mid-1] + values[mid]) / 2
	}
	return values[mid]
}

func clamp(value, low, high float64) float64 {
	return math.Max(low, math.Min(high, value))
}

type quantPoint struct {
	key     int
	r, g, b int
	n       uint32
}
type quantBox struct {
	points []quantPoint
	axis   int
	score  uint64
}

func boxOf(points []quantPoint) quantBox {
	lo := [3]int{255, 255, 255}
	hi := [3]int{0, 0, 0}
	var total uint64
	for _, p := range points {
		for axis, v := range [3]int{p.r, p.g, p.b} {
			if v < lo[axis] {
				lo[axis] = v
			}
			if v > hi[axis] {
				hi[axis] = v
			}
		}
		total += uint64(p.n)
	}
	spread := [3]int{hi[0] - lo[0], hi[1] - lo[1], hi[2] - lo[2]}
	axis := 0
	for i := 1; i < 3; i++ {
		if spread[i] > spread[axis] {
			axis = i
		}
	}
	return quantBox{points: points, axis: axis, score: uint64(spread[axis]) * total}
}

func adaptivePalette(histogram []uint32) (color.Palette, []uint8) {
	points := []quantPoint{}
	for key, count := range histogram {
		if count == 0 {
			continue
		}
		points = append(points, quantPoint{key: key, r: (key>>10)*8 + 4, g: ((key>>5)&31)*8 + 4, b: (key&31)*8 + 4, n: count})
	}
	boxes := []quantBox{}
	if len(points) > 0 {
		boxes = append(boxes, boxOf(points))
	}
	for len(boxes) < 255 {
		sort.SliceStable(boxes, func(i, j int) bool { return boxes[i].score > boxes[j].score })
		index := -1
		for i, box := range boxes {
			if len(box.points) > 1 {
				index = i
				break
			}
		}
		if index < 0 {
			break
		}
		box := boxes[index]
		boxes = append(boxes[:index], boxes[index+1:]...)
		value := func(p quantPoint) int { return [3]int{p.r, p.g, p.b}[box.axis] }
		sort.SliceStable(box.points, func(i, j int) bool { return value(box.points[i]) < value(box.points[j]) })
		var total uint64
		for _, p := range box.points {
			total += uint64(p.n)
		}
		var sum uint64
		split := 1
		for ; split < len(box.points); split++ {
			sum += uint64(box.points[split-1].n)
			if float64(sum) >= float64(total)/2 {
				break
			}
		}
		if split > len(box.points)-1 {
			split = len(box.points) - 1
		}
		boxes = append(boxes, boxOf(box.points[:split]), boxOf(box.points[split:]))
	}
	// 0 号索引保留给透明，其余最多 255 个颜色来自各颜色盒的加权平均。
	palette := color.Palette{color.RGBA{}}
	mapping := make([]uint8, 32768)
	for _, box := range boxes {
		var total float64
		sums := [3]float64{}
		for _, p := range box.points {
			total += float64(p.n)
			sums[0] += float64(p.r * int(p.n))
			sums[1] += float64(p.g * int(p.n))
			sums[2] += float64(p.b * int(p.n))
		}
		c := color.RGBA{}
		if total > 0 {
			c = color.RGBA{R: uint8(math.Round(sums[0] / total)), G: uint8(math.Round(sums[1] / total)), B: uint8(math.Round(sums[2] / total)), A: 255}
		}
		palette = append(palette, c)
		index := uint8(len(palette) - 1)
		for _, p := range box.points {
			mapping[p.key] = index
		}
	}
	for len(palette) < 256 {
		palette = append(palette, color.RGBA{A: 255})
	}
	return palette, mapping
}
