package sdlui

import (
	"fmt"
	"math"

	"github.com/veandco/go-sdl2/sdl"
)

// RightPanel は右パネル（バキューム計 + 4行インジケーター）の描画状態
type RightPanel struct {
	fm            *FontManager
	staticTex     *sdl.Texture
	orbitronPath  string
	shareTechPath string

	// バキューム計 LERP
	currentBar float64 // -1.0 ~ 0
	targetBar  float64

	// インジケーターデータ
	avgFuelEco  float64
	fuelEconomy float64
	coolantTemp float64
	tripKm      float64
	oilAlert    string
	oilRemainKm float64
	intakeMAP   float64 // kPa（バキューム色同期用）
}

// 右パネル定数（indicators.js と同一）
const (
	panelOffsetX = 560.0 // 左パネル幅（右パネル描画開始X）
	mapCX        = 110.0 // バキューム計中心X（パネル内座標）
	mapCY        = 155.0
	mapR         = 125.0
	mapArcW      = 10.0
	vacMin       = -1.0
	vacMax       = 0.0
	mapLerpSpeed = 0.35

	indXIcon    = 2.0
	indXVal     = 110.0
	indXUnit    = 200.0
	indYStart   = 305.0
	indSpacing  = 49.0

	// 閾値
	coolantColdMax    = 60.0
	coolantNormalMax  = 100.0
	coolantWarningMax = 104.0
	ecoGradientMax    = 15.0
)

// NewRightPanel は右パネルを作成する
func NewRightPanel(renderer *sdl.Renderer, fm *FontManager, orbitronPath, shareTechPath string) *RightPanel {
	p := &RightPanel{
		fm:            fm,
		orbitronPath:  orbitronPath,
		shareTechPath: shareTechPath,
		currentBar:    -0.7, // 初期値（アイドル付近）
	}
	p.buildStaticTexture(renderer)
	return p
}

// buildStaticTexture はバキューム計の静的要素をベイクする
func (p *RightPanel) buildStaticTexture(renderer *sdl.Renderer) {
	tex, err := renderer.CreateTexture(
		sdl.PIXELFORMAT_RGBA8888,
		sdl.TEXTUREACCESS_TARGET,
		240, 480,
	)
	if err != nil {
		return
	}
	tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	renderer.SetRenderTarget(tex)
	renderer.SetDrawColor(0, 0, 0, 0)
	renderer.Clear()

	cx, cy := mapCX, mapCY

	// バキュームトラック
	DrawArc(renderer, cx, cy, mapR-mapArcW/2, mapR+mapArcW/2, arcStart, arcEnd, colorTrack)

	// バキューム目盛り (5 major, 4 minor between)
	vacMajor := 5
	vacMinor := 4
	vacTotal := vacMajor * vacMinor
	for i := 0; i <= vacTotal; i++ {
		angle := arcStart + (float64(i)/float64(vacTotal))*arcSweep
		isMj := i%vacMinor == 0
		outerR := mapR + 3.0
		innerR := outerR - 14.0
		if !isMj {
			innerR = outerR - 11.0
		}
		ox, oy := polarToXY(cx, cy, outerR, angle)
		ix, iy := polarToXY(cx, cy, innerR, angle)
		tickColor := colorTickMinor
		tickW := 2.0
		if isMj {
			tickColor = colorTickMajor
			tickW = 4.0
		}
		DrawThickLine(renderer, ix, iy, ox, oy, tickW, tickColor)

		if isMj {
			v := vacMin + (float64(i)/float64(vacTotal))*(vacMax-vacMin)
			labelR := mapR - 32.0
			lx, ly := polarToXY(cx, cy, labelR, angle)
			var label string
			if v == 0 {
				label = "0"
			} else {
				label = fmt.Sprintf("%.1f", v)
			}
			p.fm.DrawTextCentered(label, p.shareTechPath, 22, colorTickLabel, lx, ly)
		}
	}

	// 中心ドット
	DrawCircleFilled(renderer, cx, cy, 5, colorCenterRim)
	DrawCircleFilled(renderer, cx, cy, 3, colorCenterDot)

	// Bar 単位ラベル
	p.fm.DrawTextCentered("Bar", p.shareTechPath, 24, colorWhite, cx, cy+mapR*0.38+44)

	// 区切り線
	renderer.SetDrawColor(0x22, 0x22, 0x22, 0xff)
	renderer.DrawLineF(10, float32(indYStart-16), 210, float32(indYStart-16))

	// 固定単位ラベル
	p.fm.DrawTextRight("km/L", p.shareTechPath, 24, colorWhite, indXUnit, indYStart+4)
	p.fm.DrawTextRight("°C", p.shareTechPath, 24, colorWhite, indXUnit, indYStart+indSpacing+4)
	p.fm.DrawTextRight("km", p.shareTechPath, 24, colorWhite, indXUnit, indYStart+indSpacing*2+4)
	p.fm.DrawTextRight("km", p.shareTechPath, 24, colorWhite, indXUnit, indYStart+indSpacing*3+4)

	renderer.SetRenderTarget(nil)
	p.staticTex = tex
}

// SetData はインジケーターデータを設定する
func (p *RightPanel) SetData(data GaugeData) {
	// MAP kPa → Bar 変換
	p.targetBar = Clamp((data.IntakeMAP-101.3)/100, vacMin, vacMax)
	p.avgFuelEco = data.AvgFuelEco
	p.fuelEconomy = data.FuelEconomy
	p.coolantTemp = data.CoolantTemp
	p.tripKm = data.TripKm
	p.oilAlert = data.OilAlert
	p.oilRemainKm = data.OilRemainKm
	p.intakeMAP = data.IntakeMAP
}

// Update は1フレーム分の LERP 補間を行う
func (p *RightPanel) Update() {
	delta := p.targetBar - p.currentBar
	if math.Abs(delta) > LerpThreshold*0.01 {
		p.currentBar = p.currentBar + delta*mapLerpSpeed
	} else {
		p.currentBar = p.targetBar
	}
}

// Draw は右パネルを描画する
func (p *RightPanel) Draw(renderer *sdl.Renderer) {
	// 静的レイヤー（パネル内座標で描画済み → panelOffsetX にオフセットして表示）
	if p.staticTex != nil {
		dst := sdl.Rect{X: int32(panelOffsetX), Y: 0, W: 240, H: 480}
		renderer.Copy(p.staticTex, nil, &dst)
	}

	// 以下はオフセット付き絶対座標で描画
	cx := panelOffsetX + mapCX
	cy := mapCY

	// バキュームアーク
	p.drawVacuumArc(renderer, cx, cy)

	// バキューム針
	p.drawVacuumNeedle(renderer, cx, cy)

	// VACUUM ラベル
	p.drawVacuumLabel(renderer, cx, cy)

	// バキューム数値
	vacColor := p.vacuumColor()
	if p.currentBar < -0.01 {
		barText := fmt.Sprintf("%.2f", p.currentBar)
		p.fm.DrawTextCentered(barText, p.orbitronPath, 48, vacColor, cx, cy+mapR*0.38)
	} else {
		p.fm.DrawTextCentered("--", p.orbitronPath, 48, colorDim, cx, cy+mapR*0.38)
	}

	// 4行インジケーター
	p.drawIndicators(renderer)
}

// drawVacuumArc はバキュームアークを描画する
func (p *RightPanel) drawVacuumArc(renderer *sdl.Renderer, cx, cy float64) {
	bar := p.currentBar
	pct := Clamp((bar-vacMin)/(vacMax-vacMin)*100, 0, 100)
	if pct < 0.5 {
		return
	}
	endAngle := arcStart + (pct/100)*arcSweep
	color := p.vacuumColor()

	// グロー
	DrawArc(renderer, cx, cy, mapR-mapArcW/2-2, mapR+mapArcW/2+2, arcStart, endAngle, color.WithAlpha(40))
	// 本体
	DrawArc(renderer, cx, cy, mapR-mapArcW/2, mapR+mapArcW/2, arcStart, endAngle, color)
}

// drawVacuumNeedle はバキューム針を描画する
func (p *RightPanel) drawVacuumNeedle(renderer *sdl.Renderer, cx, cy float64) {
	bar := p.currentBar
	pct := Clamp((bar-vacMin)/(vacMax-vacMin)*100, 0, 100)
	angle := arcStart + (pct/100)*arcSweep

	tipR := mapR - 18
	nx1, ny1 := polarToXY(cx, cy, -10, angle)
	nx2, ny2 := polarToXY(cx, cy, tipR, angle)

	color := p.vacuumColor()
	DrawThickLine(renderer, nx1, ny1, nx2, ny2, needleWidth+6, color.WithAlpha(40))
	DrawThickLine(renderer, nx1, ny1, nx2, ny2, needleWidth, color)
}

// drawVacuumLabel は VACUUM ラベルを描画する（負圧に応じて暗→明→赤+グロー）
func (p *RightPanel) drawVacuumLabel(renderer *sdl.Renderer, cx, cy float64) {
	bar := p.currentBar
	pct := Clamp((bar-vacMin)/(vacMax-vacMin)*100, 0, 100)
	hue := (1 - pct/100) * 210

	lum := 10 + (pct/100)*45
	sat := math.Min(100, pct*1.5)
	var vacCol RGBA
	if hue < 5 && sat > 80 {
		vacCol = Hex("#f44336")
	} else {
		vacCol = HSL(hue, sat, lum)
	}
	p.fm.DrawTextCentered("VACUUM", p.shareTechPath, 24, vacCol, cx, cy-30)
}

// vacuumColor は現在のバキューム値に応じた色を返す
func (p *RightPanel) vacuumColor() RGBA {
	bar := p.currentBar
	pct := Clamp((bar-vacMin)/(vacMax-vacMin)*100, 0, 100)
	if bar >= -0.01 {
		return colorDim
	}
	hue := (1 - pct/100) * 210
	if hue < 5 {
		return Hex("#f44336")
	}
	return HSL(hue, 100, 55)
}

// drawIndicators は4行インジケーターを描画する
func (p *RightPanel) drawIndicators(renderer *sdl.Renderer) {
	baseX := panelOffsetX

	// ECO
	ecoY := indYStart
	ecoCol := p.ecoColor()
	if p.avgFuelEco > 0.1 {
		ecoText := fmt.Sprintf("%.1f", math.Min(p.avgFuelEco, 99.9))
		p.fm.DrawTextCentered(ecoText, p.orbitronPath, 40, ecoCol, baseX+indXVal, ecoY+6)
	} else {
		p.fm.DrawTextCentered("--", p.orbitronPath, 40, colorDim, baseX+indXVal, ecoY+6)
	}
	// ECO アイコン（簡易：丸で代替）
	DrawCircleFilled(renderer, baseX+indXIcon+16, ecoY-2, 8, ecoCol)

	// TEMP
	tempY := indYStart + indSpacing
	tempCol := p.tempColor()
	if p.coolantTemp > 0 {
		tempText := fmt.Sprintf("%d", int(math.Round(p.coolantTemp)))
		p.fm.DrawTextCentered(tempText, p.orbitronPath, 40, tempCol, baseX+indXVal, tempY+6)
	} else {
		p.fm.DrawTextCentered("--", p.orbitronPath, 40, colorDim, baseX+indXVal, tempY+6)
	}
	DrawCircleFilled(renderer, baseX+indXIcon+10, tempY-2, 8, tempCol)

	// TRIP
	tripY := indYStart + indSpacing*2
	tripCol := p.tripColor()
	tripText := "0"
	if p.tripKm >= 0.1 {
		tripText = fmt.Sprintf("%.1f", p.tripKm)
	}
	p.fm.DrawTextCentered(tripText, p.orbitronPath, 40, tripCol, baseX+indXVal, tripY+6)
	DrawCircleFilled(renderer, baseX+indXIcon+10, tripY-2, 8, tripCol)

	// OIL
	oilY := indYStart + indSpacing*3
	oilCol := p.oilColor()
	if p.oilRemainKm > 0 {
		oilText := fmt.Sprintf("%d", int(math.Round(p.oilRemainKm)))
		p.fm.DrawTextCentered(oilText, p.orbitronPath, 40, oilCol, baseX+indXVal, oilY+6)
	} else {
		p.fm.DrawTextCentered("--", p.orbitronPath, 40, colorDim, baseX+indXVal, oilY+6)
	}
	DrawCircleFilled(renderer, baseX+indXIcon+10, oilY-2, 8, oilCol)
}

// ecoColor は瞬間燃費に応じた色を返す（エンブレ/停車時はバキューム同期）
func (p *RightPanel) ecoColor() RGBA {
	if p.fuelEconomy < 0 || p.fuelEconomy < 0.1 {
		// エンブレ/停車: バキューム色同期
		bar := (p.intakeMAP - 101.3) / 100
		pct := Clamp((bar-vacMin)/(vacMax-vacMin)*100, 0, 100)
		hue := (1 - pct/100) * 210
		return HSL(hue, 100, 55)
	}
	hue := math.Min(p.fuelEconomy/ecoGradientMax, 1) * 153
	return HSL(hue, 100, 55)
}

// tempColor は水温に応じた色を返す
func (p *RightPanel) tempColor() RGBA {
	t := p.coolantTemp
	if t <= 0 {
		return colorDim
	}
	switch {
	case t < coolantColdMax:
		return Hex("#29b6f6")
	case t <= coolantNormalMax:
		return Hex("#69f0ae")
	case t <= coolantWarningMax:
		return Hex("#ff9800")
	default:
		return Hex("#f44336")
	}
}

// tripColor はトリップ距離に応じた色を返す
func (p *RightPanel) tripColor() RGBA {
	km := p.tripKm
	switch {
	case km < 350:
		return Hex("#69f0ae")
	case km < 400:
		return Hex("#fdd835")
	case km < 450:
		return Hex("#ff9800")
	default:
		return Hex("#f44336")
	}
}

// oilColor はオイルアラートに応じた色を返す
func (p *RightPanel) oilColor() RGBA {
	switch p.oilAlert {
	case "yellow":
		return Hex("#fdd835")
	case "orange":
		return Hex("#ff9800")
	case "red":
		return Hex("#f44336")
	default:
		return Hex("#69f0ae")
	}
}

// Destroy はリソースを解放する
func (p *RightPanel) Destroy() {
	if p.staticTex != nil {
		p.staticTex.Destroy()
	}
}
