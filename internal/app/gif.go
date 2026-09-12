package app

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"sort"

	xdraw "golang.org/x/image/draw"
)

// 动图合成参数：源 4×4 序列图固定 16 格，相邻两格之间补插 1 帧后共 32 帧，
// 以 20fps（每帧 50ms）循环播放。帧率与补插倍数集中在此，便于调优。
const (
	gifSize         = 256
	sheetCells      = 16 // 4×4 动作序列图
	gifOutputFrames = 32 // sheetCells × 2（每对源帧之间插 1 帧）
	gifFrameDelay   = 5  // 1/100 秒 = 50ms ≈ 20fps
)

// synthesizeGIF 在服务器端把 4×4 动作序列图合成为 256×256 的 32 帧循环 GIF：
// 16 格源帧两两补插（预乘 alpha 混合、轮廓并集）后，用 15-bit 颜色直方图 + 中位切分
// 构建共享调色板，再以 Floyd–Steinberg 误差扩散抖动消除色带。
// 算法与浏览器端 gif-worker.v2.js 保持一致：0 号索引透明，逐帧恢复背景以正确呈现透明。
func synthesizeGIF(sheet []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(sheet))
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width != height || width%4 != 0 || width < 4 {
		return nil, errors.New("motion sheet is not a divisible square grid")
	}
	tile := width / 4
	src := make([]*image.NRGBA, sheetCells)
	for n := 0; n < sheetCells; n++ {
		frame := image.NewNRGBA(image.Rect(0, 0, gifSize, gifSize))
		cell := image.Rect((n%4)*tile, (n/4)*tile, (n%4)*tile+tile, (n/4)*tile+tile)
		if tile == gifSize {
			draw.Draw(frame, frame.Rect, img, cell.Min, draw.Src)
		} else {
			xdraw.CatmullRom.Scale(frame, frame.Rect, img, cell, xdraw.Src, nil)
		}
		src[n] = frame
	}
	frames := make([]*image.NRGBA, gifOutputFrames)
	for n := 0; n < gifOutputFrames; n++ {
		pos := float64(n) * sheetCells / gifOutputFrames
		a := int(math.Floor(pos)) % sheetCells
		f := pos - float64(a)
		if f == 0 {
			frames[n] = src[a]
		} else {
			frames[n] = blendFrames(src[a], src[(a+1)%sheetCells], f)
		}
	}
	histogram := make([]uint32, 32768)
	for _, frame := range frames {
		for i := 0; i+3 < len(frame.Pix); i += 4 {
			if frame.Pix[i+3] >= 128 {
				histogram[int(frame.Pix[i]>>3)<<10|int(frame.Pix[i+1]>>3)<<5|int(frame.Pix[i+2]>>3)]++
			}
		}
	}
	palette := adaptivePalette(histogram)
	nearest := nearestTable(palette)
	out := &gif.GIF{LoopCount: 0, Config: image.Config{ColorModel: palette, Width: gifSize, Height: gifSize}}
	for _, frame := range frames {
		out.Image = append(out.Image, ditheredFrame(frame, palette, &nearest))
		out.Delay = append(out.Delay, gifFrameDelay)
		out.Disposal = append(out.Disposal, gif.DisposalBackground)
	}
	var buf bytes.Buffer
	if err = gif.EncodeAll(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// blendFrames 对相邻两帧做预乘 alpha 插值：轮廓取两帧并集（alpha 取大者），
// 颜色只在重叠区域交叉淡化，避免补插帧出现半透明边缘的闪烁与轮廓回缩。
func blendFrames(a, b *image.NRGBA, f float64) *image.NRGBA {
	out := image.NewNRGBA(a.Rect)
	for i := 0; i+3 < len(a.Pix); i += 4 {
		aA, bA := float64(a.Pix[i+3]), float64(b.Pix[i+3])
		alpha := aA
		if bA > alpha {
			alpha = bA
		}
		out.Pix[i+3] = uint8(alpha)
		w := aA*(1-f) + bA*f
		if w <= 0 {
			continue
		}
		out.Pix[i] = uint8((float64(a.Pix[i])*aA*(1-f) + float64(b.Pix[i])*bA*f) / w)
		out.Pix[i+1] = uint8((float64(a.Pix[i+1])*aA*(1-f) + float64(b.Pix[i+1])*bA*f) / w)
		out.Pix[i+2] = uint8((float64(a.Pix[i+2])*aA*(1-f) + float64(b.Pix[i+2])*bA*f) / w)
	}
	return out
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

func adaptivePalette(histogram []uint32) color.Palette {
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
	}
	for len(palette) < 256 {
		palette = append(palette, color.RGBA{A: 255})
	}
	return palette
}

// nearestTable 为每个 15-bit 颜色键预计算调色板中最近的实色索引（跳过 0 号透明位）。
func nearestTable(palette color.Palette) [32768]uint8 {
	var nearest [32768]uint8
	for key := 0; key < len(nearest); key++ {
		r := (key>>10)*8 + 4
		g := ((key>>5)&31)*8 + 4
		b := (key&31)*8 + 4
		best, bestDist := uint8(1), 1<<30
		for i := 1; i < len(palette); i++ {
			c := palette[i].(color.RGBA)
			dr, dg, db := int(c.R)-r, int(c.G)-g, int(c.B)-b
			if d := dr*dr + dg*dg + db*db; d < bestDist {
				best, bestDist = uint8(i), d
			}
		}
		nearest[key] = best
	}
	return nearest
}

// ditheredFrame 用 Floyd–Steinberg 误差扩散把 NRGBA 帧量化到共享调色板：
// 透明像素保持 0 号索引且不参与误差扩散，避免邻近色渗入主体边缘。
func ditheredFrame(frame *image.NRGBA, palette color.Palette, nearest *[32768]uint8) *image.Paletted {
	p := image.NewPaletted(image.Rect(0, 0, gifSize, gifSize), palette)
	curR, curG, curB := make([]int, gifSize+2), make([]int, gifSize+2), make([]int, gifSize+2)
	nextR, nextG, nextB := make([]int, gifSize+2), make([]int, gifSize+2), make([]int, gifSize+2)
	for y := 0; y < gifSize; y++ {
		// 换行：上一行沉淀到“下一行”的误差成为当前行误差，清零待写入的行。
		for x := 0; x < gifSize+2; x++ {
			curR[x], curG[x], curB[x] = nextR[x], nextG[x], nextB[x]
			nextR[x], nextG[x], nextB[x] = 0, 0, 0
		}
		for x := 0; x < gifSize; x++ {
			i := frame.PixOffset(x, y)
			if frame.Pix[i+3] < 128 {
				p.SetColorIndex(x, y, 0)
				curR[x+1], curG[x+1], curB[x+1] = 0, 0, 0
				continue
			}
			r := clampByte(int(frame.Pix[i]) + curR[x+1])
			g := clampByte(int(frame.Pix[i+1]) + curG[x+1])
			b := clampByte(int(frame.Pix[i+2]) + curB[x+1])
			idx := nearest[(r>>3)<<10|(g>>3)<<5|(b>>3)]
			c := palette[idx].(color.RGBA)
			p.SetColorIndex(x, y, idx)
			er, eg, eb := r-int(c.R), g-int(c.G), b-int(c.B)
			curR[x+2] += er * 7 / 16
			curG[x+2] += eg * 7 / 16
			curB[x+2] += eb * 7 / 16
			nextR[x] += er * 3 / 16
			nextG[x] += eg * 3 / 16
			nextB[x] += eb * 3 / 16
			nextR[x+1] += er * 5 / 16
			nextG[x+1] += eg * 5 / 16
			nextB[x+1] += eb * 5 / 16
			nextR[x+2] += er / 16
			nextG[x+2] += eg / 16
			nextB[x+2] += eb / 16
		}
	}
	return p
}

func clampByte(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
