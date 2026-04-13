package sdlui

import (
	"fmt"
	"image/color"
	"math"
)

// Color ヘルパー関数群（image/color.RGBA ベース）
// canvas ライブラリと直接互換

// HSL は HSL→RGB 変換を行う（h: 0-360, s: 0-100, l: 0-100）
func HSL(h, s, l float64) color.RGBA {
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

// WithAlpha はアルファ値を変更した色を返す (premultiplied)
func WithAlpha(c color.RGBA, a uint8) color.RGBA {
	scale := float64(a) / 255.0
	return color.RGBA{
		R: uint8(float64(c.R) * scale),
		G: uint8(float64(c.G) * scale),
		B: uint8(float64(c.B) * scale),
		A: a,
	}
}

// Hex は #RRGGBB 形式の色をパースする
func Hex(s string) color.RGBA {
	if len(s) == 7 && s[0] == '#' {
		return color.RGBA{
			R: hexByte(s[1], s[2]),
			G: hexByte(s[3], s[4]),
			B: hexByte(s[5], s[6]),
			A: 255,
		}
	}
	return color.RGBA{255, 255, 255, 255}
}

func hexByte(hi, lo byte) uint8 {
	return hexNibble(hi)<<4 | hexNibble(lo)
}

func hexNibble(b byte) uint8 {
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

// SpeedColor は速度に応じたゲージ色を返す
// < 10 km/h は燃費 MAP 表示閾値 (fuel.go: minDisplaySpeedKm) と同じ
func SpeedColor(v float64) color.RGBA {
	switch {
	case v >= 130:
		return Hex("#f44336") // 赤
	case v >= 120:
		return Hex("#ff9800") // 橙
	case v >= 100:
		return Hex("#ffeb3b") // 黄
	case v >= 80:
		return Hex("#76ff03") // 黄緑（高速）
	case v >= 60:
		return Hex("#69f0ae") // 緑（巡航）
	case v >= 30:
		return Hex("#26c6da") // 水色（市街地）
	case v >= 10:
		return Hex("#42a5f5") // 青（低速）
	default:
		return Hex("#78909c") // 停車・非アクティブ
	}
}

// RPMColor は回転数に応じた色を返す
// ZJ-VE 1.3L (91PS/6000rpm, 124Nm/3500rpm) の実用域に合わせた段階的閾値
func RPMColor(rpm float64) color.RGBA {
	switch {
	case rpm >= 5000:
		return Hex("#f44336") // 赤
	case rpm >= 4000:
		return Hex("#ff9800") // 橙
	case rpm >= 3500:
		return Hex("#fdd835") // 黄
	case rpm >= 3000:
		return Hex("#76ff03") // 黄緑（パワーバンド突入）
	case rpm >= 2000:
		return Hex("#69f0ae") // 緑（通常走行）
	case rpm >= 1500:
		return Hex("#26c6da") // 水色（街中走行・低速ギア）
	case rpm >= 1000:
		return Hex("#42a5f5") // 青（アイドル付近）
	default:
		return Hex("#78909c") // < 1000 RPM は非アクティブ
	}
}

// ThrottleColor はスロットル開度に応じた色を返す（dimZone 対応）
func ThrottleColor(pct float64, active bool) color.RGBA {
	const dimZone = 5.0
	if !active {
		return Hex("#333333")
	}
	hue := 210 - (pct/100)*210
	if pct < dimZone {
		dim := pct / dimZone
		lum := 15 + dim*40
		sat := dim * 100
		return HSL(hue, sat, lum)
	}
	if hue < 5 {
		return Hex("#f44336")
	}
	return HSL(hue, 100, 55)
}

// formatComma は整数にカンマ区切りを付ける
func formatComma(n int) string {
	if n < 0 {
		return "-" + formatComma(-n)
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%s,%03d", formatComma(n/1000), n%1000)
}
