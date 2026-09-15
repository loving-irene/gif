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

	xdraw "golang.org/x/image/draw"
)

// motionSpec 是动作序列图的网格规格，由后台配置选择：
// 4x4 共16格、每帧输出256×256；5x5 共25格、每帧输出128×128。
// 上游始终产出 1024×1024 的方形序列图，切格按比例取整划分边界，
// 因此两种规格都兼容可整除与不可整除（如 5×5 对 1024）的图宽。
type motionSpec struct {
	id     string // 配置编号（4x4 / 5x5），用于提示词与日志
	cols   int    // 每边格数
	frames int    // cols×cols 总帧数
	size   int    // 输出 GIF 每帧边长
}

// motionSpecs 固定可选规格：4×4（1—4准备、5—8展开、9—12重点、13—16收势）
// 与 5×5（1—6准备、7—12展开、13—18重点、19—25收势）。
var motionSpecs = map[string]motionSpec{
	"4x4": {id: "4x4", cols: 4, frames: 16, size: 256},
	"5x5": {id: "5x5", cols: 5, frames: 25, size: 128},
}

const gifFrameDelay = 8 // GIF 延时单位为 1/100 秒；8 即统一 80ms（12.5 FPS）。

// motionGridIDs 返回后台可选的动作序列图规格编号。
func motionGridIDs() []string { return []string{"4x4", "5x5"} }

// motionSpecOf 解析规格编号，空值或未知编号回落到默认 4×4。
func motionSpecOf(id string) motionSpec {
	if s, ok := motionSpecs[id]; ok {
		return s
	}
	return motionSpecs["4x4"]
}

// motionSpecPrompt 是追加在动作提示词末尾的规格说明：后台切换网格规格后，
// 以本段覆盖提示词模板里写死的格数与阶段划分，保证生成与合成两侧始终一致。
func motionSpecPrompt(id string) string {
	s := motionSpecOf(id)
	cell := ""
	if s.cols == 4 {
		cell = "，每格256×256"
	}
	prompt := fmt.Sprintf("动作序列图规格（以此为准，前文若出现其他格数、每格尺寸或阶段划分描述，以本段为准）：一张1024×1024透明PNG，严格%d列×%d行共%d格%s。从左到右、从上到下排列同一次完整动作：%s。每格无边框无间隙无编号无文字，角色武器特效不跨格、不裁切。",
		s.cols, s.cols, s.frames, cell, s.phases())
	if s.frames == 25 {
		prompt += " 25格必须是按时间等间隔采样的连续动作，相邻格只允许小步长变化，不得跳过中间姿态或重复静止帧。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定；除动作本身必需的连续位移、起跳和落地外，不得左右漂移、上下抖动或忽大忽小。"
	}
	return prompt
}

// phases 返回 1 起的动作阶段划分文案。
func (s motionSpec) phases() string {
	switch s.frames {
	case 25:
		return "1—6准备，7—12展开，13—18动作重点，19—25收势回位"
	default:
		return "1—4准备，5—8展开，9—12动作重点，13—16收势回位"
	}
}

// synthesizeGIF 在服务器端把动作序列图按规格切格并合成为循环 GIF。
// 算法与浏览器端 gif-worker 保持一致：先按相邻帧的局部中位轨迹轻量稳定主体，
// 再使用 15-bit 颜色直方图 + 中位切分共享调色板；0 号索引透明，统一每帧 80ms，
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
	size := spec.size
	frames := make([]*image.NRGBA, spec.frames)
	for n := 0; n < spec.frames; n++ {
		frame := image.NewNRGBA(image.Rect(0, 0, size, size))
		// 按比例取整划分格边界：可整除时与原逐格切分完全一致，
		// 不可整除时（5×5 对 1024）每格相差不超过 1 像素，缩放到输出尺寸后无差别。
		x0, y0 := (n%spec.cols)*width/spec.cols, (n/spec.cols)*height/spec.cols
		x1, y1 := ((n%spec.cols)+1)*width/spec.cols, ((n/spec.cols)+1)*height/spec.cols
		cell := image.Rect(x0, y0, x1, y1)
		if cell.Dx() == size && cell.Dy() == size {
			draw.Draw(frame, frame.Rect, img, cell.Min, draw.Src)
		} else {
			xdraw.CatmullRom.Scale(frame, frame.Rect, img, cell, xdraw.Src, nil)
		}
		frames[n] = frame
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
		out.Delay = append(out.Delay, gifFrameDelay)
		out.Disposal = append(out.Disposal, gif.DisposalBackground)
	}
	var buf bytes.Buffer
	if err = gif.EncodeAll(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
			if frame.Pix[frame.PixOffset(x, y)+3] < 128 {
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
