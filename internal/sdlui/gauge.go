package sdlui

import (
	"fmt"
	"math"

	"github.com/veandco/go-sdl2/sdl"
)

// GaugeConfig は速度ゲージの設定
type GaugeConfig struct {
	CX, CY         float64 // 中心座標
	Radius          float64 // 速度トラック半径
	MaxSpeed        float64 // 最大速度 (km/h)
	OrbitronPath    string  // Orbitron フォントパス
	ShareTechPath   string  // Share Tech Mono フォントパス
}

// SpeedGauge は速度ゲージの描画状態を保持する
type SpeedGauge struct {
	cfg          GaugeConfig
	fm           *FontManager
	staticTex    *sdl.Texture // 静的レイヤーテクスチャ
	currentSpeed float64      // LERP 補間中の表示速度
	targetSpeed  float64      // 目標速度
	lastColor    RGBA         // 前フレームの色（テキストキャッシュ無効化判定用）
}

// 定数（gauge.js と同一）
const (
	arcStart = -135.0
	arcEnd   = 135.0
	arcSweep = 270.0

	trackWidth    = 16.0
	needleWidth   = 6.0
	needleGap     = 24.0 // 中心からのオフセット
	centerDotR    = 8.0
	tickMajorLen  = 30.0
	tickMinorLen  = 18.0
	tickOuterGap  = 4.0
	labelOffset   = 54.0 // 速度ラベルのトラック内側オフセット
)

// 色定数
var (
	colorTrack     = Hex("#181820")
	colorTickMajor = Hex("#aaaaaa")
	colorTickMinor = Hex("#444444")
	colorTickLabel = Hex("#ffffff")
	colorCenterDot = Hex("#1a1a22")
	colorCenterRim = Hex("#444444")
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

// buildStaticTexture は静的レイヤー（トラック、目盛り、数値）をテクスチャにベイクする
func (g *SpeedGauge) buildStaticTexture(renderer *sdl.Renderer) {
	// ターゲットテクスチャを作成
	tex, err := renderer.CreateTexture(
		sdl.PIXELFORMAT_RGBA8888,
		sdl.TEXTUREACCESS_TARGET,
		800, 480,
	)
	if err != nil {
		return
	}
	tex.SetBlendMode(sdl.BLENDMODE_BLEND)

	renderer.SetRenderTarget(tex)
	renderer.SetDrawColor(0, 0, 0, 0)
	renderer.Clear()

	cx, cy, r := g.cfg.CX, g.cfg.CY, g.cfg.Radius

	// 速度トラック（暗いアーク）
	DrawArc(renderer, cx, cy, r-trackWidth/2, r+trackWidth/2, arcStart, arcEnd, colorTrack)

	// 目盛り描画
	maxSpd := g.cfg.MaxSpeed
	majorInterval := 20.0
	for spd := 0.0; spd <= maxSpd; spd += majorInterval {
		angle := arcStart + (spd/maxSpd)*arcSweep
		outerR := r + tickOuterGap
		// Major tick
		innerR := outerR - tickMajorLen
		ox, oy := polarToXY(cx, cy, outerR, angle)
		ix, iy := polarToXY(cx, cy, innerR, angle)
		DrawThickLine(renderer, ix, iy, ox, oy, 5, colorTickMajor)

		// 速度ラベル
		labelR := r - labelOffset
		lx, ly := polarToXY(cx, cy, labelR, angle)
		label := fmt.Sprintf("%d", int(spd))
		g.fm.DrawTextCentered(label, g.cfg.ShareTechPath, 28, colorTickLabel, lx, ly)

		// Minor ticks（major間を4分割）
		if spd < maxSpd {
			for j := 1; j < 4; j++ {
				minSpd := spd + majorInterval*float64(j)/4.0
				if minSpd > maxSpd {
					break
				}
				minAngle := arcStart + (minSpd/maxSpd)*arcSweep
				minOuter := outerR
				minInner := outerR - tickMinorLen
				mx, my := polarToXY(cx, cy, minOuter, minAngle)
				mix, miy := polarToXY(cx, cy, minInner, minAngle)
				DrawThickLine(renderer, mix, miy, mx, my, 2.5, colorTickMinor)
			}
		}
	}

	renderer.SetRenderTarget(nil)
	g.staticTex = tex
}

// SetTarget はターゲット速度を設定する
func (g *SpeedGauge) SetTarget(speed float64) {
	g.targetSpeed = Clamp(speed, 0, g.cfg.MaxSpeed)
}

// Update は1フレーム分の LERP 補間を行う
func (g *SpeedGauge) Update() {
	g.currentSpeed = Lerp(g.currentSpeed, g.targetSpeed, LerpSpeed)
}

// Draw は速度ゲージを描画する
func (g *SpeedGauge) Draw(renderer *sdl.Renderer) {
	cx, cy, r := g.cfg.CX, g.cfg.CY, g.cfg.Radius

	// 静的レイヤー
	if g.staticTex != nil {
		renderer.Copy(g.staticTex, nil, nil)
	}

	speed := g.currentSpeed
	color := SpeedColor(speed)

	// 速度アーク（動的）
	if speed > 0.5 {
		pct := speed / g.cfg.MaxSpeed
		endAngle := arcStart + pct*arcSweep
		DrawArc(renderer, cx, cy, r-trackWidth/2, r+trackWidth/2, arcStart, endAngle, color)
	}

	// 針
	angle := arcStart + (speed/g.cfg.MaxSpeed)*arcSweep
	needleTipR := r - needleGap
	nx1, ny1 := polarToXY(cx, cy, -16, angle) // 中心を少し越える
	nx2, ny2 := polarToXY(cx, cy, needleTipR, angle)

	// 針のグロー（太い半透明線）
	DrawThickLine(renderer, nx1, ny1, nx2, ny2, needleWidth+8, color.WithAlpha(60))
	// 針本体
	DrawThickLine(renderer, nx1, ny1, nx2, ny2, needleWidth, color)

	// 中心ドット
	DrawCircleFilled(renderer, cx, cy, centerDotR+2, colorCenterRim)
	DrawCircleFilled(renderer, cx, cy, centerDotR, colorCenterDot)

	// 速度数値
	speedText := fmt.Sprintf("%d", int(math.Round(speed)))
	if color != g.lastColor {
		g.fm.InvalidateCache()
		g.lastColor = color
	}
	g.fm.DrawTextCentered(speedText, g.cfg.OrbitronPath, 84, color, cx, cy+95)

	// km/h 単位
	g.fm.DrawTextCentered("km/h", g.cfg.ShareTechPath, 28, Hex("#aaaaaa"), cx, cy+130)
}

// Destroy はリソースを解放する
func (g *SpeedGauge) Destroy() {
	if g.staticTex != nil {
		g.staticTex.Destroy()
	}
}
