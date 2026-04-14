package sdlui

import (
	"image/color"
	"math"

	"github.com/tdewolff/canvas"
)

// インジケーターアイコン（ブラウザ版 indicators.js の SVG パスそのまま）

const (
	svgIconLeaf   = "M0 -12C-5 -4 -7 2 -7 7c0 3 3 6 7 6s7-3 7-6c0-5-2-11-7-19z"
	svgIconThermo = "M12 2C10.34 2 9 3.34 9 5v8.59c-1.22.73-2 2.05-2 3.41 0 2.76 2.24 5 5 5s5-2.24 5-5c0-1.36-.78-2.68-2-3.41V5c0-1.66-1.34-3-3-3zm0 2c.55 0 1 .45 1 1v9.13l.5.29C14.46 15 15 15.96 15 17c0 1.65-1.35 3-3 3s-3-1.35-3-3c0-1.04.54-2 1.5-2.58l.5-.29V5c0-.55.45-1 1-1z"
	svgIconRoad   = "M11 2h2v4h-2zm0 6h2v4h-2zm0 6h2v4h-2zM2 2l4 20h2L5 2zm20 0h-2L16 22h2z"
	svgIconOil    = "M12 2C12 2 6 10 6 15a6 6 0 0 0 12 0c0-5-6-13-6-13zm0 17a3 3 0 0 1-3-3c0-.5.1-1 .3-1.5.2-.4.8-.3.9.2.1.3.1.6.1.9a1.8 1.8 0 0 0 1.8 1.8c.4 0 .7-.3.6-.7-.3-1.5-1.2-2.8-2.2-3.9-.3-.3 0-.8.4-.6C13.3 12.5 15 14.5 15 16a3 3 0 0 1-3 3z"
)

// drawSVGIconFillAt は SVG パスを塗りつぶしで描画（createIconPath 相当、グロー付き）
func drawSVGIconFillAt(ctx *canvas.Context, cxs, cys, size float64, svgPath string, col color.RGBA) {
	p, err := canvas.ParseSVGPath(svgPath)
	if err != nil {
		return
	}
	scale := size / 24.0
	// SVG 座標（Y-down）→ canvas Y-up: Y 反転スケール
	p = p.Transform(canvas.Identity.Scale(scale, -scale))
	cxu, cyu := screenToUp(cxs, cys)
	// (12,12) を中心に持ってくる
	offsetX := -12 * scale
	offsetY := 12 * scale
	p = p.Translate(cxu+offsetX, cyu+offsetY)

	// グロー
	glows := []struct {
		width float64
		alpha uint8
	}{
		{8, 8},
		{5, 20},
		{3, 45},
	}
	for _, l := range glows {
		ctx.Push()
		ctx.SetFillColor(canvas.Transparent)
		ctx.SetStrokeColor(WithAlpha(col, l.alpha))
		ctx.SetStrokeWidth(l.width)
		ctx.SetStrokeJoiner(canvas.RoundJoin)
		ctx.SetStrokeCapper(canvas.RoundCap)
		ctx.DrawPath(0, 0, p)
		ctx.Pop()
	}

	// 本体
	ctx.Push()
	ctx.SetFillColor(col)
	ctx.SetStrokeColor(canvas.Transparent)
	ctx.DrawPath(0, 0, p)
	ctx.Pop()
}

// drawSVGIconStrokeAt は SVG パスをアウトライン描画（グロー付き）
func drawSVGIconStrokeAt(ctx *canvas.Context, cxs, cys, size, rotDeg float64, svgPath string, strokeW float64, col color.RGBA) {
	p, err := canvas.ParseSVGPath(svgPath)
	if err != nil {
		return
	}
	scale := size / 20.0
	p = p.Transform(canvas.Identity.Scale(scale, -scale))
	if rotDeg != 0 {
		p = p.Transform(canvas.Identity.Rotate(-rotDeg))
	}
	cxu, cyu := screenToUp(cxs, cys)
	p = p.Translate(cxu, cyu)

	glows := []struct {
		widthAdd float64
		alpha    uint8
	}{
		{6, 8},
		{4, 18},
		{2, 40},
	}
	for _, l := range glows {
		ctx.Push()
		ctx.SetFillColor(canvas.Transparent)
		ctx.SetStrokeColor(WithAlpha(col, l.alpha))
		ctx.SetStrokeWidth(strokeW + l.widthAdd)
		ctx.SetStrokeCapper(canvas.RoundCap)
		ctx.SetStrokeJoiner(canvas.RoundJoin)
		ctx.DrawPath(0, 0, p)
		ctx.Pop()
	}

	ctx.Push()
	ctx.SetFillColor(canvas.Transparent)
	ctx.SetStrokeColor(col)
	ctx.SetStrokeWidth(strokeW)
	ctx.DrawPath(0, 0, p)
	ctx.Pop()
}

// drawLeafIconAt は葉アイコン（アウトライン + 葉脈 + 茎、グロー付き）
func drawLeafIconAt(ctx *canvas.Context, cxs, cys, size float64, col color.RGBA) {
	scale := size / 20.0
	cos60 := math.Cos(60 * math.Pi / 180)
	sin60 := math.Sin(60 * math.Pi / 180)

	tr := func(x, y float64) (float64, float64) {
		sx, sy := x*scale, y*scale
		rx := sx*cos60 - sy*sin60
		ry := sx*sin60 + sy*cos60
		return cxs + rx, cys + ry
	}

	// アウトライン
	drawSVGIconStrokeAt(ctx, cxs, cys, size, 60, svgIconLeaf, 3.0, col)

	// 葉脈（均等な細い破線 + グロー）
	vx1, vy1 := tr(0, -8)
	vx2, vy2 := tr(0, 10)
	lineLen := math.Sqrt((vx2-vx1)*(vx2-vx1) + (vy2-vy1)*(vy2-vy1))
	nCycles := int(math.Round(lineLen / 4.0))
	if nCycles < 4 {
		nCycles = 4
	}
	cycle := lineLen / float64(nCycles)
	dash := cycle * 0.55
	gap := cycle - dash
	veinGlows := []struct {
		width float64
		alpha uint8
	}{
		{6, 8},
		{4, 18},
		{3, 40},
	}
	for _, l := range veinGlows {
		drawDashedLineAt(ctx, vx1, vy1, vx2, vy2, l.width, WithAlpha(col, l.alpha), []float64{dash, gap})
	}
	drawDashedLineAt(ctx, vx1, vy1, vx2, vy2, 2.5, col, []float64{dash, gap})

	// 茎（グロー付き）
	sx1, sy1 := tr(0, 13)
	sx2, sy2 := tr(0, 18)
	stemGlows := []struct {
		width float64
		alpha uint8
	}{
		{7, 8},
		{5, 18},
		{3.5, 40},
	}
	for _, l := range stemGlows {
		drawLineAt(ctx, sx1, sy1, sx2, sy2, l.width, WithAlpha(col, l.alpha))
	}
	drawLineAt(ctx, sx1, sy1, sx2, sy2, 3.0, col)
}

// drawThermoIconAt は温度計アイコン
func drawThermoIconAt(ctx *canvas.Context, cxs, cys, size float64, col color.RGBA) {
	drawSVGIconFillAt(ctx, cxs, cys, size, svgIconThermo, col)
}

// drawRoadIconAt は道路アイコン
func drawRoadIconAt(ctx *canvas.Context, cxs, cys, size float64, col color.RGBA) {
	drawSVGIconFillAt(ctx, cxs, cys, size, svgIconRoad, col)
}

// drawDropletIconAt は雫アイコン
func drawDropletIconAt(ctx *canvas.Context, cxs, cys, size float64, col color.RGBA) {
	drawSVGIconFillAt(ctx, cxs, cys, size, svgIconOil, col)
}
