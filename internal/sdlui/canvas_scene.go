package sdlui

import (
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"math"
	"strings"
	"time"
	"unsafe"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/rasterizer"
	"github.com/veandco/go-sdl2/sdl"
)

// 起動時アニメーション設定
const (
	bootSweepOutDur  = 1.2 // 針 0→MAX
	bootSweepBackDur = 0.8 // 針 MAX→0
	bootFadeInDur    = 0.8 // 文字 dim→通常色
	bootTotalDur     = bootSweepOutDur + bootSweepBackDur + bootFadeInDur
)

// SceneConfig は CanvasScene の初期設定
type SceneConfig struct {
	MaxSpeed        float64
	ThrottleIdlePct float64
	ThrottleMaxPct  float64
	FontDir         string
}

// element は 1 つの描画要素（static も dynamic も共通）
type element struct {
	name   string
	bounds sdl.Rect
	tex    *sdl.Texture
}

// premultBlend は premultiplied alpha 用のカスタム blend mode
// canvas は premultiplied alpha 出力なので、SDL 側もそれに合わせる
// dst = src + dst * (1 - srcA)
var premultBlend = sdl.ComposeCustomBlendMode(
	sdl.BLENDFACTOR_ONE,
	sdl.BLENDFACTOR_ONE_MINUS_SRC_ALPHA,
	sdl.BLENDOPERATION_ADD,
	sdl.BLENDFACTOR_ONE,
	sdl.BLENDFACTOR_ONE_MINUS_SRC_ALPHA,
	sdl.BLENDOPERATION_ADD,
)

func newElement(ren *sdl.Renderer, name string, x, y, w, h int32) (*element, error) {
	tex, err := ren.CreateTexture(
		sdl.PIXELFORMAT_ABGR8888,
		sdl.TEXTUREACCESS_STREAMING,
		w, h,
	)
	if err != nil {
		return nil, fmt.Errorf("texture作成失敗 %s: %w", name, err)
	}
	if err := tex.SetBlendMode(premultBlend); err != nil {
		// フォールバック: 通常の blend
		if err2 := tex.SetBlendMode(sdl.BLENDMODE_BLEND); err2 != nil {
			return nil, fmt.Errorf("blendmode設定失敗 %s: %w", name, err)
		}
	}
	return &element{
		name:   name,
		bounds: sdl.Rect{X: x, Y: y, W: w, H: h},
		tex:    tex,
	}, nil
}

func (e *element) destroy() {
	if e.tex != nil {
		_ = e.tex.Destroy()
	}
}

// commit は描画済み canvas.Canvas をラスタライズして SDL テクスチャに転送
func (e *element) commit(c *canvas.Canvas) error {
	img := rasterizer.Draw(c, canvas.DPMM(1), canvas.DefaultColorSpace)
	return e.tex.Update(nil, unsafe.Pointer(&img.Pix[0]), img.Stride)
}

// newCanvas は要素サイズの canvas + context を作り、ローカル座標でグローバル画面座標が描けるよう Translate する
// 呼び出し側で描画関数にグローバル画面座標を渡せば、内部で screenH ベースの Y-up 変換 → Translate でローカルに落ちる
func (e *element) newCanvas() (*canvas.Canvas, *canvas.Context) {
	w := float64(e.bounds.W)
	h := float64(e.bounds.H)
	c := canvas.New(w, h)
	ctx := canvas.NewContext(c)
	// グローバル画面座標で drawX を入れたら、drawX → screenToUp(drawX) = (drawX, canvasScreenH - drawY) で
	// グローバル Y-up 座標になる。これを要素ローカル (0..w, 0..h) に変換するため Translate。
	// グローバル Y-up (gx, gy) → ローカル Y-up (gx - bounds.X, gy - (canvasScreenH - bounds.Y - h))
	// = (gx - bounds.X, gy - canvasScreenH + bounds.Y + h)
	tx := -float64(e.bounds.X)
	ty := -canvasScreenH + float64(e.bounds.Y) + h
	ctx.SetCoordSystem(canvas.CartesianI)
	ctx.Translate(tx, ty)
	return c, ctx
}

// CanvasScene は element-based な描画シーン
type CanvasScene struct {
	renderer *sdl.Renderer
	cfg      SceneConfig
	fonts    *CanvasFonts

	// 状態（LERP 値）
	curSpeed, tgtSpeed float64
	curRPM, tgtRPM     float64
	curThr, tgtThr     float64
	curBar, tgtBar     float64

	// 非補間の状態
	gear         int
	atRange      string
	hold         bool
	tcLocked     bool
	coolantTemp  float64
	tripKm       float64
	oilAlert     string
	oilCurrentKm float64
	fuelEco      float64
	avgFuelEco   float64
	intakeMAP    float64

	// 起動アニメ
	startTime  time.Time
	bootDone   bool
	fadeFactor float64 // 0=消灯, 1=通常

	// 前回の state（dirty 判定用）
	lastSpdInt  int
	lastRPMInt  int
	lastGear    int
	lastRange   string
	lastHold    bool
	lastTCLock  bool
	lastBarStr  string
	lastEcoStr  string
	lastTempStr string
	lastTripStr string
	lastOilStr  string
	lastEcoCol  color.RGBA
	lastTempCol color.RGBA
	lastTripCol color.RGBA
	lastOilCol  color.RGBA
	initialized bool

	// Elements
	bgEl          *element
	labelsEl      *element // 目盛り数字・単位ラベル（静的、起動中は ColorMod で暗く）
	speedArcEl    *element
	speedNeedleEl *element
	speedNumEl    *element
	rpmArcEl      *element
	rpmNumEl      *element
	thrArcEl      *element
	thrLabelEl    *element
	rangeBoxEl    *element
	gearBoxEl     *element
	vacArcEl      *element
	vacNeedleEl   *element
	vacValueEl    *element
	vacLabelEl    *element
	indEcoEl      *element
	indTempEl     *element
	indTripEl     *element
	indOilEl      *element
}

// NewCanvasScene は新しい scene を初期化（背景のベイクまで完了）
func NewCanvasScene(renderer *sdl.Renderer, cfg SceneConfig) (*CanvasScene, error) {
	fonts, err := LoadCanvasFonts(cfg.FontDir)
	if err != nil {
		return nil, err
	}

	s := &CanvasScene{
		renderer: renderer,
		cfg:      cfg,
		fonts:    fonts,
	}

	// --- 要素を作成 ---
	type elemDef struct {
		name       string
		x, y, w, h int32
		dest       **element
	}
	defs := []elemDef{
		// 静的背景（トラックのみ、常に表示）
		{"bg", 0, 0, 800, 480, &s.bgEl},
		// 静的ラベル（目盛り数字・単位、起動中は ColorMod で暗く）
		{"labels", 0, 0, 800, 480, &s.labelsEl},
		// 速度ゲージ dynamic（針・アークが動く、外側グロー分を含めて広め）
		{"speedArc", 0, 0, 560, 480, &s.speedArcEl},
		{"speedNeedle", 0, 0, 560, 480, &s.speedNeedleEl},
		{"speedNum", 100, 280, 350, 140, &s.speedNumEl},
		{"rpmArc", 0, 0, 560, 480, &s.rpmArcEl},
		{"rpmNum", 100, 150, 350, 100, &s.rpmNumEl},
		{"thrArc", 80, 80, 400, 400, &s.thrArcEl},
		{"thrLabel", 100, 400, 350, 70, &s.thrLabelEl},
		// ギア/レンジ枠
		{"rangeBox", 0, 0, 100, 110, &s.rangeBoxEl},
		{"gearBox", 460, 0, 100, 110, &s.gearBoxEl},
		// バキューム
		{"vacArc", 530, 0, 270, 320, &s.vacArcEl},
		{"vacNeedle", 530, 0, 270, 320, &s.vacNeedleEl},
		{"vacValue", 560, 165, 200, 80, &s.vacValueEl},
		{"vacLabel", 560, 100, 200, 50, &s.vacLabelEl},
		// 4行インジケーター
		{"indEco", 530, 275, 270, 60, &s.indEcoEl},
		{"indTemp", 530, 324, 270, 60, &s.indTempEl},
		{"indTrip", 530, 373, 270, 60, &s.indTripEl},
		{"indOil", 530, 422, 270, 58, &s.indOilEl},
	}
	for _, d := range defs {
		e, err := newElement(renderer, d.name, d.x, d.y, d.w, d.h)
		if err != nil {
			return nil, err
		}
		*d.dest = e
	}

	// 初期状態
	s.curBar = -1.0
	s.tgtBar = -1.0
	s.atRange = ""
	s.lastSpdInt = -1
	s.lastRPMInt = -1
	s.startTime = time.Now()
	s.fadeFactor = 0

	// 静的背景 + ラベルをベイク
	if err := s.renderBackground(); err != nil {
		return nil, fmt.Errorf("背景ベイク失敗: %w", err)
	}
	if err := s.renderLabels(); err != nil {
		return nil, fmt.Errorf("ラベルベイク失敗: %w", err)
	}

	slog.Info("CanvasScene 初期化完了", "elements", len(defs))
	return s, nil
}

// SetTargets は OBD データから目標値を設定（次の Update() で反映）
func (s *CanvasScene) SetTargets(data GaugeData) {
	s.tgtSpeed = clamp(data.SpeedKmh, 0, s.cfg.MaxSpeed)
	s.tgtRPM = clamp(data.RPM, 0, rpmMaxVal)
	rng := s.cfg.ThrottleMaxPct - s.cfg.ThrottleIdlePct
	if rng > 0 {
		s.tgtThr = clamp((data.ThrottlePos-s.cfg.ThrottleIdlePct)/rng*100, 0, 100)
	} else {
		s.tgtThr = 0
	}
	s.tgtBar = clamp((data.IntakeMAP-101.3)/100, vacMin, vacMax)

	s.gear = data.Gear
	s.atRange = data.ATRangeStr
	s.hold = data.Hold
	s.tcLocked = data.TCLocked
	s.coolantTemp = data.CoolantTemp
	s.tripKm = data.TripKm
	s.oilAlert = data.OilAlert
	s.oilCurrentKm = data.OilCurrentKm
	s.fuelEco = data.FuelEconomy
	s.avgFuelEco = data.AvgFuelEco
	s.intakeMAP = data.IntakeMAP
}

// Update は LERP 補間 + 変更された要素を再描画
func (s *CanvasScene) Update() {
	// --- 起動アニメーション ---
	if !s.bootDone {
		elapsed := time.Since(s.startTime).Seconds()
		switch {
		case elapsed < bootSweepOutDur:
			// Phase 1: 針 0 → MAX
			t := easeInOut(elapsed / bootSweepOutDur)
			s.curSpeed = s.cfg.MaxSpeed * t
			s.curRPM = rpmMaxVal * t
			s.curThr = 100 * t
			s.curBar = -1.0 + 1.0*t
			s.fadeFactor = 0
		case elapsed < bootSweepOutDur+bootSweepBackDur:
			// Phase 2: 針 MAX → 0
			t := easeInOut((elapsed - bootSweepOutDur) / bootSweepBackDur)
			s.curSpeed = s.cfg.MaxSpeed * (1 - t)
			s.curRPM = rpmMaxVal * (1 - t)
			s.curThr = 100 * (1 - t)
			s.curBar = 0.0 - 1.0*t
			s.fadeFactor = 0
		case elapsed < bootTotalDur:
			// Phase 3: 文字フェードイン
			t := (elapsed - bootSweepOutDur - bootSweepBackDur) / bootFadeInDur
			s.fadeFactor = easeInOut(t)
			s.curSpeed = 0
			s.curRPM = 0
			s.curThr = 0
			s.curBar = -1.0
		default:
			// 起動アニメ完了 → 通常動作へ
			s.bootDone = true
			s.fadeFactor = 1.0
		}

		// 起動中は全 dynamic 要素を強制再描画
		_ = s.renderSpeedArc()
		_ = s.renderSpeedNeedle()
		_ = s.renderSpeedNumber()
		_ = s.renderRPMArc()
		_ = s.renderRPMNumber()
		_ = s.renderThrottleArc()
		_ = s.renderThrottleLabel()
		_ = s.renderVacuumArc()
		_ = s.renderVacuumNeedle()
		_ = s.renderVacuumValue()
		_ = s.renderVacuumLabel()
		_ = s.renderRangeBox()
		_ = s.renderGearBox()
		_ = s.renderIndEco()
		_ = s.renderIndTemp()
		_ = s.renderIndTrip()
		_ = s.renderIndOil()
		// dirty フラグは初期化扱いのままにしておく
		s.initialized = false
		return
	}

	// --- 通常 LERP ---
	s.curSpeed = Lerp(s.curSpeed, s.tgtSpeed, LerpSpeed)
	s.curRPM = Lerp(s.curRPM, s.tgtRPM, 0.4)
	s.curThr = Lerp(s.curThr, s.tgtThr, 0.4)
	// バキュームは専用 speed
	delta := s.tgtBar - s.curBar
	if math.Abs(delta) > LerpThreshold*0.01 {
		s.curBar = s.curBar + delta*0.35
	} else {
		s.curBar = s.tgtBar
	}

	// 毎フレーム更新される dynamic 要素
	_ = s.renderSpeedArc()
	_ = s.renderSpeedNeedle()
	_ = s.renderRPMArc()
	_ = s.renderThrottleArc()
	_ = s.renderVacuumArc()
	_ = s.renderVacuumNeedle()

	// 整数値が変わった時のみ
	spdInt := int(math.Round(s.curSpeed))
	if !s.initialized || spdInt != s.lastSpdInt {
		_ = s.renderSpeedNumber()
		s.lastSpdInt = spdInt
	}
	rpmInt := int(math.Round(s.curRPM))
	if !s.initialized || rpmInt != s.lastRPMInt {
		_ = s.renderRPMNumber()
		s.lastRPMInt = rpmInt
	}
	barStr := fmt.Sprintf("%.2f", s.curBar)
	if !s.initialized || barStr != s.lastBarStr {
		_ = s.renderVacuumValue()
		s.lastBarStr = barStr
	}
	// VACUUM / THROTTLE ラベル
	_ = s.renderVacuumLabel()
	_ = s.renderThrottleLabel()

	// ギア/レンジ枠（変化時のみ）
	if !s.initialized || s.gear != s.lastGear || s.atRange != s.lastRange || s.hold != s.lastHold {
		_ = s.renderRangeBox()
		_ = s.renderGearBox()
		s.lastGear = s.gear
		s.lastRange = s.atRange
		s.lastHold = s.hold
	}
	if !s.initialized || s.tcLocked != s.lastTCLock {
		// LOCK ラベルは gearBox 内で描画されるため、ここでも再レンダ
		_ = s.renderGearBox()
		s.lastTCLock = s.tcLocked
	}

	// インジケーター（値が変わった時のみ）
	ecoStr := s.ecoDisplayText()
	ecoCol := s.ecoColor()
	if !s.initialized || ecoStr != s.lastEcoStr || ecoCol != s.lastEcoCol {
		_ = s.renderIndEco()
		s.lastEcoStr = ecoStr
		s.lastEcoCol = ecoCol
	}
	tempStr := s.tempDisplayText()
	tempCol := s.tempColor()
	if !s.initialized || tempStr != s.lastTempStr || tempCol != s.lastTempCol {
		_ = s.renderIndTemp()
		s.lastTempStr = tempStr
		s.lastTempCol = tempCol
	}
	tripStr := s.tripDisplayText()
	tripCol := s.tripColor()
	if !s.initialized || tripStr != s.lastTripStr || tripCol != s.lastTripCol {
		_ = s.renderIndTrip()
		s.lastTripStr = tripStr
		s.lastTripCol = tripCol
	}
	oilStr := s.oilDisplayText()
	oilCol := s.oilColor()
	if !s.initialized || oilStr != s.lastOilStr || oilCol != s.lastOilCol {
		_ = s.renderIndOil()
		s.lastOilStr = oilStr
		s.lastOilCol = oilCol
	}

	s.initialized = true
}

// Draw は全要素を画面に転送
func (s *CanvasScene) Draw(renderer *sdl.Renderer) {
	_ = renderer.SetDrawColor(0, 0, 0, 255)
	_ = renderer.Clear()

	// ラベルのフェード: ColorMod で #333 (51) → #fff (255) へ
	labelMod := uint8(51 + float64(255-51)*s.fadeFactor)
	_ = s.labelsEl.tex.SetColorMod(labelMod, labelMod, labelMod)

	all := []*element{
		s.bgEl,
		s.labelsEl, // 背景トラックの上、ラベルはフェード対象
		// アーク + VACUUMラベル（最下層）
		s.speedArcEl, s.rpmArcEl, s.thrArcEl, s.vacArcEl,
		s.vacLabelEl,
		// 針（アーク・VACUUMラベルの上、数字の下）
		s.speedNeedleEl, s.vacNeedleEl,
		// 数字・枠・インジケーター（最上層）
		s.speedNumEl, s.rpmNumEl, s.thrLabelEl,
		s.rangeBoxEl, s.gearBoxEl,
		s.vacValueEl,
		s.indEcoEl, s.indTempEl, s.indTripEl, s.indOilEl,
	}
	for _, e := range all {
		_ = renderer.Copy(e.tex, nil, &e.bounds)
	}
}

// Destroy は全リソースを解放
func (s *CanvasScene) Destroy() {
	all := []*element{
		s.bgEl, s.labelsEl,
		// アーク + VACUUMラベル（最下層）
		s.speedArcEl, s.rpmArcEl, s.thrArcEl, s.vacArcEl,
		s.vacLabelEl,
		// 針（アーク・VACUUMラベルの上、数字の下）
		s.speedNeedleEl, s.vacNeedleEl,
		// 数字・枠・インジケーター（最上層）
		s.speedNumEl, s.rpmNumEl, s.thrLabelEl,
		s.rangeBoxEl, s.gearBoxEl,
		s.vacValueEl,
		s.indEcoEl, s.indTempEl, s.indTripEl, s.indOilEl,
	}
	for _, e := range all {
		if e != nil {
			e.destroy()
		}
	}
}

// easeInOut は 0-1 の入力を ease-in-out カーブに通す
func easeInOut(t float64) float64 {
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// fadeColor は dim(#333) と active の間を fadeFactor で補間
func (s *CanvasScene) fadeColor(active color.RGBA) color.RGBA {
	if s.fadeFactor >= 1.0 {
		return active
	}
	if s.fadeFactor <= 0 {
		return colDim
	}
	f := s.fadeFactor
	return color.RGBA{
		R: uint8(float64(colDim.R)*(1-f) + float64(active.R)*f),
		G: uint8(float64(colDim.G)*(1-f) + float64(active.G)*f),
		B: uint8(float64(colDim.B)*(1-f) + float64(active.B)*f),
		A: 255,
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// 未使用警告を抑制するためのダミー参照
var _ = strings.Contains
var _ image.Image = (*image.RGBA)(nil)
