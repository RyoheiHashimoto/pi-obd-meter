package sdlui

import (
	"math"

	"github.com/veandco/go-sdl2/sdl"
)

// DrawLeafIcon は葉アイコン（ECO）を描画する
// ブラウザ版: stroke outline + vein + stem, rotate(60°), scale(size/20)
func DrawLeafIcon(renderer *sdl.Renderer, cx, cy, size float64, color RGBA) {
	scale := size / 20.0
	cos60 := math.Cos(60 * degToRad)
	sin60 := math.Sin(60 * degToRad)

	// ベジエ曲線からサンプリングした葉の輪郭（indicators.js ICON_LEAF 相当）
	outline := [][2]float64{
		{0, -12}, {-3.2, -6.4}, {-5.4, -1.4}, {-6.6, 3.0}, {-7, 7},
		{-6.1, 9.9}, {-3.6, 12.1}, {0, 13},
		{3.6, 12.1}, {6.1, 9.9}, {7, 7},
		{6.6, 3.0}, {5.4, -1.4}, {3.2, -6.4},
	}

	tr := func(x, y float64) (float64, float64) {
		sx, sy := x*scale, y*scale
		return cx + sx*cos60 - sy*sin60, cy + sx*sin60 + sy*cos60
	}

	// アウトライン
	n := len(outline)
	for i := 0; i < n; i++ {
		x1, y1 := tr(outline[i][0], outline[i][1])
		x2, y2 := tr(outline[(i+1)%n][0], outline[(i+1)%n][1])
		DrawThickLine(renderer, x1, y1, x2, y2, 1.5, color)
	}

	// 葉脈（中心線）
	vx1, vy1 := tr(0, -8)
	vx2, vy2 := tr(0, 10)
	DrawThickLine(renderer, vx1, vy1, vx2, vy2, 1.5, color)

	// 茎
	sx1, sy1 := tr(0, 13)
	sx2, sy2 := tr(0, 18)
	DrawThickLine(renderer, sx1, sy1, sx2, sy2, 1.5, color)
}

// DrawThermoIcon は温度計アイコン（TEMP）を描画する（塗りつぶし）
// ブラウザ版: createIconPath(ICON_THERMO, 40) — filled
func DrawThermoIcon(renderer *sdl.Renderer, cx, cy, size float64, color RGBA) {
	s := size / 24.0
	// 管（縦線）: y=-10 〜 +1
	tubeTop := cy - 10*s
	tubeBot := cy + 1*s
	DrawThickLine(renderer, cx, tubeTop, cx, tubeBot, 4*s, color)
	DrawCircleFilled(renderer, cx, tubeTop, 2*s, color) // 上端キャップ
	// 球（下部）: center y=+5, r=5
	DrawCircleFilled(renderer, cx, cy+5*s, 5*s, color)
}

// DrawRoadIcon は道路アイコン（TRIP）を描画する（塗りつぶし）
// ブラウザ版: createIconPath(ICON_ROAD, 40) — filled
func DrawRoadIcon(renderer *sdl.Renderer, cx, cy, size float64, color RGBA) {
	s := size / 24.0
	h := 10.0 * s
	// 左右の路肩線（上が狭く、下が広い遠近法）
	DrawThickLine(renderer, cx-3*s, cy-h, cx-8*s, cy+h, 2*s, color)
	DrawThickLine(renderer, cx+3*s, cy-h, cx+8*s, cy+h, 2*s, color)
	// 中央の破線（3本）
	dh := 3.0 * s
	gap := 2.5 * s
	for i := 0; i < 3; i++ {
		dy := cy - h + 1.5*s + float64(i)*(dh+gap)
		DrawThickLine(renderer, cx, dy, cx, dy+dh, 1.5*s, color)
	}
}

// DrawDropletIcon は雫アイコン（OIL）を描画する（塗りつぶし）
// ブラウザ版: createIconPath(ICON_OIL, 40) — filled
func DrawDropletIcon(renderer *sdl.Renderer, cx, cy, size float64, color RGBA) {
	s := size / 24.0
	// 雫形状のポリゴン（上が尖り、下が丸い）
	pts := [][2]float64{
		{0, -10},
		{-2, -6}, {-4, -2}, {-5.5, 1}, {-6, 4},
		{-5.5, 7}, {-4, 9}, {-2, 10.5}, {0, 11},
		{2, 10.5}, {4, 9}, {5.5, 7}, {6, 4},
		{5.5, 1}, {4, -2}, {2, -6},
	}
	// fan triangulation（先端から扇状に三角形分割）
	sc := color.ToSDLColor()
	for i := 1; i < len(pts)-1; i++ {
		verts := []sdl.Vertex{
			{Position: sdl.FPoint{X: float32(cx + pts[0][0]*s), Y: float32(cy + pts[0][1]*s)}, Color: sc},
			{Position: sdl.FPoint{X: float32(cx + pts[i][0]*s), Y: float32(cy + pts[i][1]*s)}, Color: sc},
			{Position: sdl.FPoint{X: float32(cx + pts[i+1][0]*s), Y: float32(cy + pts[i+1][1]*s)}, Color: sc},
		}
		renderer.RenderGeometry(nil, verts, []int32{0, 1, 2})
	}
}
