package sdlui

import (
	"fmt"
	"image/color"
	"math"
)

// Color ヘルパー関数群（image/color.RGBA ベース）
// canvas ライブラリと直接互換

// HSL→RGB 変換（h: 0-360, s: 0-100, l: 0-100）
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
func SpeedColor(v float64) color.RGBA {
	switch {
	case v >= 120:
		return Hex("#f44336")
	case v >= 100:
		return Hex("#ff9800")
	case v >= 80:
		return Hex("#ffeb3b")
	case v >= 60:
		return Hex("#69f0ae")
	case v >= 30:
		return Hex("#42a5f5")
	default:
		return Hex("#78909c")
	}
}

// RPMColor は回転数に応じた色を返す
func RPMColor(rpm float64) color.RGBA {
	switch {
	case rpm >= 6500:
		return Hex("#f44336")
	case rpm >= 4500:
		return Hex("#ff9800")
	case rpm >= 3000:
		return Hex("#fdd835")
	case rpm >= 1500:
		return Hex("#69f0ae")
	default:
		return Hex("#42a5f5")
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
