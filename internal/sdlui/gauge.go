package sdlui

import (
	"fmt"
	"math"

	"github.com/veandco/go-sdl2/sdl"
)

// GaugeConfig は速度ゲージの設定
type GaugeConfig struct {
	CX, CY          float64 // 中心座標
	Radius           float64 // 速度トラック半径
	MaxSpeed         float64 // 最大速度 (km/h)
	ThrottleIdlePct  float64 // スロットルアイドル開度
	ThrottleMaxPct   float64 // スロットル最大開度
	OrbitronPath     string  // Orbitron フォントパス
	ShareTechPath    string  // Share Tech Mono フォントパス
}

// SpeedGauge は左パネル全体の描画状態を保持する
type SpeedGauge struct {
	cfg          GaugeConfig
	fm           *FontManager
	staticTex    *sdl.Texture

	// 速度
	currentSpeed float64
	targetSpeed  float64

	// RPM
	currentRPM   float64
	targetRPM    float64

	// スロットル
	currentThr   float64
	targetThr    float64

	// ギア/レンジ
	gear         int
	atRange      string
	hold         bool
	tcLocked     bool
}

// 定数（gauge.js と同一）
const (
	arcStart = -135.0
	arcEnd   = 135.0
	arcSweep = 270.0

	trackWidth      = 16.0
	needleWidth     = 6.0
	needleGap       = 24.0
	centerDotR      = 8.0
	tickMajorLen    = 30.0
	tickMinorLen    = 18.0
	tickOuterGap    = 4.0
	labelOffset     = 54.0

	rpmROffset      = 24.0 // RPM アーク: 速度トラック外側
	rpmArcWidth     = 12.0
	rpmMax          = 8000.0
	rpmLerpSpeed    = 0.4

	throttleROffset = 84.0 // スロットルアーク: 速度トラック内側
	throttleArcW    = 10.0
	thrLerpSpeed    = 0.4
	thrDimZone      = 5.0 // pct がこの値以下で暗→明グラデーション
	thrActiveThresh = 0.5

	// ギア/レンジ枠
	gearBoxW = 64.0
	gearBoxH = 62.0
)

// 色定数
var (
	colorTrack     = Hex("#181820")
	colorTickMajor = Hex("#aaaaaa")
	colorTickMinor = Hex("#444444")
	colorTickLabel = Hex("#ffffff")
	colorCenterDot = Hex("#1a1a22")
	colorCenterRim = Hex("#444444")
	colorRedzone   = Hex("#3d0000")
	colorThrTrack  = Hex("#0a0a0f")
	colorRPMTrack  = Hex("#1a1a24")
	colorDim       = Hex("#333333")
	colorWhite     = Hex("#ffffff")
	colorHoldYel   = Hex("#fdd835")
	colorGreen     = Hex("#69f0ae")
	colorOrange    = Hex("#ff9800")
)

// NewSpeedGauge は速度ゲージを作成する
func NewSpeedGauge(renderer *sdl.Renderer, fm *FontManager, cfg GaugeConfig) *SpeedGauge {
	g := &SpeedGauge{
		cfg: cfg,
		fm:  fm,
	}
	g.buildStaticTexture(renderer)
	return g
}

// buildStaticTexture は静的レイヤーをテクスチャにベイクする
func (g *SpeedGauge) buildStaticTexture(renderer *sdl.Renderer) {
	tex, err := renderer.CreateTexture(
		sdl.PIXELFORMAT_RGBA8888,
		sdl.TEXTUREACCESS_TARGET,
		560, 480,
	)
	if err != nil {
		return
	}
	tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	renderer.SetRenderTarget(tex)
	renderer.SetDrawColor(0, 0, 0, 0)
	renderer.Clear()

	cx, cy, r := g.cfg.CX, g.cfg.CY, g.cfg.Radius
	rpmR := r + rpmROffset
	throttleR := r - throttleROffset

	// RPM トラック（最外周）
	DrawArc(renderer, cx, cy, rpmR-rpmArcWidth/2, rpmR+rpmArcWidth/2, arcStart, arcEnd, colorRPMTrack)

	// RPM レッドゾーン背景 (6500-8000)
	redStart := arcStart + (6500.0/rpmMax)*arcSweep
	DrawArc(renderer, cx, cy, rpmR-rpmArcWidth/2, rpmR+rpmArcWidth/2, redStart, arcEnd, colorRedzone)

	// 速度トラック
	DrawArc(renderer, cx, cy, r-trackWidth/2, r+trackWidth/2, arcStart, arcEnd, colorTrack)

	// 速度目盛り
	maxSpd := g.cfg.MaxSpeed
	majorInterval := 20.0
	for spd := 0.0; spd <= maxSpd; spd += majorInterval {
		angle := arcStart + (spd/maxSpd)*arcSweep
		outerR := r + tickOuterGap
		innerR := outerR - tickMajorLen
		ox, oy := polarToXY(cx, cy, outerR, angle)
		ix, iy := polarToXY(cx, cy, innerR, angle)
		DrawThickLine(renderer, ix, iy, ox, oy, 5, colorTickMajor)

		labelR := r - labelOffset
		lx, ly := polarToXY(cx, cy, labelR, angle)
		label := fmt.Sprintf("%d", int(spd))
		g.fm.DrawTextCentered(label, g.cfg.ShareTechPath, 28, colorTickLabel, lx, ly)

		if spd < maxSpd {
			for j := 1; j < 4; j++ {
				minSpd := spd + majorInterval*float64(j)/4.0
				if minSpd > maxSpd {
					break
				}
				minAngle := arcStart + (minSpd/maxSpd)*arcSweep
				mx, my := polarToXY(cx, cy, outerR, minAngle)
				mix, miy := polarToXY(cx, cy, outerR-tickMinorLen, minAngle)
				DrawThickLine(renderer, mix, miy, mx, my, 2.5, colorTickMinor)
			}
		}
	}

	// スロットルトラック
	DrawArc(renderer, cx, cy, throttleR-throttleArcW/2, throttleR+throttleArcW/2, arcStart, arcEnd, colorThrTrack)

	// ギア/レンジ枠（静的：枠線のみ）
	rangeX := cx - r - 10
	gearX := cx + r + 2
	boxY := 62.0 - gearBoxH + 14
	drawRoundedRect(renderer, rangeX-gearBoxW/2, boxY, gearBoxW, gearBoxH, colorCenterRim)
	drawRoundedRect(renderer, gearX-gearBoxW/2, boxY, gearBoxW, gearBoxH, colorCenterRim)

	renderer.SetRenderTarget(nil)
	g.staticTex = tex
}

// drawRoundedRect は角丸矩形の枠線を描画する（簡易版）
func drawRoundedRect(renderer *sdl.Renderer, x, y, w, h float64, color RGBA) {
	renderer.SetDrawColor(color.R, color.G, color.B, color.A)
	r := &sdl.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)}
	renderer.DrawRect(r)
}

// SetTarget はターゲット値を設定する
func (g *SpeedGauge) SetTarget(speed float64) {
	g.targetSpeed = Clamp(speed, 0, g.cfg.MaxSpeed)
}

// SetRPM はRPMターゲットを設定する
func (g *SpeedGauge) SetRPM(rpm float64) {
	g.targetRPM = Clamp(rpm, 0, rpmMax)
}

// SetThrottle はスロットル生値を設定する（正規化してから保持）
func (g *SpeedGauge) SetThrottle(rawPct float64) {
	rng := g.cfg.ThrottleMaxPct - g.cfg.ThrottleIdlePct
	if rng <= 0 {
		g.targetThr = 0
		return
	}
	normalized := Clamp((rawPct-g.cfg.ThrottleIdlePct)/rng*100, 0, 100)
	g.targetThr = normalized
}

// SetGear はギア/レンジ情報を設定する
func (g *SpeedGauge) SetGear(gear int, atRange string, hold, tcLocked bool) {
	g.gear = gear
	g.atRange = atRange
	g.hold = hold
	g.tcLocked = tcLocked
}

// Update は1フレーム分の LERP 補間を行う
func (g *SpeedGauge) Update() {
	g.currentSpeed = Lerp(g.currentSpeed, g.targetSpeed, LerpSpeed)
	g.currentRPM = Lerp(g.currentRPM, g.targetRPM, rpmLerpSpeed)
	g.currentThr = Lerp(g.currentThr, g.targetThr, thrLerpSpeed)
}

// Draw は左パネル全体を描画する
func (g *SpeedGauge) Draw(renderer *sdl.Renderer) {
	// 静的レイヤー（左パネル 560×480）
	if g.staticTex != nil {
		dst := sdl.Rect{X: 0, Y: 0, W: 560, H: 480}
		renderer.Copy(g.staticTex, nil, &dst)
	}

	cx, cy, r := g.cfg.CX, g.cfg.CY, g.cfg.Radius
	speed := g.currentSpeed
	spdColor := SpeedColor(speed)

	// --- RPM アーク ---
	g.drawRPMArc(renderer, cx, cy, r+rpmROffset)

	// --- スロットルアーク ---
	g.drawThrottleArc(renderer, cx, cy, r-throttleROffset)

	// --- 速度アーク ---
	if speed > 0.5 {
		pct := speed / g.cfg.MaxSpeed
		endAngle := arcStart + pct*arcSweep
		DrawArc(renderer, cx, cy, r-trackWidth/2, r+trackWidth/2, arcStart, endAngle, spdColor)
	}

	// --- 針 ---
	angle := arcStart + (speed/g.cfg.MaxSpeed)*arcSweep
	needleTipR := r - needleGap
	nx1, ny1 := polarToXY(cx, cy, -16, angle)
	nx2, ny2 := polarToXY(cx, cy, needleTipR, angle)
	DrawThickLine(renderer, nx1, ny1, nx2, ny2, needleWidth+4, spdColor.WithAlpha(40))
	DrawThickLine(renderer, nx1, ny1, nx2, ny2, needleWidth, spdColor)

	// --- 中心ドット ---
	DrawCircleFilled(renderer, cx, cy, centerDotR+2, colorCenterRim)
	DrawCircleFilled(renderer, cx, cy, centerDotR, colorCenterDot)

	// --- RPM 数値（ゲージ上半分中央） ---
	rpmColor := RPMColor(g.currentRPM)
	throttleR := r - throttleROffset
	rpmReadY := cy - throttleR/2 + 5
	if g.currentRPM > 100 {
		rpmText := fmt.Sprintf("%d", int(math.Round(g.currentRPM)))
		g.fm.DrawTextCentered(rpmText, g.cfg.OrbitronPath, 48, rpmColor, cx, rpmReadY)
		g.fm.DrawTextCentered("r/min", g.cfg.ShareTechPath, 24, colorWhite, cx, rpmReadY+34)
	} else {
		g.fm.DrawTextCentered("--", g.cfg.OrbitronPath, 48, colorDim, cx, rpmReadY)
		g.fm.DrawTextCentered("r/min", g.cfg.ShareTechPath, 24, colorDim, cx, rpmReadY+34)
	}

	// --- 速度数値 ---
	speedText := fmt.Sprintf("%d", int(math.Round(speed)))
	numY := cy + r*0.35
	g.fm.DrawTextCentered(speedText, g.cfg.OrbitronPath, 84, spdColor, cx, numY)

	// --- km/h ---
	unitY := numY + 84*0.45
	g.fm.DrawTextCentered("km/h", g.cfg.ShareTechPath, 28, colorWhite, cx, unitY)

	// --- THROTTLE ラベル ---
	thrActive := g.currentThr > thrActiveThresh
	thrPct := Clamp(g.currentThr, 0, 100)
	thrColor := g.throttleColor(thrPct, thrActive)
	thrLabelColor := colorDim
	if thrActive && thrPct >= thrDimZone {
		thrLabelColor = thrColor
	}
	g.fm.DrawTextCentered("THROTTLE", g.cfg.ShareTechPath, 24, thrLabelColor, cx, unitY+64)

	// --- ギア/レンジ ---
	g.drawGearRange(cx, cy, r)
}

// drawRPMArc は RPM アークを描画する
func (g *SpeedGauge) drawRPMArc(renderer *sdl.Renderer, cx, cy, rpmR float64) {
	rpm := g.currentRPM
	if rpm <= 100 {
		return
	}
	pct := rpm / rpmMax
	endAngle := arcStart + pct*arcSweep
	color := RPMColor(rpm)

	// グロー（やや太い半透明アーク）
	DrawArc(renderer, cx, cy, rpmR-rpmArcWidth/2-1, rpmR+rpmArcWidth/2+1, arcStart, endAngle, color.WithAlpha(30))
	// 本体
	DrawArc(renderer, cx, cy, rpmR-rpmArcWidth/2, rpmR+rpmArcWidth/2, arcStart, endAngle, color)
}

// drawThrottleArc はスロットルアークを描画する（HSLグラデーション + dimZone）
func (g *SpeedGauge) drawThrottleArc(renderer *sdl.Renderer, cx, cy, thrR float64) {
	thr := g.currentThr // 0-100%
	if thr <= thrActiveThresh {
		return
	}

	endAngle := arcStart + (thr/100)*arcSweep

	// 1° 刻みで色を変えながら描画（グラデーション）
	steps := int(math.Ceil(endAngle - arcStart))
	for i := 0; i < steps; i++ {
		d0 := arcStart + float64(i)
		d1 := d0 + 1
		if d1 > endAngle {
			d1 = endAngle
		}
		// この角度位置での pct
		segPct := float64(i+1) / float64(steps) * thr
		color := g.throttleColor(segPct, true)

		ox0, oy0 := polarToXY(cx, cy, thrR+throttleArcW/2, d0)
		ox1, oy1 := polarToXY(cx, cy, thrR+throttleArcW/2, d1)
		ix0, iy0 := polarToXY(cx, cy, thrR-throttleArcW/2, d0)
		ix1, iy1 := polarToXY(cx, cy, thrR-throttleArcW/2, d1)

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

// throttleColor はスロットル開度に応じた色を返す（dimZone 対応）
func (g *SpeedGauge) throttleColor(pct float64, active bool) RGBA {
	if !active {
		return colorDim
	}
	hue := 210 - (pct/100)*210
	if pct < thrDimZone {
		dim := pct / thrDimZone
		lum := 15 + dim*40
		sat := dim * 100
		return HSL(hue, sat, lum)
	}
	if hue < 5 {
		return Hex("#f44336")
	}
	return HSL(hue, 100, 55)
}

// drawGearRange はギア/レンジ表示を描画する
func (g *SpeedGauge) drawGearRange(cx, _, r float64) {
	rangeX := cx - r - 10
	gearX := cx + r + 2
	rangeY := 62.0
	holdY := rangeY + gearBoxH - 22
	lockY := holdY

	// レンジ色
	color := g.gearColor()

	// レンジ文字
	if g.atRange != "" {
		g.fm.DrawTextCentered(g.atRange, g.cfg.OrbitronPath, 52, color, rangeX, rangeY-10)
	}

	// ギア番号
	gearText := "-"
	switch {
	case g.atRange == "P" || g.atRange == "N" || g.atRange == "R":
		gearText = "--"
	case g.gear >= 1 && g.gear <= 4:
		gearText = fmt.Sprintf("%d", g.gear)
	}
	g.fm.DrawTextCentered(gearText, g.cfg.OrbitronPath, 52, color, gearX, rangeY-10)

	// HOLD ラベル
	holdColor := colorDim
	if g.hold {
		holdColor = colorHoldYel
	}
	g.fm.DrawTextCentered("HOLD", g.cfg.ShareTechPath, 24, holdColor, rangeX, holdY)

	// LOCK ラベル
	lockColor := colorDim
	if g.tcLocked {
		lockColor = colorGreen
	}
	g.fm.DrawTextCentered("LOCK", g.cfg.ShareTechPath, 24, lockColor, gearX, lockY)
}

// gearColor はレンジに応じた色を返す
func (g *SpeedGauge) gearColor() RGBA {
	switch g.atRange {
	case "P", "N":
		return colorWhite
	case "R":
		return colorOrange
	default:
		if g.hold {
			return colorHoldYel
		}
		return colorGreen
	}
}

// Destroy はリソースを解放する
func (g *SpeedGauge) Destroy() {
	if g.staticTex != nil {
		g.staticTex.Destroy()
	}
}
