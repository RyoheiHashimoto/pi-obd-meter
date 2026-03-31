package sdlui

import (
	"fmt"
	"log/slog"
	"math"
	"runtime"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

const (
	WindowWidth  = 800
	WindowHeight = 480
)

// DataProvider はリアルタイムデータを提供するインターフェース
type DataProvider func() GaugeData

// GaugeData は描画に必要なデータ
type GaugeData struct {
	SpeedKmh    float64
	RPM         float64
	ThrottlePos float64
	IntakeMAP   float64
	CoolantTemp float64
	FuelEconomy float64
	AvgFuelEco  float64
	TripKm      float64
	Gear        int
	ATRangeStr  string
	Hold        bool
	TCLocked    bool
	OilAlert    string
	OilRemainKm float64
	OBDConnected bool
}

// RendererConfig はレンダラー設定
type RendererConfig struct {
	MaxSpeed      float64
	FontDir       string // フォントディレクトリ
	Demo          bool   // デモモード
}

// Renderer は SDL2 レンダラー
type Renderer struct {
	cfg      RendererConfig
	window   *sdl.Window
	renderer *sdl.Renderer
	fm       *FontManager
	gauge    *SpeedGauge
	provider DataProvider
	running  bool
	stopCh   chan struct{}
}

// NewRenderer は新しい SDL2 レンダラーを作成する（まだ開始しない）
func NewRenderer(cfg RendererConfig, provider DataProvider) *Renderer {
	return &Renderer{
		cfg:      cfg,
		provider: provider,
		stopCh:   make(chan struct{}),
	}
}

// Run はSDLイベントループを開始する（メイン goroutine から呼ぶこと）
// ブロッキング。Stop() が呼ばれるか、ウィンドウが閉じられるまで戻らない。
func (r *Renderer) Run() error {
	runtime.LockOSThread()

	if err := sdl.Init(sdl.INIT_VIDEO | sdl.INIT_EVENTS); err != nil {
		return fmt.Errorf("SDL初期化失敗: %w", err)
	}
	defer sdl.Quit()

	if err := ttf.Init(); err != nil {
		return fmt.Errorf("SDL_ttf初期化失敗: %w", err)
	}
	defer ttf.Quit()

	window, err := sdl.CreateWindow(
		"DYデミオ メーター",
		sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED,
		WindowWidth, WindowHeight,
		sdl.WINDOW_SHOWN,
	)
	if err != nil {
		return fmt.Errorf("ウィンドウ作成失敗: %w", err)
	}
	defer window.Destroy()
	r.window = window

	renderer, err := sdl.CreateRenderer(window, -1, sdl.RENDERER_ACCELERATED|sdl.RENDERER_PRESENTVSYNC)
	if err != nil {
		return fmt.Errorf("レンダラー作成失敗: %w", err)
	}
	defer renderer.Destroy()
	r.renderer = renderer

	renderer.SetDrawBlendMode(sdl.BLENDMODE_BLEND)

	// フォント読み込み
	fm := NewFontManager(renderer)
	defer fm.Destroy()
	r.fm = fm

	orbitronPath := r.cfg.FontDir + "/Orbitron-Black.ttf"
	shareTechPath := r.cfg.FontDir + "/ShareTechMono-Regular.ttf"

	// Orbitron の各サイズ
	for _, size := range []int{84, 52, 48, 40, 28} {
		if err := fm.LoadFont(orbitronPath, size); err != nil {
			// woff2 は SDL_ttf 非対応の場合あり。Orbitron は TTF を使う
			slog.Warn("フォント読み込み失敗、継続", "path", orbitronPath, "size", size, "error", err)
		}
	}
	// Share Tech Mono の各サイズ
	for _, size := range []int{28, 24} {
		if err := fm.LoadFont(shareTechPath, size); err != nil {
			slog.Warn("フォント読み込み失敗、継続", "path", shareTechPath, "size", size, "error", err)
		}
	}

	// 速度ゲージ作成
	r.gauge = NewSpeedGauge(renderer, fm, GaugeConfig{
		CX:            280,
		CY:            270,
		Radius:        230,
		MaxSpeed:      r.cfg.MaxSpeed,
		OrbitronPath:  orbitronPath,
		ShareTechPath: shareTechPath,
	})
	defer r.gauge.Destroy()

	slog.Info("SDL2メーター起動", "width", WindowWidth, "height", WindowHeight)

	// デモ用
	var demoT float64

	r.running = true
	for r.running {
		// イベント処理
		for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			switch e := event.(type) {
			case *sdl.QuitEvent:
				r.running = false
			case *sdl.KeyboardEvent:
				if e.Type == sdl.KEYDOWN && e.Keysym.Sym == sdl.K_ESCAPE {
					r.running = false
				}
			}
		}

		// stop シグナル確認
		select {
		case <-r.stopCh:
			r.running = false
			continue
		default:
		}

		// データ取得
		var data GaugeData
		if r.cfg.Demo {
			demoT += 1.0 / 60.0
			data = demoData(demoT)
		} else if r.provider != nil {
			data = r.provider()
		}

		// LERP 更新
		r.gauge.SetTarget(data.SpeedKmh)
		r.gauge.Update()

		// 描画
		renderer.SetDrawColor(0, 0, 0, 255)
		renderer.Clear()

		r.gauge.Draw(renderer)

		renderer.Present()
	}

	return nil
}

// Stop はレンダリングループを停止する（別 goroutine から呼べる）
func (r *Renderer) Stop() {
	select {
	case r.stopCh <- struct{}{}:
	default:
	}
}

// demoData はデモ用のサイン波データを生成する
func demoData(t float64) GaugeData {
	// 速度: 0-140 km/h をゆっくり変動
	speed := 70 + 70*math.Sin(t*0.3)
	rpm := 800 + speed*35
	throttle := 5 + 40*math.Max(0, math.Sin(t*0.5))

	gear := 1
	switch {
	case speed > 100:
		gear = 4
	case speed > 60:
		gear = 3
	case speed > 30:
		gear = 2
	}

	return GaugeData{
		SpeedKmh:     speed,
		RPM:          rpm,
		ThrottlePos:  throttle,
		IntakeMAP:    30 + throttle*0.7,
		CoolantTemp:  88,
		FuelEconomy:  8 + 4*math.Sin(t*0.4),
		AvgFuelEco:   9.5,
		TripKm:       120.3 + t*0.01,
		Gear:         gear,
		ATRangeStr:   "D",
		Hold:         false,
		TCLocked:     speed > 50,
		OilAlert:     "green",
		OilRemainKm:  2800,
		OBDConnected: true,
	}
}

// RunDemo はデモモードでレンダラーを起動する（テスト用エントリポイント）
func RunDemo(fontDir string, maxSpeed float64) error {
	r := NewRenderer(RendererConfig{
		MaxSpeed: maxSpeed,
		FontDir:  fontDir,
		Demo:     true,
	}, nil)
	return r.Run()
}

