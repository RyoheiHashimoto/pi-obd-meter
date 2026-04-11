package sdlui

import (
	"image/color"
	"math"

	"github.com/tdewolff/canvas"
)

// canvas 用の描画プリミティブ群
// 全ての関数は「画面座標（screen coords, Y-down）」を受け取り、
// 内部で Y-up canvas 座標に変換する（screenH 基準）。
// 要素別レンダリング時は呼び出し側で ctx.Translate によりローカル座標に補正する。

const (
	canvasScreenW = 800.0
	canvasScreenH = 480.0

	// 座標定数（ブラウザ版 gauge.js と同一）
	cxScreen = 275.0
	cyScreen = 275.0
	gaugeR   = 230.0
	maxSpdW  = 180.0

	arcStart = -135.0
	arcEnd   = 135.0
	arcSweep = 270.0

	trackWidth   = 16.0
	needleWidth  = 6.0
	needleGap    = 24.0
	centerDotR   = 8.0
	tickMajorLen = 30.0
	tickMinorLen = 24.0
	tickOuterGap = 4.0
	labelOffset  = 54.0

	rpmROffset  = 24.0
	rpmArcWidth = 12.0
	rpmMaxVal   = 8000.0

	thrROffset = 84.0
	thrArcW    = 10.0

	gearBoxW = 64.0
	gearBoxH = 62.0

	// 右パネル
	panelOffsetX = 530.0
	mapCX        = 130.0
	mapCY        = 162.0
	mapR         = 125.0
	mapArcW      = 9.0 // 10→9 少し細く

	indXIcon   = 22.0
	indXVal    = 130.0
	indXUnit   = 236.0
	indYStart  = 312.0
	indSpacing = 49.0

	vacMin = -1.0
	vacMax = 0.0

	ecoGradMax        = 15.0
	coolantColdMax    = 60.0
	coolantNormalMax  = 100.0
	coolantWarningMax = 104.0
)

const pxToPt = 72.0 / 25.4

// 色定数
var (
	colTrack     = Hex("#181820")
	colTickMajor = Hex("#aaaaaa")
	colTickMinor = Hex("#444444")
	colTickLabel = Hex("#ffffff")
	colCenterDot = Hex("#1a1a22")
	colCenterRim = Hex("#444444")
	colRedzone   = Hex("#3d0000")
	colThrTrack  = Hex("#0a0a0f")
	colRPMTrack  = Hex("#1a1a24")
	colWhite     = Hex("#ffffff")
	colDim       = Hex("#333333")
	colHoldYel   = Hex("#fdd835")
	colGreen     = Hex("#69f0ae")
	colOrange    = Hex("#ff9800")
)

// polarToScreen は極座標→画面座標変換（12時=0°, CW+）
func polarToScreen(cxs, cys, r, deg float64) (float64, float64) {
	rad := deg * math.Pi / 180
	return cxs + r*math.Sin(rad), cys - r*math.Cos(rad)
}

// screenToUp は画面 Y-down 座標を canvas Y-up 座標に変換
func screenToUp(cxs, cys float64) (float64, float64) {
	return cxs, canvasScreenH - cys
}

// drawArcAt は任意中心でアークを描画（polyline ベース、CartesianI Y-up）
func drawArcAt(ctx *canvas.Context, cxs, cys, radius, strokeW float64, startDeg, endDeg float64, col color.RGBA) {
	if endDeg <= startDeg {
		return
	}
	cxu, cyu := screenToUp(cxs, cys)

	p := &canvas.Path{}
	steps := int(math.Ceil((endDeg - startDeg) * 1.5))
	if steps < 4 {
		steps = 4
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		deg := startDeg + t*(endDeg-startDeg)
		rad := deg * math.Pi / 180
		x := cxu + radius*math.Sin(rad)
		y := cyu + radius*math.Cos(rad)
		if i == 0 {
			p.MoveTo(x, y)
		} else {
			p.LineTo(x, y)
		}
	}

	ctx.Push()
	ctx.SetFillColor(canvas.Transparent)
	ctx.SetStrokeColor(col)
	ctx.SetStrokeWidth(strokeW)
	ctx.SetStrokeCapper(canvas.RoundCap)
	ctx.SetStrokeJoiner(canvas.RoundJoin)
	ctx.DrawPath(0, 0, p)
	ctx.Pop()
}

// drawLineAt は画面座標で太線を描画
func drawLineAt(ctx *canvas.Context, x1, y1, x2, y2, width float64, col color.RGBA) {
	x1u, y1u := screenToUp(x1, y1)
	x2u, y2u := screenToUp(x2, y2)
	p := &canvas.Path{}
	p.MoveTo(x1u, y1u)
	p.LineTo(x2u, y2u)

	ctx.Push()
	ctx.SetFillColor(canvas.Transparent)
	ctx.SetStrokeColor(col)
	ctx.SetStrokeWidth(width)
	ctx.SetStrokeCapper(canvas.RoundCap)
	ctx.DrawPath(0, 0, p)
	ctx.Pop()
}

// drawDashedLineAt は画面座標で破線を描画
func drawDashedLineAt(ctx *canvas.Context, x1, y1, x2, y2, width float64, col color.RGBA, dashes []float64) {
	x1u, y1u := screenToUp(x1, y1)
	x2u, y2u := screenToUp(x2, y2)
	p := &canvas.Path{}
	p.MoveTo(x1u, y1u)
	p.LineTo(x2u, y2u)

	ctx.Push()
	ctx.SetFillColor(canvas.Transparent)
	ctx.SetStrokeColor(col)
	ctx.SetStrokeWidth(width)
	ctx.SetStrokeCapper(canvas.ButtCap)
	ctx.SetDashes(0, dashes...)
	ctx.DrawPath(0, 0, p)
	ctx.Pop()
}

// drawCircleAt は画面座標で塗りつぶし円を描画
func drawCircleAt(ctx *canvas.Context, cxs, cys, radius float64, col color.RGBA) {
	cxu, cyu := screenToUp(cxs, cys)
	circle := canvas.Circle(radius)
	ctx.Push()
	ctx.SetFillColor(col)
	ctx.SetStrokeColor(canvas.Transparent)
	ctx.DrawPath(cxu, cyu, circle)
	ctx.Pop()
}

// drawRoundedRectAt は角丸矩形の枠線（画面座標）
func drawRoundedRectAt(ctx *canvas.Context, sx, sy, w, h, rx, strokeW float64, col color.RGBA) {
	yUp := canvasScreenH - sy - h
	p := canvas.RoundedRectangle(w, h, rx)

	ctx.Push()
	ctx.SetFillColor(canvas.Transparent)
	ctx.SetStrokeColor(col)
	ctx.SetStrokeWidth(strokeW)
	ctx.SetStrokeJoiner(canvas.RoundJoin)
	ctx.DrawPath(sx, yUp, p)
	ctx.Pop()
}

// drawGlowArcAt はグロー付きアーク（速度計・RPM 用、しっかりした光）
func drawGlowArcAt(ctx *canvas.Context, cxs, cys, radius, mainW float64, startDeg, endDeg float64, col color.RGBA) {
	glows := []struct {
		widthAdd float64
		alpha    uint8
	}{
		{18, 10},
		{13, 22},
		{9, 40},
		{5, 75},
	}
	for _, l := range glows {
		drawArcAt(ctx, cxs, cys, radius, mainW+l.widthAdd, startDeg, endDeg, WithAlpha(col, l.alpha))
	}
	drawArcAt(ctx, cxs, cys, radius, mainW, startDeg, endDeg, col)
}

// drawGlowArcSubtleAt はグロー付きアーク（バキューム計用、控えめ）
func drawGlowArcSubtleAt(ctx *canvas.Context, cxs, cys, radius, mainW float64, startDeg, endDeg float64, col color.RGBA) {
	glows := []struct {
		widthAdd float64
		alpha    uint8
	}{
		{14, 8},
		{10, 18},
		{6, 35},
		{3, 65},
	}
	for _, l := range glows {
		drawArcAt(ctx, cxs, cys, radius, mainW+l.widthAdd, startDeg, endDeg, WithAlpha(col, l.alpha))
	}
	drawArcAt(ctx, cxs, cys, radius, mainW, startDeg, endDeg, col)
}

// drawGlowLineAt はグロー付き直線（速度計針）
func drawGlowLineAt(ctx *canvas.Context, x1, y1, x2, y2, mainW float64, col color.RGBA) {
	glows := []struct {
		widthAdd float64
		alpha    uint8
	}{
		{20, 10},
		{14, 22},
		{10, 40},
		{6, 75},
	}
	for _, l := range glows {
		drawLineAt(ctx, x1, y1, x2, y2, mainW+l.widthAdd, WithAlpha(col, l.alpha))
	}
	drawLineAt(ctx, x1, y1, x2, y2, mainW, col)
}

// drawScreenBackgroundGradient は 2 つのゲージ中心を起点にした radial gradient を描く
// 各メーターが「スポットライトで照らされている」ような奥行き感
func drawScreenBackgroundGradient(ctx *canvas.Context) {
	// まず全面黒
	ctx.Push()
	ctx.Style.Fill = canvas.Paint{Color: color.RGBA{0, 0, 0, 255}}
	ctx.Style.Stroke = canvas.Paint{Color: canvas.Transparent}
	ctx.DrawPath(0, 0, canvas.Rectangle(canvasScreenW, canvasScreenH))
	ctx.Pop()

	// --- 速度計中心のグラデ ---
	// 画面座標 (cxScreen, cyScreen) を canvas Y-up に
	cxu1 := cxScreen
	cyu1 := canvasScreenH - cyScreen
	grad1 := canvas.NewGradient()
	grad1.Add(0, Hex("#2c2c42"))
	grad1.Add(0.5, Hex("#10101a"))
	grad1.Add(1, color.RGBA{0, 0, 0, 0})
	radGrad1 := grad1.ToRadial(canvas.Point{X: cxu1, Y: cyu1}, 0, canvas.Point{X: cxu1, Y: cyu1}, 380)

	ctx.Push()
	ctx.Style.Fill = canvas.Paint{Gradient: radGrad1}
	ctx.Style.Stroke = canvas.Paint{Color: canvas.Transparent}
	ctx.DrawPath(0, 0, canvas.Rectangle(canvasScreenW, canvasScreenH))
	ctx.Pop()

	// --- バキューム計中心のグラデ ---
	cxu2 := panelOffsetX + mapCX
	cyu2 := canvasScreenH - mapCY
	grad2 := canvas.NewGradient()
	grad2.Add(0, Hex("#2c2c42"))
	grad2.Add(0.5, Hex("#10101a"))
	grad2.Add(1, color.RGBA{0, 0, 0, 0})
	radGrad2 := grad2.ToRadial(canvas.Point{X: cxu2, Y: cyu2}, 0, canvas.Point{X: cxu2, Y: cyu2}, 220)

	ctx.Push()
	ctx.Style.Fill = canvas.Paint{Gradient: radGrad2}
	ctx.Style.Stroke = canvas.Paint{Color: canvas.Transparent}
	ctx.DrawPath(0, 0, canvas.Rectangle(canvasScreenW, canvasScreenH))
	ctx.Pop()
}

// drawGradientTrackAt は半径方向の radial gradient でアークトラックを描画
// 内側から外側へのグラデで立体感（凹/凸）を出す
func drawGradientTrackAt(ctx *canvas.Context, cxs, cys, radius, strokeW float64, startDeg, endDeg float64, innerCol, midCol, outerCol color.RGBA) {
	if endDeg <= startDeg {
		return
	}
	cxu, cyu := screenToUp(cxs, cys)

	p := &canvas.Path{}
	steps := int(math.Ceil((endDeg - startDeg) * 1.5))
	if steps < 4 {
		steps = 4
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		deg := startDeg + t*(endDeg-startDeg)
		rad := deg * math.Pi / 180
		x := cxu + radius*math.Sin(rad)
		y := cyu + radius*math.Cos(rad)
		if i == 0 {
			p.MoveTo(x, y)
		} else {
			p.LineTo(x, y)
		}
	}

	// 3-stop radial gradient
	grad := canvas.NewGradient()
	grad.Add(0, innerCol)
	grad.Add(0.5, midCol)
	grad.Add(1, outerCol)
	innerR := radius - strokeW/2
	outerR := radius + strokeW/2
	radGrad := grad.ToRadial(canvas.Point{X: cxu, Y: cyu}, innerR, canvas.Point{X: cxu, Y: cyu}, outerR)

	ctx.Push()
	ctx.Style.Fill = canvas.Paint{Color: canvas.Transparent}
	ctx.Style.Stroke = canvas.Paint{Gradient: radGrad}
	ctx.SetStrokeWidth(strokeW)
	ctx.SetStrokeCapper(canvas.RoundCap)
	ctx.SetStrokeJoiner(canvas.RoundJoin)
	ctx.DrawPath(0, 0, p)
	ctx.Pop()
}

// drawGlowLineSubtleAt はグロー付き直線（バキューム計針）
func drawGlowLineSubtleAt(ctx *canvas.Context, x1, y1, x2, y2, mainW float64, col color.RGBA) {
	glows := []struct {
		widthAdd float64
		alpha    uint8
	}{
		{14, 8},
		{10, 18},
		{6, 35},
		{3, 65},
	}
	for _, l := range glows {
		drawLineAt(ctx, x1, y1, x2, y2, mainW+l.widthAdd, WithAlpha(col, l.alpha))
	}
	drawLineAt(ctx, x1, y1, x2, y2, mainW, col)
}
