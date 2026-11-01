package page

import (
	"bytes"
	"image"
	_ "image/jpeg" // decode pictures from the browser
	_ "image/png"
)

// VisualBoxes compares two pictures of a page and returns the areas that
// look different, as [x, y, w, h] fractions of the newer picture.
func VisualBoxes(before, after []byte) [][4]float64 {
	a, _, err := image.Decode(bytes.NewReader(before))
	if err != nil {
		return nil
	}
	b, _, err := image.Decode(bytes.NewReader(after))
	if err != nil {
		return nil
	}
	const cell = 24
	bw, bh := b.Bounds().Dx(), b.Bounds().Dy()
	w := min(a.Bounds().Dx(), bw)
	h := min(a.Bounds().Dy(), bh)
	if w < cell || h < cell {
		return nil
	}
	cols, rows := (w+cell-1)/cell, (h+cell-1)/cell
	hot := make([]bool, cols*rows)
	lum := func(img image.Image, x, y int) int {
		r, g, bl, _ := img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y).RGBA()
		return int((299*r + 587*g + 114*bl) / 1000 >> 8)
	}
	for cy := 0; cy < rows; cy++ {
		for cx := 0; cx < cols; cx++ {
			var sum, n int
			for y := cy * cell; y < min((cy+1)*cell, h); y += 3 {
				for x := cx * cell; x < min((cx+1)*cell, w); x += 3 {
					d := lum(a, x, y) - lum(b, x, y)
					if d < 0 {
						d = -d
					}
					sum += d
					n++
				}
			}
			if n > 0 && sum/n > 14 {
				hot[cy*cols+cx] = true
			}
		}
	}
	// connected areas of changed cells → bounding boxes
	seen := make([]bool, len(hot))
	var boxes [][4]float64
	for i := range hot {
		if !hot[i] || seen[i] {
			continue
		}
		minX, minY, maxX, maxY := cols, rows, 0, 0
		stack := []int{i}
		seen[i] = true
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := k%cols, k/cols
			minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, -1}, {1, -1}, {-1, 1}} {
				nx, ny := x+d[0], y+d[1]
				if nx < 0 || ny < 0 || nx >= cols || ny >= rows {
					continue
				}
				j := ny*cols + nx
				if hot[j] && !seen[j] {
					seen[j] = true
					stack = append(stack, j)
				}
			}
		}
		px, py := minX*cell, minY*cell
		pw, ph := min((maxX+1)*cell, w)-px, min((maxY+1)*cell, h)-py
		boxes = append(boxes, [4]float64{float64(px) / float64(bw), float64(py) / float64(bh), float64(pw) / float64(bw), float64(ph) / float64(bh)})
		if len(boxes) >= 40 {
			break
		}
	}
	// the newer picture is taller: the new part counts as changed
	if bh > h+cell {
		boxes = append(boxes, [4]float64{0, float64(h) / float64(bh), 1, float64(bh-h) / float64(bh)})
	}
	return boxes
}
