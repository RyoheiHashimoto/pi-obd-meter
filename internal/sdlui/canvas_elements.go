package sdlui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
)

// 各要素の描画関数。全て画面座標（Y-down）で描く。
// element.newCanvas() が Translate でローカル座標に落とす。

// --- 背景（トラックのみ、静的）---
func (s *CanvasScene) renderBackground() error {
	c, ctx := s.bgEl.newCanvas()

	// RPM トラック
	rpmR := gaugeR + rpmROffset
	drawArcAt(ctx, cxScreen, cyScreen, rpmR, rpmArcWidth, arcStart, arcEnd, colRPMTrack)
	// RPM レッドゾーン
	redStart := arcStart + (6500.0/rpmMaxVal)*arcSweep
	drawArcAt(ctx, cxScreen, cyScreen, rpmR, rpmArcWidth, redStart, arcEnd, colRedzone)

	// 速度トラック
	drawArcAt(ctx, cxScreen, cyScreen, gaugeR, trackWidth, arcStart, arcEnd, colTrack)

	// 速度目盛り（線のみ、ラベルは labelsEl に）
	maxSpd := s.cfg.MaxSpeed
	majorInterval := 20.0
	for spd := 0.0; spd <= maxSpd; spd += majorInterval {
		angle := arcStart + (spd/maxSpd)*arcSweep
		outerR := gaugeR + tickOuterGap
		innerR := outerR - tickMajorLen
		ox, oy := polarToScreen(cxScreen, cyScreen, outerR, angle)
		ix, iy := polarToScreen(cxScreen, cyScreen, innerR, angle)
		drawLineAt(ctx, ix, iy, ox, oy, 5, colTickMajor)

		// 副目盛り
		if spd < maxSpd {
			for j := 1; j < 5; j++ {
				minSpd := spd + majorInterval*float64(j)/5.0
				if minSpd > maxSpd {
					break
				}
				minAngle := arcStart + (minSpd/maxSpd)*arcSweep
				mx, my := polarToScreen(cxScreen, cyScreen, outerR, minAngle)
				mix, miy := polarToScreen(cxScreen, cyScreen, outerR-tickMinorLen, minAngle)
				drawLineAt(ctx, mix, miy, mx, my, 2.5, colTickMinor)
			}
		}
	}

	// スロットルトラック
	thrR := gaugeR - thrROffset
	drawArcAt(ctx, cxScreen, cyScreen, thrR, thrArcW, arcStart, arcEnd, colThrTrack)

	// --- 右パネル：バキューム計（トラック + 目盛り線のみ）---
	vcx := panelOffsetX + mapCX
	vcy := mapCY
	drawArcAt(ctx, vcx, vcy, mapR, mapArcW, arcStart, arcEnd, colTrack)
	vacTotal := 20
	for i := 0; i <= vacTotal; i++ {
		angle := arcStart + (float64(i)/float64(vacTotal))*arcSweep
		isMj := i%4 == 0
		outerR := mapR + 3.0
		innerR := outerR - 16.0
		ox, oy := polarToScreen(vcx, vcy, outerR, angle)
		ix, iy := polarToScreen(vcx, vcy, innerR, angle)
		col := colTickMinor
		w := 2.0
		if isMj {
			col = colTickMajor
			w = 4.0
		}
		drawLineAt(ctx, ix, iy, ox, oy, w, col)
	}

	// バキューム中心ドット
	drawCircleAt(ctx, vcx, vcy, 5, colCenterRim)
	drawCircleAt(ctx, vcx, vcy, 3, colCenterDot)

	// 区切り線（インジケーターとバキューム値の間）
	drawLineAt(ctx, panelOffsetX+40, indYStart-16, panelOffsetX+240, indYStart-16, 1, Hex("#222222"))

	return s.bgEl.commit(c)
}

// --- 静的ラベル（フェードイン対象）---
func (s *CanvasScene) renderLabels() error {
	c, ctx := s.labelsEl.newCanvas()

	// 速度目盛りラベル
	maxSpd := s.cfg.MaxSpeed
	for spd := 0.0; spd <= maxSpd; spd += 20.0 {
		angle := arcStart + (spd/maxSpd)*arcSweep
		labelR := gaugeR - labelOffset
		lx, ly := polarToScreen(cxScreen, cyScreen, labelR, angle)
		s.fonts.drawTextCentered(ctx, s.fonts.ShareTech, 28, colTickLabel, lx, ly, fmt.Sprintf("%d", int(spd)))
	}

	// バキューム目盛りラベル
	vcx := panelOffsetX + mapCX
	vcy := mapCY
	vacTotal := 20
	for i := 0; i <= vacTotal; i += 4 {
		angle := arcStart + (float64(i)/float64(vacTotal))*arcSweep
		v := vacMin + (float64(i)/float64(vacTotal))*(vacMax-vacMin)
		labelR := mapR - 32.0
		lx, ly := polarToScreen(vcx, vcy, labelR, angle)
		var label string
		if v == 0 {
			label = "0"
		} else {
			ss := fmt.Sprintf("%.1f", v)
			label = strings.Replace(ss, "-0.", "-.", 1)
		}
		s.fonts.drawTextCentered(ctx, s.fonts.ShareTech, 22, colTickLabel, lx, ly, label)
	}

	// Bar 単位ラベル
	s.fonts.drawTextBaseline(ctx, s.fonts.ShareTech, 24, colWhite, vcx, vcy+mapR*0.38+44, "Bar")

	// 右パネル固定単位ラベル
	s.fonts.drawTextRight(ctx, s.fonts.ShareTech, 24, colWhite, panelOffsetX+indXUnit, indYStart+4, "km/L")
	s.fonts.drawTextRight(ctx, s.fonts.ShareTech, 24, colWhite, panelOffsetX+indXUnit, indYStart+indSpacing+4, "°C")
	s.fonts.drawTextRight(ctx, s.fonts.ShareTech, 24, colWhite, panelOffsetX+indXUnit, indYStart+indSpacing*2+4, "km")
	s.fonts.drawTextRight(ctx, s.fonts.ShareTech, 24, colWhite, panelOffsetX+indXUnit, indYStart+indSpacing*3+4, "km")

	// km/h ラベル
	numYScreen := cyScreen + gaugeR*0.42
	unitYScreen := numYScreen + 84*0.45
	s.fonts.drawTextBaseline(ctx, s.fonts.ShareTech, 28, colWhite, cxScreen, unitYScreen, "km/h")

	// r/min ラベル
	throttleR := gaugeR - thrROffset
	rpmReadYScreen := cyScreen - throttleR/2 + 5
	s.fonts.drawTextBaseline(ctx, s.fonts.ShareTech, 24, colWhite, cxScreen, rpmReadYScreen+34, "r/min")

	return s.labelsEl.commit(c)
}

// --- 速度アーク（動的）---
func (s *CanvasScene) renderSpeedArc() error {
	c, ctx := s.speedArcEl.newCanvas()
	if s.curSpeed > 0.5 {
		spdColor := SpeedColor(s.curSpeed)
		spdPct := s.curSpeed / s.cfg.MaxSpeed
		spdEnd := arcStart + spdPct*arcSweep
		drawGlowArcAt(ctx, cxScreen, cyScreen, gaugeR, trackWidth, arcStart, spdEnd, spdColor)
	}
	return s.speedArcEl.commit(c)
}

// --- 速度針（動的）---
func (s *CanvasScene) renderSpeedNeedle() error {
	c, ctx := s.speedNeedleEl.newCanvas()
	spdColor := SpeedColor(s.curSpeed)
	spdPct := s.curSpeed / s.cfg.MaxSpeed
	needleAngle := arcStart + spdPct*arcSweep
	tipR := gaugeR - needleGap
	nx1, ny1 := polarToScreen(cxScreen, cyScreen, -16, needleAngle)
	nx2, ny2 := polarToScreen(cxScreen, cyScreen, tipR, needleAngle)
	drawGlowLineAt(ctx, nx1, ny1, nx2, ny2, needleWidth, spdColor)
	// 中心ドット（針の上）
	drawCircleAt(ctx, cxScreen, cyScreen, centerDotR+3, colCenterRim)
	drawCircleAt(ctx, cxScreen, cyScreen, centerDotR-1, colCenterDot)
	return s.speedNeedleEl.commit(c)
}

// --- 速度数値（整数値変化時のみ）---
func (s *CanvasScene) renderSpeedNumber() error {
	c, ctx := s.speedNumEl.newCanvas()
	spdColor := s.fadeColor(SpeedColor(s.curSpeed))
	numYScreen := cyScreen + gaugeR*0.42
	s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 84, spdColor, cxScreen, numYScreen, fmt.Sprintf("%d", int(math.Round(s.curSpeed))))
	return s.speedNumEl.commit(c)
}

// --- RPM アーク（動的）---
func (s *CanvasScene) renderRPMArc() error {
	c, ctx := s.rpmArcEl.newCanvas()
	if s.curRPM > 100 {
		rpmColor := RPMColor(s.curRPM)
		rpmPct := s.curRPM / rpmMaxVal
		rpmEnd := arcStart + rpmPct*arcSweep
		rpmR := gaugeR + rpmROffset
		drawGlowArcAt(ctx, cxScreen, cyScreen, rpmR, rpmArcWidth, arcStart, rpmEnd, rpmColor)
	}
	return s.rpmArcEl.commit(c)
}

// --- RPM 数値（整数値変化時のみ）---
func (s *CanvasScene) renderRPMNumber() error {
	c, ctx := s.rpmNumEl.newCanvas()
	throttleR := gaugeR - thrROffset
	rpmReadYScreen := cyScreen - throttleR/2 + 5
	if s.curRPM > 100 {
		rpmColor := s.fadeColor(RPMColor(s.curRPM))
		s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 48, rpmColor, cxScreen, rpmReadYScreen, formatComma(int(math.Round(s.curRPM))))
	} else {
		s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 48, colDim, cxScreen, rpmReadYScreen, "--")
	}
	return s.rpmNumEl.commit(c)
}

// --- スロットルアーク（動的）---
func (s *CanvasScene) renderThrottleArc() error {
	c, ctx := s.thrArcEl.newCanvas()
	if s.curThr > 0.5 {
		thrR := gaugeR - thrROffset
		thrEnd := arcStart + (s.curThr/100)*arcSweep
		thrCol := ThrottleColor(s.curThr, true)
		drawGlowArcAt(ctx, cxScreen, cyScreen, thrR, thrArcW, arcStart, thrEnd, thrCol)
	}
	return s.thrArcEl.commit(c)
}

// --- THROTTLE ラベル ---
func (s *CanvasScene) renderThrottleLabel() error {
	c, ctx := s.thrLabelEl.newCanvas()
	numYScreen := cyScreen + gaugeR*0.42
	unitYScreen := numYScreen + 84*0.45
	lblY := unitYScreen + 50
	if s.curThr >= 5 && s.fadeFactor > 0.01 {
		col := s.fadeColor(ThrottleColor(s.curThr, true))
		s.fonts.drawGlowTextBaseline(ctx, s.fonts.ShareTech, 24, col, cxScreen, lblY, "THROTTLE")
	} else {
		s.fonts.drawTextBaseline(ctx, s.fonts.ShareTech, 24, colDim, cxScreen, lblY, "THROTTLE")
	}
	return s.thrLabelEl.commit(c)
}

// --- ギア/レンジ枠 ---
func (s *CanvasScene) gearColor() color.RGBA {
	switch s.atRange {
	case "P", "N":
		return colWhite
	case "R":
		return colOrange
	default:
		if s.hold {
			return colHoldYel
		}
		return colGreen
	}
}

func (s *CanvasScene) renderRangeBox() error {
	c, ctx := s.rangeBoxEl.newCanvas()
	rangeX := cxScreen - gaugeR - 4
	rangeY := 59.0
	boxY := rangeY - gearBoxH + 14
	col := s.fadeColor(s.gearColor())

	drawRoundedRectAt(ctx, rangeX-gearBoxW/2, boxY, gearBoxW, gearBoxH, 8, 3, col)
	if s.atRange != "" {
		s.fonts.drawGlowTextBaseline(ctx, s.fonts.Orbitron, 52, col, rangeX, rangeY, s.atRange)
	}
	// HOLD ラベル（active 時のみ点灯、起動中は常に消灯）
	if s.hold && s.fadeFactor > 0.01 {
		s.fonts.drawGlowTextBaseline(ctx, s.fonts.ShareTech, 24, s.fadeColor(colHoldYel), rangeX, rangeY+gearBoxH-22, "HOLD")
	} else {
		s.fonts.drawTextBaseline(ctx, s.fonts.ShareTech, 24, colDim, rangeX, rangeY+gearBoxH-22, "HOLD")
	}
	return s.rangeBoxEl.commit(c)
}

func (s *CanvasScene) renderGearBox() error {
	c, ctx := s.gearBoxEl.newCanvas()
	gearX := cxScreen + gaugeR + 2
	rangeY := 59.0
	boxY := rangeY - gearBoxH + 14
	col := s.fadeColor(s.gearColor())

	drawRoundedRectAt(ctx, gearX-gearBoxW/2, boxY, gearBoxW, gearBoxH, 8, 3, col)
	gearText := "-"
	switch {
	case s.atRange == "P" || s.atRange == "N" || s.atRange == "R":
		gearText = "--"
	case s.gear >= 1 && s.gear <= 4:
		gearText = fmt.Sprintf("%d", s.gear)
	}
	s.fonts.drawGlowTextBaseline(ctx, s.fonts.Orbitron, 52, col, gearX, rangeY, gearText)

	// LOCK ラベル（active 時のみ点灯、起動中は常に消灯）
	if s.tcLocked && s.fadeFactor > 0.01 {
		s.fonts.drawGlowTextBaseline(ctx, s.fonts.ShareTech, 24, s.fadeColor(colGreen), gearX, rangeY+gearBoxH-22, "LOCK")
	} else {
		s.fonts.drawTextBaseline(ctx, s.fonts.ShareTech, 24, colDim, gearX, rangeY+gearBoxH-22, "LOCK")
	}
	return s.gearBoxEl.commit(c)
}

// --- バキュームアーク ---
func (s *CanvasScene) vacColor() color.RGBA {
	pct := clamp((s.curBar-vacMin)/(vacMax-vacMin)*100, 0, 100)
	if s.curBar >= -0.01 {
		return colDim
	}
	hue := (1 - pct/100) * 210
	if hue < 5 {
		return Hex("#f44336")
	}
	return HSL(hue, 100, 55)
}

func (s *CanvasScene) renderVacuumArc() error {
	c, ctx := s.vacArcEl.newCanvas()
	vcx := panelOffsetX + mapCX
	vcy := mapCY
	pct := clamp((s.curBar-vacMin)/(vacMax-vacMin)*100, 0, 100)
	if pct >= 0.5 {
		vacEnd := arcStart + (pct/100)*arcSweep
		col := s.vacColor()
		drawGlowArcSubtleAt(ctx, vcx, vcy, mapR, mapArcW, arcStart, vacEnd, col)
	}
	return s.vacArcEl.commit(c)
}

// --- バキューム針 ---
func (s *CanvasScene) renderVacuumNeedle() error {
	c, ctx := s.vacNeedleEl.newCanvas()
	vcx := panelOffsetX + mapCX
	vcy := mapCY
	pct := clamp((s.curBar-vacMin)/(vacMax-vacMin)*100, 0, 100)
	needleAngle := arcStart + (pct/100)*arcSweep
	col := s.vacColor()

	nx1, ny1 := polarToScreen(vcx, vcy, -6, needleAngle)      // 尻ほんの少し戻す（-4→-6）
	nx2, ny2 := polarToScreen(vcx, vcy, mapR-14, needleAngle)
	drawGlowLineSubtleAt(ctx, nx1, ny1, nx2, ny2, 4.5, col)

	// 中心ドット（針の上、少し大きめ）
	drawCircleAt(ctx, vcx, vcy, 7, colCenterRim)
	drawCircleAt(ctx, vcx, vcy, 4, colCenterDot)
	return s.vacNeedleEl.commit(c)
}

// --- バキューム値 ---
func (s *CanvasScene) renderVacuumValue() error {
	c, ctx := s.vacValueEl.newCanvas()
	vcx := panelOffsetX + mapCX
	vcy := mapCY
	col := s.fadeColor(s.vacColor())
	if s.curBar < -0.01 && s.fadeFactor > 0.01 {
		s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 48, col, vcx, vcy+mapR*0.38, fmt.Sprintf("%.2f", s.curBar))
	} else {
		s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 48, colDim, vcx, vcy+mapR*0.38, "--")
	}
	return s.vacValueEl.commit(c)
}

// --- VACUUM ラベル ---
func (s *CanvasScene) renderVacuumLabel() error {
	c, ctx := s.vacLabelEl.newCanvas()
	vcx := panelOffsetX + mapCX
	vcy := mapCY
	pct := clamp((s.curBar-vacMin)/(vacMax-vacMin)*100, 0, 100)
	hue := (1 - pct/100) * 210
	lum := 10 + (pct/100)*45
	sat := math.Min(100, pct*1.5)
	var vacCol color.RGBA
	if hue < 5 && sat > 80 {
		vacCol = Hex("#f44336")
	} else {
		vacCol = HSL(hue, sat, lum)
	}
	if s.curBar < -0.01 && s.fadeFactor > 0.01 {
		s.fonts.drawGlowTextCentered(ctx, s.fonts.ShareTech, 24, s.fadeColor(vacCol), vcx, vcy-30, "VACUUM")
	} else {
		s.fonts.drawTextCentered(ctx, s.fonts.ShareTech, 24, colDim, vcx, vcy-30, "VACUUM")
	}
	return s.vacLabelEl.commit(c)
}

// --- インジケーター: ECO ---
func (s *CanvasScene) ecoColor() color.RGBA {
	if s.fuelEco < 0 || s.fuelEco < 0.1 {
		bar := (s.intakeMAP - 101.3) / 100
		pct := clamp((bar-vacMin)/(vacMax-vacMin)*100, 0, 100)
		hue := (1 - pct/100) * 210
		return HSL(hue, 100, 55)
	}
	hue := math.Min(s.fuelEco/ecoGradMax, 1) * 153
	return HSL(hue, 100, 55)
}

func (s *CanvasScene) ecoDisplayText() string {
	if s.avgFuelEco > 0.1 {
		return fmt.Sprintf("%.1f", math.Min(s.avgFuelEco, 99.9))
	}
	return "--"
}

func (s *CanvasScene) renderIndEco() error {
	c, ctx := s.indEcoEl.newCanvas()
	baseX := panelOffsetX
	ecoY := indYStart
	col := s.fadeColor(s.ecoColor())
	text := s.ecoDisplayText()
	if text == "--" {
		s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 40, colDim, baseX+indXVal, ecoY+6, "--")
	} else {
		s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 40, col, baseX+indXVal, ecoY+6, text)
	}
	drawLeafIconAt(ctx, baseX+indXIcon+16, ecoY-8, 30, col)
	return s.indEcoEl.commit(c)
}

// --- インジケーター: TEMP ---
func (s *CanvasScene) tempColor() color.RGBA {
	t := s.coolantTemp
	if t <= 0 {
		return colDim
	}
	switch {
	case t < coolantColdMax:
		return Hex("#29b6f6")
	case t <= coolantNormalMax:
		return colGreen
	case t <= coolantWarningMax:
		return colOrange
	default:
		return Hex("#f44336")
	}
}

func (s *CanvasScene) tempDisplayText() string {
	if s.coolantTemp > 0 {
		return fmt.Sprintf("%d", int(math.Round(s.coolantTemp)))
	}
	return "--"
}

func (s *CanvasScene) renderIndTemp() error {
	c, ctx := s.indTempEl.newCanvas()
	baseX := panelOffsetX
	tempY := indYStart + indSpacing
	col := s.fadeColor(s.tempColor())
	text := s.tempDisplayText()
	if text == "--" {
		s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 40, colDim, baseX+indXVal, tempY+6, "--")
	} else {
		s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 40, col, baseX+indXVal, tempY+6, text)
	}
	drawThermoIconAt(ctx, baseX+indXIcon+10, tempY-8, 40, col)
	return s.indTempEl.commit(c)
}

// --- インジケーター: TRIP ---
func (s *CanvasScene) tripColor() color.RGBA {
	km := s.tripKm
	switch {
	case km < 350:
		return colGreen
	case km < 400:
		return colHoldYel
	case km < 450:
		return colOrange
	default:
		return Hex("#f44336")
	}
}

func (s *CanvasScene) tripDisplayText() string {
	if s.tripKm >= 0.1 {
		return fmt.Sprintf("%.1f", s.tripKm)
	}
	return "0"
}

func (s *CanvasScene) renderIndTrip() error {
	c, ctx := s.indTripEl.newCanvas()
	baseX := panelOffsetX
	tripY := indYStart + indSpacing*2
	col := s.fadeColor(s.tripColor())
	s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 40, col, baseX+indXVal, tripY+6, s.tripDisplayText())
	drawRoadIconAt(ctx, baseX+indXIcon+10, tripY-8, 40, col)
	return s.indTripEl.commit(c)
}

// --- インジケーター: OIL ---
func (s *CanvasScene) oilColor() color.RGBA {
	switch s.oilAlert {
	case "yellow":
		return colHoldYel
	case "orange":
		return colOrange
	case "red":
		return Hex("#f44336")
	default:
		return colGreen
	}
}

func (s *CanvasScene) oilDisplayText() string {
	if s.oilCurrentKm > 0 {
		return formatComma(int(math.Round(s.oilCurrentKm)))
	}
	return "--"
}

func (s *CanvasScene) renderIndOil() error {
	c, ctx := s.indOilEl.newCanvas()
	baseX := panelOffsetX
	oilY := indYStart + indSpacing*3
	col := s.fadeColor(s.oilColor())
	text := s.oilDisplayText()
	if text == "--" {
		s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 40, colDim, baseX+indXVal, oilY+6, "--")
	} else {
		s.fonts.drawTextBaseline(ctx, s.fonts.Orbitron, 40, col, baseX+indXVal, oilY+6, text)
	}
	drawDropletIconAt(ctx, baseX+indXIcon+10, oilY-8, 40, col)
	return s.indOilEl.commit(c)
}
