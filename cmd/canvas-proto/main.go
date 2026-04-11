// canvas-proto: tdewolff/canvas で速度ゲージを描画し PNG 出力するプロトタイプ。
// Y-up の CartesianI（canvas デフォルト）で座標計算を行い、ブラウザ版の見た目を再現する。
//
// Usage: go run ./cmd/canvas-proto/
// Output: canvas-proto.png (800x480)
package main

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"strings"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers"
)

var (
	fontOrbitron *canvas.FontFamily
	fontShareTech *canvas.FontFamily
)

func loadFonts() error {
	fontOrbitron = canvas.NewFontFamily("Orbitron")
	if err := fontOrbitron.LoadFontFile("web/static/fonts/Orbitron-Black.ttf", canvas.FontBlack); err != nil {
		return fmt.Errorf("Orbitron読み込み失敗: %w", err)
	}
	fontShareTech = canvas.NewFontFamily("ShareTechMono")
	if err := fontShareTech.LoadFontFile("web/static/fonts/ShareTechMono-Regular.ttf", canvas.FontRegular); err != nil {
		return fmt.Errorf("ShareTechMono読み込み失敗: %w", err)
	}
	return nil
}

// pxPt は pixel サイズを point サイズに変換（canvas の Face は pt 単位、DPMM=1 で 1mm=1px）
const pxToPt = 72.0 / 25.4

// faceFor は適切な FontStyle でフォントを取得する
func faceFor(fam *canvas.FontFamily, sizePx float64, col color.RGBA) *canvas.FontFace {
	if fam == fontOrbitron {
		return fam.Face(sizePx*pxToPt, col, canvas.FontBlack)
	}
	// ShareTechMono はブラウザ版も font-weight: 700 (faux bold) で表示
	return fam.Face(sizePx*pxToPt, col, canvas.FontBold)
}

// drawTextCentered は text-anchor:middle + dominant-baseline:middle 相当
func drawTextCentered(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string) {
	face := faceFor(fam, sizePx, col)
	txt := canvas.NewTextLine(face, text, canvas.Center)
	yUp := screenH - screenY
	metrics := face.Metrics()
	yBaseline := yUp - (metrics.Ascent-metrics.Descent)/2
	ctx.DrawText(x, yBaseline, txt)
}

// drawTextBaseline は text-anchor:middle 相当（ベースラインを y に配置）
func drawTextBaseline(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string) {
	face := faceFor(fam, sizePx, col)
	txt := canvas.NewTextLine(face, text, canvas.Center)
	yUp := screenH - screenY
	ctx.DrawText(x, yUp, txt)
}

// drawGlowTextBaseline はグロー付きテキスト（ベースライン基準）
// ブラウザの drop-shadow(0 0 6px color) を近似
// オフセットを複数方向に振って半透明で重ね描き → ガウシアン blur 風
func drawGlowTextBaseline(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string) {
	// グローは色を維持、オフセット半径別に alpha を変える
	// 8方向 × 3段 = 24 サンプル
	drawTextOffsets(ctx, fam, sizePx, col, x, screenY, text, canvas.Center, false)
	// 本体
	drawTextBaseline(ctx, fam, sizePx, col, x, screenY, text)
}

// drawGlowTextCentered はグロー付きテキスト（視覚中央基準）
func drawGlowTextCentered(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string) {
	drawTextOffsets(ctx, fam, sizePx, col, x, screenY, text, canvas.Center, true)
	drawTextCentered(ctx, fam, sizePx, col, x, screenY, text)
}

// drawTextOffsets は多方向オフセットで半透明テキストを描画してグロー風にする（控えめ）
func drawTextOffsets(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string, halign canvas.TextAlign, centered bool) {
	dirs := []struct{ dx, dy float64 }{
		{0, -1}, {0.707, -0.707}, {1, 0}, {0.707, 0.707},
		{0, 1}, {-0.707, 0.707}, {-1, 0}, {-0.707, -0.707},
	}
	// 2層、控えめな alpha
	layers := []struct {
		dist  float64
		alpha uint8
	}{
		{4, 10},
		{2.2, 25},
	}
	for _, l := range layers {
		c := withAlpha(col, l.alpha)
		for _, d := range dirs {
			ox := x + d.dx*l.dist
			oy := screenY + d.dy*l.dist
			if centered {
				drawTextCentered(ctx, fam, sizePx, c, ox, oy, text)
			} else {
				drawTextBaselineNoGlow(ctx, fam, sizePx, c, ox, oy, text, halign)
			}
		}
	}
}

// drawTextBaselineNoGlow は内部用（glow layers から呼ばれる）
func drawTextBaselineNoGlow(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string, halign canvas.TextAlign) {
	face := faceFor(fam, sizePx, col)
	txt := canvas.NewTextLine(face, text, halign)
	yUp := screenH - screenY
	ctx.DrawText(x, yUp, txt)
}

// drawTextRight は text-anchor:end 相当（ベースライン、右端基準）
func drawTextRight(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, rx, screenY float64, text string) {
	face := faceFor(fam, sizePx, col)
	txt := canvas.NewTextLine(face, text, canvas.Right)
	yUp := screenH - screenY
	ctx.DrawText(rx, yUp, txt)
}

// --- ブラウザ版と同一の定数 (gauge.js) ---
const (
	screenW = 800.0
	screenH = 480.0

	// ブラウザ版の画面座標（Y-down）
	cxScreen = 275.0
	cyScreen = 275.0 // 268 → 275 (全体下へ)
	r        = 230.0
	maxSpeed = 180.0

	arcStart = -135.0 // gauge degrees (12時=0°, CW+)
	arcEnd   = 135.0
	arcSweep = 270.0

	trackWidth   = 16.0
	needleWidth  = 6.0
	needleGap    = 24.0
	centerDotR   = 8.0
	tickMajorLen = 30.0
	tickMinorLen = 24.0 // 18 → 24（長く）
	tickOuterGap = 4.0

	rpmROffset  = 24.0
	rpmArcWidth = 12.0
	rpmMax      = 8000.0

	thrROffset = 84.0
	thrArcW    = 10.0
)

// Y-up に変換した座標（canvas の CartesianI 用）
var cxUp = cxScreen
var cyUp = screenH - cyScreen

// --- 色定数 ---
var (
	colTrack     = hexColor("#181820")
	colTickMajor = hexColor("#aaaaaa")
	colTickMinor = hexColor("#444444")
	colCenterDot = hexColor("#1a1a22")
	colCenterRim = hexColor("#444444")
	colRedzone   = hexColor("#3d0000")
	colThrTrack  = hexColor("#0a0a0f")
	colRPMTrack  = hexColor("#1a1a24")
)

func main() {
	if err := loadFonts(); err != nil {
		fmt.Fprintf(os.Stderr, "フォント読み込みエラー: %v\n", err)
		os.Exit(1)
	}

	// Canvas 800x480、1mm = 1pixel (DPMM=1)
	c := canvas.New(screenW, screenH)
	ctx := canvas.NewContext(c)
	// CartesianI (デフォルト、Y-up、原点=左下)

	// 黒背景
	ctx.SetFillColor(canvas.Black)
	ctx.DrawPath(0, 0, canvas.Rectangle(screenW, screenH))

	// === 速度ゲージ ===

	// RPM トラック（最外周）
	rpmR := r + rpmROffset
	drawArc(ctx, rpmR, rpmArcWidth, arcStart, arcEnd, colRPMTrack)

	// RPM レッドゾーン背景 (6500-8000)
	redStart := arcStart + (6500.0/rpmMax)*arcSweep
	drawArc(ctx, rpmR, rpmArcWidth, redStart, arcEnd, colRedzone)

	// 速度トラック
	drawArc(ctx, r, trackWidth, arcStart, arcEnd, colTrack)

	// 速度目盛り
	majorInterval := 20.0
	labelOffset := 54.0
	for spd := 0.0; spd <= maxSpeed; spd += majorInterval {
		angle := arcStart + (spd/maxSpeed)*arcSweep
		outerR := r + tickOuterGap
		innerR := outerR - tickMajorLen
		ox, oy := polarUp(outerR, angle)
		ix, iy := polarUp(innerR, angle)
		drawLine(ctx, ix, iy, ox, oy, 5, colTickMajor)

		// 目盛り数値ラベル
		labelRad := r - labelOffset
		lx, ly := polarUp(labelRad, angle)
		lyScreen := screenH - ly
		drawTextCentered(ctx, fontShareTech, 28, hexColor("#ffffff"), lx, lyScreen, fmt.Sprintf("%d", int(spd)))

		// 副目盛り（ブラウザ版: 主目盛りあたり4本＝5等分）
		if spd < maxSpeed {
			for j := 1; j < 5; j++ {
				minSpd := spd + majorInterval*float64(j)/5.0
				if minSpd > maxSpeed {
					break
				}
				minAngle := arcStart + (minSpd/maxSpeed)*arcSweep
				mx, my := polarUp(outerR, minAngle)
				mix, miy := polarUp(outerR-tickMinorLen, minAngle)
				drawLine(ctx, mix, miy, mx, my, 2.5, colTickMinor)
			}
		}
	}

	// スロットルトラック
	thrR := r - thrROffset
	drawArc(ctx, thrR, thrArcW, arcStart, arcEnd, colThrTrack)

	// --- デモ: ブラウザ版と同じ値で比較 ---
	demoSpeed := 63.0
	demoThrPct := 18.0 // デモ用スロットル開度
	spdColor := speedColor(demoSpeed)
	spdPct := demoSpeed / maxSpeed
	spdEnd := arcStart + spdPct*arcSweep

	// スロットルアーク（単色、ブラウザ版と同じく現在の pct に基づく HSL）
	if demoThrPct > 0.5 {
		thrEnd := arcStart + (demoThrPct/100)*arcSweep
		thrCol := throttleColor(demoThrPct, true)
		drawGlowArc(ctx, thrR, thrArcW, arcStart, thrEnd, thrCol)
	}

	// 速度アーク（高品質グロー）
	drawGlowArc(ctx, r, trackWidth, arcStart, spdEnd, spdColor)

	// --- ギア/レンジ枠 ---
	demoRange := "D"
	demoGear := 3
	demoHold := false
	demoTCLock := true
	gearBoxW := 64.0
	gearBoxH := 62.0

	rangeX := cxScreen - r - 4 // 10 → 4（少し右へ）
	gearX := cxScreen + r + 2
	rangeY := 59.0 // 52 → 59（少し下へ）
	boxY := rangeY - gearBoxH + 14

	gearCol := gearColor(demoRange, demoHold)

	// レンジ枠 (左上、文字とボックスにグロー)
	drawRoundedRect(ctx, rangeX-gearBoxW/2, boxY, gearBoxW, gearBoxH, 8, 3, gearCol)
	drawGlowTextBaseline(ctx, fontOrbitron, 52, gearCol, rangeX, rangeY, demoRange)

	// ギア番号 (右上、文字とボックスにグロー)
	drawRoundedRect(ctx, gearX-gearBoxW/2, boxY, gearBoxW, gearBoxH, 8, 3, gearCol)
	gearText := "-"
	if demoRange == "P" || demoRange == "N" || demoRange == "R" {
		gearText = "--"
	} else if demoGear >= 1 && demoGear <= 4 {
		gearText = fmt.Sprintf("%d", demoGear)
	}
	drawGlowTextBaseline(ctx, fontOrbitron, 52, gearCol, gearX, rangeY, gearText)

	// HOLD ラベル（active 時のみグロー）
	if demoHold {
		drawGlowTextBaseline(ctx, fontShareTech, 24, hexColor("#fdd835"), rangeX, rangeY+gearBoxH-22, "HOLD")
	} else {
		drawTextBaseline(ctx, fontShareTech, 24, hexColor("#333333"), rangeX, rangeY+gearBoxH-22, "HOLD")
	}

	// LOCK ラベル（active 時のみグロー）
	if demoTCLock {
		drawGlowTextBaseline(ctx, fontShareTech, 24, hexColor("#69f0ae"), gearX, rangeY+gearBoxH-22, "LOCK")
	} else {
		drawTextBaseline(ctx, fontShareTech, 24, hexColor("#333333"), gearX, rangeY+gearBoxH-22, "LOCK")
	}

	// --- 速度数値（下に） ---
	numYScreen := cyScreen + r*0.42
	drawTextBaseline(ctx, fontOrbitron, 84, spdColor, cxScreen, numYScreen, fmt.Sprintf("%d", int(demoSpeed)))

	// km/h ラベル
	unitYScreen := numYScreen + 84*0.45
	drawTextBaseline(ctx, fontShareTech, 28, hexColor("#ffffff"), cxScreen, unitYScreen, "km/h")

	// THROTTLE ラベル（アクティブ時はグロー付き）
	thrLabelCol := throttleColor(demoThrPct, demoThrPct > 0.5)
	if demoThrPct >= 5 {
		drawGlowTextBaseline(ctx, fontShareTech, 24, thrLabelCol, cxScreen, unitYScreen+50, "THROTTLE")
	} else {
		drawTextBaseline(ctx, fontShareTech, 24, hexColor("#333333"), cxScreen, unitYScreen+50, "THROTTLE")
	}

	// --- デモ: RPM 3,007 (高品質グロー) ---
	demoRPM := 3007.0
	rpmColor := rpmColorFn(demoRPM)
	rpmPct := demoRPM / rpmMax
	rpmEnd := arcStart + rpmPct*arcSweep
	drawGlowArc(ctx, rpmR, rpmArcWidth, arcStart, rpmEnd, rpmColor)

	// RPM 数値（ベースライン基準、カンマ区切り）
	throttleR := r - thrROffset
	rpmReadYScreen := cyScreen - throttleR/2 + 5
	drawTextBaseline(ctx, fontOrbitron, 48, rpmColor, cxScreen, rpmReadYScreen, formatComma(int(demoRPM)))
	drawTextBaseline(ctx, fontShareTech, 24, hexColor("#ffffff"), cxScreen, rpmReadYScreen+34, "r/min")

	// --- 針（数字の前に描画＝最後に描く）---
	needleAngle := spdEnd
	tipR := r - needleGap
	nx1, ny1 := polarUp(-16, needleAngle)
	nx2, ny2 := polarUp(tipR, needleAngle)
	drawGlowLine(ctx, nx1, ny1, nx2, ny2, needleWidth, spdColor)

	// 中心ドット（針の上、グレーの縁を広く、全体少し小さめ）
	drawCircle(ctx, cxUp, cyUp, centerDotR+3, colCenterRim)
	drawCircle(ctx, cxUp, cyUp, centerDotR-1, colCenterDot)

	// === 右パネル: バキューム計 + インジケーター ===
	drawRightPanel(ctx)

	// --- 出力 ---
	outPath := "canvas-proto.png"
	if err := renderers.Write(outPath, c, canvas.DPMM(1)); err != nil {
		fmt.Fprintf(os.Stderr, "書き出しエラー: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("出力: %s (800x480)\n", outPath)
}

// === 座標変換 ===

// polarUp は gauge 角度 → canvas CartesianI (Y-up) 座標に変換
// gauge: 12時=0°, 時計回り正
// Y-up: cy_up は画面座標から反転済み
func polarUp(radius, gaugeDeg float64) (float64, float64) {
	rad := gaugeDeg * math.Pi / 180
	return cxUp + radius*math.Sin(rad), cyUp + radius*math.Cos(rad)
}

// === 描画ヘルパー ===

// drawArc は速度ゲージ中心 (cxScreen, cyScreen) でアークを描画する
func drawArc(ctx *canvas.Context, radius, strokeW float64, startGauge, endGauge float64, col color.RGBA) {
	drawArcAt(ctx, cxScreen, cyScreen, radius, strokeW, startGauge, endGauge, col)
}

// drawGlowArc は複数層でスムーズなグロー効果付きアークを描画する
// ブラウザの drop-shadow(0 0 6px color) を近似
func drawGlowArc(ctx *canvas.Context, radius, mainW float64, startGauge, endGauge float64, col color.RGBA) {
	drawGlowArcAt(ctx, cxScreen, cyScreen, radius, mainW, startGauge, endGauge, col)
}

// drawGradientArc はスロットルのような開度毎に色が変わるアークを描画する
// 1°刻みで色を変えながらセグメント描画
func drawGradientArc(ctx *canvas.Context, cxU, cyU, radius, strokeW float64, startGauge, endGauge float64, maxPct float64) {
	if endGauge <= startGauge {
		return
	}
	steps := int(math.Ceil(endGauge - startGauge))
	if steps < 1 {
		return
	}
	for i := 0; i < steps; i++ {
		d0 := startGauge + float64(i)
		d1 := d0 + 1
		if d1 > endGauge {
			d1 = endGauge
		}
		segPct := float64(i+1) / float64(steps) * maxPct
		col := throttleColor(segPct, true)
		// drawArcAt は screen 座標系、cxU/cyU は Y-up なので変換
		cxs := cxU
		cys := screenH - cyU
		drawArcAt(ctx, cxs, cys, radius, strokeW, d0, d1, col)
	}
}

// throttleColor はスロットル開度に応じた色を返す（HSL グラデーション、dimZone 対応）
func throttleColor(pct float64, active bool) color.RGBA {
	const dimZone = 5.0
	if !active {
		return hexColor("#333333")
	}
	hue := 210 - (pct/100)*210
	if pct < dimZone {
		dim := pct / dimZone
		lum := 15 + dim*40
		sat := dim * 100
		return hslColor(hue, sat, lum)
	}
	if hue < 5 {
		return hexColor("#f44336")
	}
	return hslColor(hue, 100, 55)
}

func drawGlowArcAt(ctx *canvas.Context, cxs, cys, radius, mainW float64, startGauge, endGauge float64, col color.RGBA) {
	// 外側のみグロー、本体は正確な太さ
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
		drawArcAt(ctx, cxs, cys, radius, mainW+l.widthAdd, startGauge, endGauge, withAlpha(col, l.alpha))
	}
	drawArcAt(ctx, cxs, cys, radius, mainW, startGauge, endGauge, col)
}

// drawGlowLine は速度計針用のしっかりしたグロー
func drawGlowLine(ctx *canvas.Context, x1, y1, x2, y2, mainW float64, col color.RGBA) {
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
		drawLine(ctx, x1, y1, x2, y2, mainW+l.widthAdd, withAlpha(col, l.alpha))
	}
	drawLine(ctx, x1, y1, x2, y2, mainW, col)
}

// drawGlowLineSubtle はバキューム計針用
func drawGlowLineSubtle(ctx *canvas.Context, x1, y1, x2, y2, mainW float64, col color.RGBA) {
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
		drawLine(ctx, x1, y1, x2, y2, mainW+l.widthAdd, withAlpha(col, l.alpha))
	}
	drawLine(ctx, x1, y1, x2, y2, mainW, col)
}

// drawGlowArcSubtleAt はバキューム計用（細い本体 + 強めのグロー）
func drawGlowArcSubtleAt(ctx *canvas.Context, cxs, cys, radius, mainW float64, startGauge, endGauge float64, col color.RGBA) {
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
		drawArcAt(ctx, cxs, cys, radius, mainW+l.widthAdd, startGauge, endGauge, withAlpha(col, l.alpha))
	}
	drawArcAt(ctx, cxs, cys, radius, mainW, startGauge, endGauge, col)
}

// drawArcAt は任意中心点 (screenX, screenY) でアークを描画する
func drawArcAt(ctx *canvas.Context, cxs, cys, radius, strokeW float64, startGauge, endGauge float64, col color.RGBA) {
	if endGauge <= startGauge {
		return
	}
	cxu := cxs
	cyu := screenH - cys

	p := &canvas.Path{}
	steps := int(math.Ceil((endGauge - startGauge) * 1.5))
	if steps < 4 {
		steps = 4
	}

	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		deg := startGauge + t*(endGauge-startGauge)
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

// drawLine は太線を描画する
func drawLine(ctx *canvas.Context, x1, y1, x2, y2 float64, width float64, col color.RGBA) {
	p := &canvas.Path{}
	p.MoveTo(x1, y1)
	p.LineTo(x2, y2)

	ctx.Push()
	ctx.SetFillColor(canvas.Transparent)
	ctx.SetStrokeColor(col)
	ctx.SetStrokeWidth(width)
	ctx.SetStrokeCapper(canvas.RoundCap)
	ctx.DrawPath(0, 0, p)
	ctx.Pop()
}

// drawCircle は塗りつぶし円を描画する
func drawCircle(ctx *canvas.Context, cx, cy, radius float64, col color.RGBA) {
	circle := canvas.Circle(radius)
	ctx.Push()
	ctx.SetFillColor(col)
	ctx.SetStrokeColor(canvas.Transparent)
	ctx.DrawPath(cx, cy, circle)
	ctx.Pop()
}

// drawRoundedRect は角丸矩形の枠線を描画する（screen座標、Y-down）
func drawRoundedRect(ctx *canvas.Context, screenX, screenY, w, h, rx, strokeW float64, col color.RGBA) {
	yUp := screenH - screenY - h
	p := canvas.RoundedRectangle(w, h, rx)
	ctx.Push()
	ctx.SetFillColor(canvas.Transparent)
	ctx.SetStrokeColor(col)
	ctx.SetStrokeWidth(strokeW)
	ctx.SetStrokeJoiner(canvas.RoundJoin)
	ctx.DrawPath(screenX, yUp, p)
	ctx.Pop()
}

// === 右パネル描画 ===

const (
	panelOffsetX = 530.0
	mapCX        = 130.0
	mapCY        = 162.0 // 155 → 162 (下へ)
	mapR         = 125.0
	mapArcW      = 10.0 // スロットルと同じ

	indXIcon   = 22.0
	indXVal    = 130.0 // mapCX と同じ (Bar 中心と揃える)
	indXUnit   = 236.0
	indYStart  = 312.0 // 305 → 312
	indSpacing = 49.0
)

// drawRightPanel はバキューム計 + 4行インジケーターを描画する
func drawRightPanel(ctx *canvas.Context) {
	// 右パネル座標は左上原点 (0,0) 基準、panelOffsetX を加えて screen 座標に
	cx := panelOffsetX + mapCX
	cy := mapCY

	// バキュームトラック
	drawArcAt(ctx, cx, cy, mapR, mapArcW, arcStart, arcEnd, colTrack)

	// バキューム目盛り (5 major, 4 minor between → total 20)
	vacMajor := 5
	vacMinor := 4
	vacTotal := vacMajor * vacMinor
	for i := 0; i <= vacTotal; i++ {
		angle := arcStart + (float64(i)/float64(vacTotal))*arcSweep
		isMj := i%vacMinor == 0
		outerR := mapR + 3.0
		innerR := outerR - 16.0 // 主目盛り（18 → 16 少し短く）
		if !isMj {
			innerR = outerR - 14.0 // 副目盛り
		}
		ox, oy := polarAtUp(cx, cy, outerR, angle)
		ix, iy := polarAtUp(cx, cy, innerR, angle)
		col := colTickMinor
		w := 2.0
		if isMj {
			col = colTickMajor
			w = 4.0
		}
		drawLine(ctx, ix, iy, ox, oy, w, col)

		if isMj {
			v := -1.0 + (float64(i)/float64(vacTotal))*1.0
			labelR := mapR - 32.0
			lx, lyUp := polarAtUp(cx, cy, labelR, angle)
			lyScreen := screenH - lyUp
			var label string
			if v == 0 {
				label = "0"
			} else {
				s := fmt.Sprintf("%.1f", v)
				label = strings.Replace(s, "-0.", "-.", 1)
			}
			drawTextCentered(ctx, fontShareTech, 22, colTickLabel, lx, lyScreen, label)
		}
	}

	// --- デモ: vacuum -0.68 bar (intake_map ≈ 33.3 kPa) ---
	demoBar := -0.68

	// バキュームアーク（控えめグロー）
	vacPct := math.Max(0, math.Min(100, (demoBar-(-1.0))/1.0*100))
	vacEnd := arcStart + (vacPct/100)*arcSweep
	vacHue := (1 - vacPct/100) * 210
	vacCol := hslColor(vacHue, 100, 55)
	drawGlowArcSubtleAt(ctx, cx, cy, mapR, mapArcW, arcStart, vacEnd, vacCol)

	// VACUUM ラベル (アクティブ時はグロー付き、針より先に描いて後ろに)
	lum := 10 + (vacPct/100)*45
	sat := math.Min(100, vacPct*1.5)
	vacLabelCol := hslColor(vacHue, sat, lum)
	if demoBar < -0.01 {
		drawGlowTextCentered(ctx, fontShareTech, 24, vacLabelCol, cx, cy-30, "VACUUM")
	} else {
		drawTextCentered(ctx, fontShareTech, 24, vacLabelCol, cx, cy-30, "VACUUM")
	}

	// バキューム針（少し細めの本体 + 控えめグロー）
	needleAngle := vacEnd
	vacNeedleW := 4.5
	nx1, ny1 := polarAtUp(cx, cy, -10, needleAngle)
	nx2, ny2 := polarAtUp(cx, cy, mapR-18, needleAngle)
	drawGlowLineSubtle(ctx, nx1, ny1, nx2, ny2, vacNeedleW, vacCol)

	// 中心ドット（針の上に描く、少し大きめ）
	drawCircle(ctx, cx, screenH-cy, 8, colCenterRim)
	drawCircle(ctx, cx, screenH-cy, 5, colCenterDot)

	// バキューム数値 (グロー付き)
	drawTextBaseline(ctx, fontOrbitron, 48, vacCol, cx, cy+mapR*0.38, fmt.Sprintf("%.2f", demoBar))
	drawTextBaseline(ctx, fontShareTech, 24, colTickLabel, cx, cy+mapR*0.38+44, "Bar")

	// 区切り線（画面座標で指定）
	drawLineScreen(ctx, panelOffsetX+40, indYStart-16, panelOffsetX+240, indYStart-16, 1, hexColor("#222222"))

	// === 4行インジケーター ===
	baseX := panelOffsetX

	// ECO 9.5 km/L
	ecoY := indYStart
	ecoCol := hslColor(math.Min(9.5/15, 1)*153, 100, 55)
	drawTextBaseline(ctx, fontOrbitron, 40, ecoCol, baseX+indXVal, ecoY+6, "9.5")
	drawTextRight(ctx, fontShareTech, 24, colTickLabel, baseX+indXUnit, ecoY+4, "km/L")
	drawLeafIcon(ctx, baseX+indXIcon+16, ecoY-8, 30, ecoCol)

	// TEMP 88 °C
	tempY := indYStart + indSpacing
	tempCol := hexColor("#69f0ae")
	drawTextBaseline(ctx, fontOrbitron, 40, tempCol, baseX+indXVal, tempY+6, "88")
	drawTextRight(ctx, fontShareTech, 24, colTickLabel, baseX+indXUnit, tempY+4, "°C")
	drawThermoIcon(ctx, baseX+indXIcon+10, tempY-8, 40, tempCol)

	// TRIP 308.3 km
	tripY := indYStart + indSpacing*2
	tripCol := hexColor("#69f0ae")
	drawTextBaseline(ctx, fontOrbitron, 40, tripCol, baseX+indXVal, tripY+6, "308.3")
	drawTextRight(ctx, fontShareTech, 24, colTickLabel, baseX+indXUnit, tripY+4, "km")
	drawRoadIcon(ctx, baseX+indXIcon+10, tripY-8, 40, tripCol)

	// OIL 0 km
	oilY := indYStart + indSpacing*3
	oilCol := hexColor("#69f0ae")
	drawTextBaseline(ctx, fontOrbitron, 40, oilCol, baseX+indXVal, oilY+6, "0")
	drawTextRight(ctx, fontShareTech, 24, colTickLabel, baseX+indXUnit, oilY+4, "km")
	drawDropletIcon(ctx, baseX+indXIcon+10, oilY-8, 40, oilCol)
}

// colTickLabel は白 (定数参照のための alias)
var colTickLabel = hexColor("#ffffff")

// polarAtUp は任意中心点 (screen座標) → Y-up canvas 座標に変換
func polarAtUp(cxScreen, cyScreen, radius, deg float64) (float64, float64) {
	rad := deg * math.Pi / 180
	return cxScreen + radius*math.Sin(rad), (screenH - cyScreen) + radius*math.Cos(rad)
}

// hslColor は HSL (h: 0-360, s/l: 0-100) → color.RGBA
func hslColor(h, s, l float64) color.RGBA {
	s /= 100
	l /= 100
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return color.RGBA{
		R: uint8((r + m) * 255),
		G: uint8((g + m) * 255),
		B: uint8((b + m) * 255),
		A: 255,
	}
}

// === インジケーターアイコン (ブラウザ版 indicators.js の SVG パスそのまま) ===

// SVG パスは 24x24 viewBox 基準
const (
	svgIconLeaf   = "M0 -12C-5 -4 -7 2 -7 7c0 3 3 6 7 6s7-3 7-6c0-5-2-11-7-19z"
	svgIconThermo = "M12 2C10.34 2 9 3.34 9 5v8.59c-1.22.73-2 2.05-2 3.41 0 2.76 2.24 5 5 5s5-2.24 5-5c0-1.36-.78-2.68-2-3.41V5c0-1.66-1.34-3-3-3zm0 2c.55 0 1 .45 1 1v9.13l.5.29C14.46 15 15 15.96 15 17c0 1.65-1.35 3-3 3s-3-1.35-3-3c0-1.04.54-2 1.5-2.58l.5-.29V5c0-.55.45-1 1-1z"
	svgIconRoad   = "M11 2h2v4h-2zm0 6h2v4h-2zm0 6h2v4h-2zM2 2l4 20h2L5 2zm20 0h-2L16 22h2z"
	svgIconOil    = "M12 2C12 2 6 10 6 15a6 6 0 0 0 12 0c0-5-6-13-6-13zm0 17a3 3 0 0 1-3-3c0-.5.1-1 .3-1.5.2-.4.8-.3.9.2.1.3.1.6.1.9a1.8 1.8 0 0 0 1.8 1.8c.4 0 .7-.3.6-.7-.3-1.5-1.2-2.8-2.2-3.9-.3-.3 0-.8.4-.6C13.3 12.5 15 14.5 15 16a3 3 0 0 1-3 3z"
)

// drawSVGIconFill は SVG パスを塗りつぶしで描画する（createIconPath 相当）
// (cxs, cys) はアイコン中央の画面座標、size はピクセルサイズ
// グロー付き: 外側に向けて wider stroke を重ね塗り
func drawSVGIconFill(ctx *canvas.Context, cxs, cys, size float64, svgPath string, col color.RGBA) {
	p, err := canvas.ParseSVGPath(svgPath)
	if err != nil {
		return
	}
	scale := size / 24.0
	p = p.Transform(canvas.Identity.Scale(scale, -scale))
	cxu := cxs
	cyu := screenH - cys
	offsetX := -12 * scale
	offsetY := 12 * scale
	p = p.Translate(cxu+offsetX, cyu+offsetY)

	// グロー層（外側に向けて stroke 太く）
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
		ctx.SetStrokeColor(withAlpha(col, l.alpha))
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

// drawSVGIconStroke は SVG パスをアウトライン描画する（グロー付き）
func drawSVGIconStroke(ctx *canvas.Context, cxs, cys, size, rotDeg float64, svgPath string, strokeW float64, col color.RGBA) {
	p, err := canvas.ParseSVGPath(svgPath)
	if err != nil {
		return
	}
	scale := size / 20.0
	p = p.Transform(canvas.Identity.Scale(scale, -scale))
	if rotDeg != 0 {
		p = p.Transform(canvas.Identity.Rotate(-rotDeg))
	}
	cxu := cxs
	cyu := screenH - cys
	p = p.Translate(cxu, cyu)

	// グロー層
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
		ctx.SetStrokeColor(withAlpha(col, l.alpha))
		ctx.SetStrokeWidth(strokeW + l.widthAdd)
		ctx.SetStrokeCapper(canvas.RoundCap)
		ctx.SetStrokeJoiner(canvas.RoundJoin)
		ctx.DrawPath(0, 0, p)
		ctx.Pop()
	}

	// 本体
	ctx.Push()
	ctx.SetFillColor(canvas.Transparent)
	ctx.SetStrokeColor(col)
	ctx.SetStrokeWidth(strokeW)
	ctx.DrawPath(0, 0, p)
	ctx.Pop()
}

// drawLeafIcon は葉アイコン (ECO) — outline + vein (dashed) + stem, rotate 60°
func drawLeafIcon(ctx *canvas.Context, cxs, cys, size float64, col color.RGBA) {
	scale := size / 20.0
	cos60 := math.Cos(60 * math.Pi / 180)
	sin60 := math.Sin(60 * math.Pi / 180)

	tr := func(x, y float64) (float64, float64) {
		sx, sy := x*scale, y*scale
		rx := sx*cos60 - sy*sin60
		ry := sx*sin60 + sy*cos60
		return cxs + rx, screenH - (cys + ry)
	}

	// アウトライン
	drawSVGIconStroke(ctx, cxs, cys, size, 60, svgIconLeaf, 3.0, col)

	// 葉脈 (均等な細い破線 + グロー)
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
	// 葉脈のグロー（太めに半透明で数層）
	veinGlows := []struct {
		width float64
		alpha uint8
	}{
		{6, 8},
		{4, 18},
		{3, 40},
	}
	for _, l := range veinGlows {
		drawDashedLine(ctx, vx1, vy1, vx2, vy2, l.width, withAlpha(col, l.alpha), []float64{dash, gap})
	}
	// 本体
	drawDashedLine(ctx, vx1, vy1, vx2, vy2, 2.5, col, []float64{dash, gap})

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
		drawLine(ctx, sx1, sy1, sx2, sy2, l.width, withAlpha(col, l.alpha))
	}
	drawLine(ctx, sx1, sy1, sx2, sy2, 3.0, col)
}

// drawDashedLine は破線を描画する
func drawDashedLine(ctx *canvas.Context, x1, y1, x2, y2, width float64, col color.RGBA, dashes []float64) {
	p := &canvas.Path{}
	p.MoveTo(x1, y1)
	p.LineTo(x2, y2)

	ctx.Push()
	ctx.SetFillColor(canvas.Transparent)
	ctx.SetStrokeColor(col)
	ctx.SetStrokeWidth(width)
	ctx.SetStrokeCapper(canvas.ButtCap)
	ctx.SetDashes(0, dashes...)
	ctx.DrawPath(0, 0, p)
	ctx.Pop()
}

// drawThermoIcon は温度計アイコン (TEMP) — filled
func drawThermoIcon(ctx *canvas.Context, cxs, cys, size float64, col color.RGBA) {
	drawSVGIconFill(ctx, cxs, cys, size, svgIconThermo, col)
}

// drawRoadIcon は道路アイコン (TRIP) — filled
func drawRoadIcon(ctx *canvas.Context, cxs, cys, size float64, col color.RGBA) {
	drawSVGIconFill(ctx, cxs, cys, size, svgIconRoad, col)
}

// drawDropletIcon は雫アイコン (OIL) — filled
func drawDropletIcon(ctx *canvas.Context, cxs, cys, size float64, col color.RGBA) {
	drawSVGIconFill(ctx, cxs, cys, size, svgIconOil, col)
}

// drawLineScreen は画面座標(Y-down)で太線を描画する
func drawLineScreen(ctx *canvas.Context, x1, y1, x2, y2, width float64, col color.RGBA) {
	drawLine(ctx, x1, screenH-y1, x2, screenH-y2, width, col)
}

// drawCircleScreen は画面座標で塗りつぶし円を描画する
func drawCircleScreen(ctx *canvas.Context, cxs, cys, radius float64, col color.RGBA) {
	drawCircle(ctx, cxs, screenH-cys, radius, col)
}

// drawArc2 は指定中心点でアークを描画する（既存 drawArc を任意中心対応に拡張）
// gearColor はレンジとHOLD状態からギア枠の色を返す
func gearColor(atRange string, hold bool) color.RGBA {
	switch atRange {
	case "P", "N":
		return hexColor("#ffffff")
	case "R":
		return hexColor("#ff9800")
	default:
		if hold {
			return hexColor("#fdd835")
		}
		return hexColor("#69f0ae")
	}
}

// === 色ヘルパー ===

func hexColor(s string) color.RGBA {
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{255, 255, 255, 255}
	}
	r := hexVal(s[1])<<4 | hexVal(s[2])
	g := hexVal(s[3])<<4 | hexVal(s[4])
	b := hexVal(s[5])<<4 | hexVal(s[6])
	return color.RGBA{r, g, b, 255}
}

func hexVal(b byte) uint8 {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	}
	return 0
}

func withAlpha(c color.RGBA, a uint8) color.RGBA {
	// tdewolff/canvas は premultiplied alpha を期待するので、RGBに alpha をかける
	scale := float64(a) / 255.0
	return color.RGBA{
		R: uint8(float64(c.R) * scale),
		G: uint8(float64(c.G) * scale),
		B: uint8(float64(c.B) * scale),
		A: a,
	}
}

func speedColor(v float64) color.RGBA {
	switch {
	case v >= 120:
		return hexColor("#f44336")
	case v >= 100:
		return hexColor("#ff9800")
	case v >= 80:
		return hexColor("#ffeb3b")
	case v >= 60:
		return hexColor("#69f0ae")
	case v >= 30:
		return hexColor("#42a5f5")
	default:
		return hexColor("#78909c")
	}
}

func formatComma(n int) string {
	if n < 0 {
		return "-" + formatComma(-n)
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%s,%03d", formatComma(n/1000), n%1000)
}

func rpmColorFn(rpm float64) color.RGBA {
	switch {
	case rpm >= 6500:
		return hexColor("#f44336")
	case rpm >= 4500:
		return hexColor("#ff9800")
	case rpm >= 3000:
		return hexColor("#fdd835")
	case rpm >= 1500:
		return hexColor("#69f0ae")
	default:
		return hexColor("#42a5f5")
	}
}
