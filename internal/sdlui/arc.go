package sdlui

import (
	"math"

	"github.com/veandco/go-sdl2/sdl"
)

const degToRad = math.Pi / 180

// polarToXY は極座標を直交座標に変換する（12時=0°、時計回り正）
// gauge.js の polarToXY と同一の座標系
func polarToXY(cx, cy, r, deg float64) (float64, float64) {
	rad := deg * degToRad
	return cx + r*math.Sin(rad), cy - r*math.Cos(rad)
}

// DrawArc は円弧を描画する（ポリゴン近似、1°刻み）
// startDeg, endDeg: 角度（12時=0°、時計回り正、例: -135〜+135 で270°アーク）
// innerR, outerR: 内径・外径
func DrawArc(renderer *sdl.Renderer, cx, cy float64, innerR, outerR float64, startDeg, endDeg float64, color RGBA) {
	if endDeg <= startDeg {
		return
	}

	renderer.SetDrawColor(color.R, color.G, color.B, color.A)

	steps := int(math.Ceil(endDeg - startDeg))
	if steps < 1 {
		return
	}

	for i := 0; i < steps; i++ {
		d0 := startDeg + float64(i)
		d1 := startDeg + float64(i+1)
		if d1 > endDeg {
			d1 = endDeg
		}

		// 4点の台形
		ox0, oy0 := polarToXY(cx, cy, outerR, d0)
		ox1, oy1 := polarToXY(cx, cy, outerR, d1)
		ix0, iy0 := polarToXY(cx, cy, innerR, d0)
		ix1, iy1 := polarToXY(cx, cy, innerR, d1)

		// SDL_RenderGeometry で2つの三角形として描画
		verts := []sdl.Vertex{
			{Position: sdl.FPoint{X: float32(ox0), Y: float32(oy0)}, Color: color.ToSDLColor()},
			{Position: sdl.FPoint{X: float32(ox1), Y: float32(oy1)}, Color: color.ToSDLColor()},
			{Position: sdl.FPoint{X: float32(ix0), Y: float32(iy0)}, Color: color.ToSDLColor()},
			{Position: sdl.FPoint{X: float32(ix1), Y: float32(iy1)}, Color: color.ToSDLColor()},
		}
		indices := []int32{0, 1, 2, 1, 2, 3}

		renderer.RenderGeometry(nil, verts, indices)
	}
}

// DrawLine は2点間の線を描画する
func DrawLine(renderer *sdl.Renderer, x1, y1, x2, y2 float64, color RGBA) {
	renderer.SetDrawColor(color.R, color.G, color.B, color.A)
	renderer.DrawLineF(float32(x1), float32(y1), float32(x2), float32(y2))
}

// DrawThickLine は太線を描画する（直交方向に幅を持たせた矩形）
func DrawThickLine(renderer *sdl.Renderer, x1, y1, x2, y2 float64, width float64, color RGBA) {
	dx := x2 - x1
	dy := y2 - y1
	length := math.Sqrt(dx*dx + dy*dy)
	if length < 0.001 {
		return
	}

	// 直交方向の単位ベクトル × 幅/2
	nx := -dy / length * width / 2
	ny := dx / length * width / 2

	verts := []sdl.Vertex{
		{Position: sdl.FPoint{X: float32(x1 + nx), Y: float32(y1 + ny)}, Color: color.ToSDLColor()},
		{Position: sdl.FPoint{X: float32(x1 - nx), Y: float32(y1 - ny)}, Color: color.ToSDLColor()},
		{Position: sdl.FPoint{X: float32(x2 + nx), Y: float32(y2 + ny)}, Color: color.ToSDLColor()},
		{Position: sdl.FPoint{X: float32(x2 - nx), Y: float32(y2 - ny)}, Color: color.ToSDLColor()},
	}
	indices := []int32{0, 1, 2, 1, 2, 3}

	renderer.RenderGeometry(nil, verts, indices)
}

// DrawArcCaps はアーク両端に丸キャップを描画する
func DrawArcCaps(renderer *sdl.Renderer, cx, cy, r, halfW, startDeg, endDeg float64, color RGBA) {
	// 開始端
	sx, sy := polarToXY(cx, cy, r, startDeg)
	DrawCircleFilled(renderer, sx, sy, halfW, color)
	// 終了端
	ex, ey := polarToXY(cx, cy, r, endDeg)
	DrawCircleFilled(renderer, ex, ey, halfW, color)
}

// DrawCircleFilled は塗りつぶし円を描画する
func DrawCircleFilled(renderer *sdl.Renderer, cx, cy, r float64, color RGBA) {
	renderer.SetDrawColor(color.R, color.G, color.B, color.A)
	ri := int32(r)
	cxi := int32(cx)
	cyi := int32(cy)
	for dy := -ri; dy <= ri; dy++ {
		dx := int32(math.Sqrt(float64(ri*ri - dy*dy)))
		renderer.DrawLine(cxi-dx, cyi+dy, cxi+dx, cyi+dy)
	}
}
