package sdlui

import (
	"fmt"
	"image/color"

	"github.com/tdewolff/canvas"
)

// canvas ベースのフォント + テキスト描画

// CanvasFonts は canvas.FontFamily を保持
type CanvasFonts struct {
	Orbitron  *canvas.FontFamily
	ShareTech *canvas.FontFamily
}

// LoadCanvasFonts は canvas 用のフォントを読み込む
func LoadCanvasFonts(fontDir string) (*CanvasFonts, error) {
	f := &CanvasFonts{}
	f.Orbitron = canvas.NewFontFamily("Orbitron")
	if err := f.Orbitron.LoadFontFile(fontDir+"/Orbitron-Black.ttf", canvas.FontBlack); err != nil {
		return nil, fmt.Errorf("Orbitron 読み込み失敗: %w", err)
	}
	f.ShareTech = canvas.NewFontFamily("ShareTechMono")
	if err := f.ShareTech.LoadFontFile(fontDir+"/ShareTechMono-Regular.ttf", canvas.FontRegular); err != nil {
		return nil, fmt.Errorf("ShareTechMono 読み込み失敗: %w", err)
	}
	return f, nil
}

// face は適切な FontStyle で FontFace を取得
func (f *CanvasFonts) face(fam *canvas.FontFamily, sizePx float64, col color.RGBA) *canvas.FontFace {
	if fam == f.Orbitron {
		return fam.Face(sizePx*pxToPt, col, canvas.FontBlack)
	}
	// ShareTechMono はブラウザ版も font-weight: 700 で表示（faux bold）
	return fam.Face(sizePx*pxToPt, col, canvas.FontBold)
}

// drawTextCentered は視覚中央を (x, screenY) に配置（dominant-baseline:middle 相当）
func (f *CanvasFonts) drawTextCentered(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string) {
	face := f.face(fam, sizePx, col)
	txt := canvas.NewTextLine(face, text, canvas.Center)
	yUp := canvasScreenH - screenY
	m := face.Metrics()
	yBaseline := yUp - (m.Ascent-m.Descent)/2
	ctx.DrawText(x, yBaseline, txt)
}

// drawTextBaseline はベースラインを (x, screenY) に配置（SVG text-anchor:middle 相当）
func (f *CanvasFonts) drawTextBaseline(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string) {
	face := f.face(fam, sizePx, col)
	txt := canvas.NewTextLine(face, text, canvas.Center)
	yUp := canvasScreenH - screenY
	ctx.DrawText(x, yUp, txt)
}

// drawTextBaselineShadow はドロップシャドウ付きのベースラインテキスト
// (C) 大きい数字を浮き上がらせる
func (f *CanvasFonts) drawTextBaselineShadow(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string) {
	// 影 (黒 60%)
	shadowCol := color.RGBA{0, 0, 0, 153}
	f.drawTextBaseline(ctx, fam, sizePx, shadowCol, x+2, screenY+3, text)
	// 本体
	f.drawTextBaseline(ctx, fam, sizePx, col, x, screenY, text)
}

// drawTextRight はテキストを右揃え・ベースライン基準で描画
func (f *CanvasFonts) drawTextRight(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, rx, screenY float64, text string) {
	face := f.face(fam, sizePx, col)
	txt := canvas.NewTextLine(face, text, canvas.Right)
	yUp := canvasScreenH - screenY
	ctx.DrawText(rx, yUp, txt)
}

// drawGlowTextBaseline はグロー付きベースラインテキスト
func (f *CanvasFonts) drawGlowTextBaseline(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string) {
	f.drawTextOffsets(ctx, fam, sizePx, col, x, screenY, text, canvas.Center, false)
	f.drawTextBaseline(ctx, fam, sizePx, col, x, screenY, text)
}

// drawGlowTextCentered はグロー付き視覚中央テキスト
func (f *CanvasFonts) drawGlowTextCentered(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string) {
	f.drawTextOffsets(ctx, fam, sizePx, col, x, screenY, text, canvas.Center, true)
	f.drawTextCentered(ctx, fam, sizePx, col, x, screenY, text)
}

// drawTextOffsets は 8 方向オフセット + 2 層でグロー風にテキスト描画
func (f *CanvasFonts) drawTextOffsets(ctx *canvas.Context, fam *canvas.FontFamily, sizePx float64, col color.RGBA, x, screenY float64, text string, halign canvas.TextAlign, centered bool) {
	dirs := []struct{ dx, dy float64 }{
		{0, -1}, {0.707, -0.707}, {1, 0}, {0.707, 0.707},
		{0, 1}, {-0.707, 0.707}, {-1, 0}, {-0.707, -0.707},
	}
	layers := []struct {
		dist  float64
		alpha uint8
	}{
		{4, 10},
		{2.2, 25},
	}
	for _, l := range layers {
		c := WithAlpha(col, l.alpha)
		for _, d := range dirs {
			ox := x + d.dx*l.dist
			oy := screenY + d.dy*l.dist
			if centered {
				f.drawTextCentered(ctx, fam, sizePx, c, ox, oy, text)
			} else {
				// ベースライン基準のオフセット
				face := f.face(fam, sizePx, c)
				txt := canvas.NewTextLine(face, text, halign)
				yUp := canvasScreenH - oy
				ctx.DrawText(ox, yUp, txt)
			}
		}
	}
}
