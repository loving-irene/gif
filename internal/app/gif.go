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

// synthesizeGIF 在服务器端把 4×4 动作序列图合成为 256×256 的 16 帧循环 GIF。
// 算法与浏览器端 gif-worker.v1.js 保持一致：15-bit 颜色直方图 + 中位切分共享调色板，
// 0 号索引透明，第 5—8 帧（展开阶段）稍快，逐帧恢复背景以正确呈现透明。
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
	frames := make([]*image.NRGBA, 16)
	for n := 0; n < 16; n++ {
		frame := image.NewNRGBA(image.Rect(0, 0, 256, 256))
		cell := image.Rect((n%4)*tile, (n/4)*tile, (n%4)*tile+tile, (n/4)*tile+tile)
		if tile == 256 {
			draw.Draw(frame, frame.Rect, img, cell.Min, draw.Src)
		} else {
			xdraw.CatmullRom.Scale(frame, frame.Rect, img, cell, xdraw.Src, nil)
		}
		frames[n] = frame
	}
	histogram := make([]uint32, 32768)
	for _, frame := range frames {
		for i := 0; i+3 < len(frame.Pix); i += 4 {
			if frame.Pix[i+3] >= 128 {
				histogram[int(frame.Pix[i]>>3)<<10|int(frame.Pix[i+1]>>3)<<5|int(frame.Pix[i+2]>>3)]++
			}
		}
	}
	palette, mapping := adaptivePalette(histogram)
	out := &gif.GIF{LoopCount: 0, Config: image.Config{ColorModel: palette, Width: 256, Height: 256}}
	for n, frame := range frames {
		p := image.NewPaletted(image.Rect(0, 0, 256, 256), palette)
		for y := 0; y < 256; y++ {
			for x := 0; x < 256; x++ {
				i := frame.PixOffset(x, y)
				if frame.Pix[i+3] < 128 {
					p.SetColorIndex(x, y, 0)
					continue
				}
				p.SetColorIndex(x, y, mapping[int(frame.Pix[i]>>3)<<10|int(frame.Pix[i+1]>>3)<<5|int(frame.Pix[i+2]>>3)])
			}
		}
		delay := 10
		if n >= 4 && n <= 7 {
			delay = 6
		}
		out.Image = append(out.Image, p)
		out.Delay = append(out.Delay, delay)
		out.Disposal = append(out.Disposal, gif.DisposalBackground)
	}
	var buf bytes.Buffer
	if err = gif.EncodeAll(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
