package main

import (
	"encoding/json"
	"log/slog"
	"os"
)

// CoolantTempConfig は水温閾値のJSON設定
type CoolantTempConfig struct {
	ColdMax    int `json:"cold_max"`    // 冷間上限 (これ未満=青)
	NormalMax  int `json:"normal_max"`  // 正常上限 (これ以下=緑)
	WarningMax int `json:"warning_max"` // 警告上限 (これ以下=橙、超=赤)
}

// OilChangeConfig はオイル交換のJSON設定
type OilChangeConfig struct {
	IntervalKm float64 `json:"interval_km"`
	WarningKm  float64 `json:"warning_km"`
	DangerKm   float64 `json:"danger_km"`
}

// WebSocketConfig はWebSocket設定
type WebSocketConfig struct {
	Enabled             bool `json:"enabled"`
	BroadcastIntervalMs int  `json:"broadcast_interval_ms"`
	MaxClients          int  `json:"max_clients"`
}

// Config はアプリケーション設定
// defaultFuelRateCorrection は燃料消費レートの補正係数の既定値。
//
// **この記述は誤っていた (2026-09-26 訂正)。** 「DYデミオはMAFを持たないため、
// 負荷×RPMから吸入空気量を推定している」と書いてあったが、DY デミオは MAF
// (PID 0x10) を持ち、実機はその経路を通っている。calcFuelEconomy の優先順位は
// MAF > MAP (Speed-Density) > 負荷×RPM で、MAF が取れている限り第1経路に入る。
//
// したがってこの係数が吸収しているのは「推定と実測の乖離」ではなく、
// MAF の系統誤差 (Bosch HFM5 の仕様で ≤3%)、燃料密度と理論空燃比の取り違え
// (750 g/L・14.7 に対し、ISO 15031-5 の例示は 14.64、国内レギュラーの実測は
// 728.5 g/L で、2つで ×1.034)、平均λのずれ、の合計である。
//
// 2026-08-31 に 1.3 → 1.07 へ改訂。給油〜給油の3区間でアプリの燃料積算を
// レシートと直接突き合わせたところ、一貫して過大だった。
//
//	区間  ODO距離   アプリ積算   レシート    比
//	 1     430km     41.16L     35.29L   1.166
//	 2     220km     23.47L     18.80L   1.249
//	 3     330km     30.72L     24.70L   1.244
//
// 平均 1.220。1.3 ÷ 1.220 = 1.066 から当初 1.07 とした。
//
// その後 2026-08-31 に 1.12 へ再調整した。減速時燃料カット中の燃料を
// 数えていた不具合 (fuel.go) を直したため、積算が 4.0% 減ったぶんを戻す
// 必要がある。1.07 はバグ込みのモデルに合わせて求めた値だったので、
// バグだけ直すと 4% の過少になる。
//
//	燃料カットを除いた積算 91.76L / レシート 78.79L = 1.165
//	1.3 ÷ 1.165 = 1.116 → 1.12 を採る
//
// この比較は走行距離を一切使っていないため、同時期に判明したトリップ距離の
// 取りこぼし (ODO比 -17.6%) からは独立している。
//
// --- 2026-09-18: 逆方向のずれを観測した (定数は据え置き) ---
//
// 給油時に前タンクの実績が取れた。366.47km 走ってレシート 33.46L。
//
//	実燃費              366.47 / 33.46 = 10.95 km/L
//	アプリの燃費表示    366.47 / 29.44 = 12.45 km/L  (+13.7% 楽観的)
//	アプリ積算 ÷ レシート = 29.44 / 33.46 = 0.880    (4.02L 少ない)
//	辻褄を合わせる係数   = 1.12 × 1.136 = 1.27
//
// 8月とは向きが逆で、今度は過少に出ている。1.12 は下げすぎだった可能性がある。
// ただし 8月も3区間を貯めてから決めた。**1回では動かさない。** この走行は
// 高速140km/h・峠・農道と負荷の幅が大きく、走り方の偏りである可能性も残る。
//
// 前タンクの実績はジャーナルの
//
//	給油によるトリップリセット prev_distance_km=... prev_fuel_l=...
//
// の行に出る。給油の検出行とは別で、検出の約10分後 (GAS 送信後) に出るので、
// 給油直後に grep すると取り逃す。次の給油でこの2行を拾って向きを確かめること。
const defaultFuelRateCorrection = 1.12

type Config struct {
	CANInterface    string `json:"can_interface"`
	SerialPort      string `json:"serial_port"`
	WebhookURL      string `json:"webhook_url"`
	PollIntervalMs  int    `json:"poll_interval_ms"`
	LocalAPIPort    int    `json:"local_api_port"`
	MaintenancePath string `json:"maintenance_path"`
	// WebStaticDir は設定ファイルからは受け取らない (`-web-dir` フラグ専用)。
	//
	// UI はバイナリに埋め込んである (web/embed.go)。設定ファイルでここに
	// パスを書けてしまうと、そのディレクトリが古いままでも必ず優先される。
	// 実際 Pi の config.json は /opt/pi-obd-meter/web/static を指しており、
	// /opt は overlayfs の上層 (tmpfs) なので再起動のたびに中身が 9/4 の版へ
	// 戻っていた。OTA でバイナリだけ新しくなっても画面は古いまま、という
	// 状態が 2026-09-05 以降ずっと続いていた (2026-09-24 に実機で確認)。
	//
	// 設定ファイルは git 管理外で OTA からも更新できないため、コード側で
	// 受け取らないようにして初めて直る。開発でファイルから配りたいときは
	// `-web-dir <path>` を渡す。
	WebStaticDir        string            `json:"-"`
	MaxSpeedKmh         int               `json:"max_speed_kmh"`
	OBDProtocol         string            `json:"obd_protocol"`
	EngineDisplacementL float64           `json:"engine_displacement_l"`
	InitialOdometerKm   float64           `json:"initial_odometer_km"`
	ThrottleIdlePct     float64           `json:"throttle_idle_pct"`
	ThrottleMaxPct      float64           `json:"throttle_max_pct"`
	FuelTankL           float64           `json:"fuel_tank_l"`
	FuelRateCorrection  float64           `json:"fuel_rate_correction"`
	MaxPS               float64           `json:"max_ps"`
	MaxTorqueKgfm       float64           `json:"max_torque_kgfm"`
	MaxTorqueRPM        int               `json:"max_torque_rpm"`
	MaxPSRPM            int               `json:"max_ps_rpm"`
	EcoGradientMaxKmpl  float64           `json:"eco_gradient_max_kmpl"`
	TripWarnKm          float64           `json:"trip_warn_km"`
	TripDangerKm        float64           `json:"trip_danger_km"`
	CoolantTemp         CoolantTempConfig `json:"coolant_temp"`
	OilChange           OilChangeConfig   `json:"oil_change"`
	WebSocket           WebSocketConfig   `json:"websocket"`
}

// RealtimeData はリアルタイムAPIのレスポンス（LCD用）
type RealtimeData struct {
	SpeedKmh       float64 `json:"speed_kmh"`
	RPM            float64 `json:"rpm"`
	EngineLoad     float64 `json:"engine_load"`
	ThrottlePos    float64 `json:"throttle_pos"`
	FuelEconomy    float64 `json:"fuel_economy"`
	FuelRateLH     float64 `json:"fuel_rate_lh"`
	AvgFuelEconomy float64 `json:"avg_fuel_economy"`
	EngagedGear    int     `json:"engaged_gear"`
	ATFTempC       float64 `json:"atf_temp_c"`
	ATFValid       bool    `json:"atf_valid"`
	ATFLevel       string  `json:"atf_level,omitempty"` // 表示色のキー。"" / warm / caution / hot / danger
	TripKm         float64 `json:"trip_km"`
	CoolantTemp    float64 `json:"coolant_temp"`
	IntakeMAP      float64 `json:"intake_map"`
	Voltage        float64 `json:"voltage"`
	FuelLevel      float64 `json:"fuel_level"`
	AmbientTemp    float64 `json:"ambient_temp"`
	EngineLoadPct  float64 `json:"engine_load_pct"`
	MAFAirFlow     float64 `json:"maf_airflow"`
	ShortFuelTrim  float64 `json:"short_fuel_trim"`
	TimingAdvance  float64 `json:"timing_advance"`
	IntakeAirTemp  float64 `json:"intake_air_temp"`
	O2Voltage      float64 `json:"o2_voltage"`
	RuntimeSec     int     `json:"runtime_sec"`
	// 給油ダイアログ (#120)。検出していない間と走行中は nil。
	Refuel *RefuelUI `json:"refuel,omitempty"`
	// 機関系の診断値 (2026-09-16 追加)。
	// 燃料トリムは fuel_system_str が「クローズドループ」のときだけ意味を持つ。
	LongFuelTrim     float64           `json:"long_fuel_trim"`
	FuelSystemStatus int               `json:"fuel_system_status"`
	FuelSystemStr    string            `json:"fuel_system_str,omitempty"`
	CatalystTempC    float64           `json:"catalyst_temp_c"`
	AbsoluteLoad     float64           `json:"absolute_load"`
	MIL              bool              `json:"mil"`       // チェックランプ点灯中
	DTCCount         int               `json:"dtc_count"` // 記録されている故障コード数
	Aux22            map[string]uint32 `json:"aux22,omitempty"`
	// 4輪それぞれの車速 (km/h) — CAN 0x4B0。
	// 前輪がわずかに速いのが定常状態で、幅は速度で変わる
	// (120km/h で +0.5〜1.0、低速で +0.1〜0.3)。ホイールスピンは
	// その速度での幅を超える前後差が「続く」ことで見る。
	WheelFL        float64 `json:"wheel_fl"`
	WheelFR        float64 `json:"wheel_fr"`
	WheelRL        float64 `json:"wheel_rl"`
	WheelRR        float64 `json:"wheel_rr"`
	RangeToEmptyKm float64 `json:"range_to_empty_km"` // 給油までの推定残距離 (推定残量 × ECO)
	FuelEstimateL  float64 `json:"fuel_estimate_l"`   // 航続距離に使う燃料残量の推定値 (L)。0 = 未推定
	Gear           int     `json:"gear"`
	GearRatio      float64 `json:"gear_ratio"`
	ATRange        int     `json:"at_range"`
	ATRangeStr     string  `json:"at_range_str"`
	Hold           bool    `json:"hold"`
	TCLocked       bool    `json:"tc_locked"`
	TCCLockPct     float64 `json:"tcc_lock_pct"`
	SlipRatio      float64 `json:"slip_ratio"`    // トルコンの滑り比。1.0=直結、1.05=5%滑り
	BrakePedal     bool    `json:"brake_pedal"`   // ブレーキペダル
	RadiatorFan    bool    `json:"radiator_fan"`  // ラジエータファン
	ACCompressor   bool    `json:"ac_compressor"` // エアコンコンプレッサー
	GradeRaw       int     `json:"grade_raw"`     // 勾配の生値。負が登り。単位未確定
	Shifting       bool    `json:"shifting"`
	OdometerCANKm  float64 `json:"odometer_can_km"` // CAN 0x430 由来の累計走行距離（検証用に併記）
	ElecB0Pct      float64 `json:"elec_b0_pct"`     // 0x430 B0 生値/2.55（燃料残量候補・未確定）
	ElecB1Raw      float64 `json:"elec_b1_raw"`     // 0x430 B1 生値（未確定）
	OilAlert       string  `json:"oil_alert"`
	OilCurrentKm   float64 `json:"oil_current_km"`
	OilRemainingKm float64 `json:"oil_remaining_km"`
	Notification   string  `json:"notification,omitempty"`
	OBDConnected   bool    `json:"obd_connected"`
	WiFiConnected  bool    `json:"wifi_connected"`
	PendingCount   int     `json:"pending_count"`
	SendSending    bool    `json:"send_sending"`
}

// loadConfig はJSONファイルから設定を読み込む。ファイルがなければデフォルト値を返す。
func loadConfig(path string) Config {
	cfg := Config{
		SerialPort:          "/dev/rfcomm0",
		WebhookURL:          "",
		PollIntervalMs:      500,
		LocalAPIPort:        9090,
		MaintenancePath:     "/var/lib/pi-obd-meter/maintenance.json",
		WebStaticDir:        "",
		MaxSpeedKmh:         180,
		OBDProtocol:         "6",
		EngineDisplacementL: 1.3,
		ThrottleIdlePct:     11.5,
		ThrottleMaxPct:      78,
		MaxPS:               91,
		MaxTorqueKgfm:       12.6,
		MaxTorqueRPM:        3500,
		MaxPSRPM:            6000,
		FuelTankL:           46,
		FuelRateCorrection:  defaultFuelRateCorrection,
		WebSocket: WebSocketConfig{
			Enabled:             true,
			BroadcastIntervalMs: 50,
			MaxClients:          3,
		},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("設定ファイルが見つかりません、デフォルト使用", "path", path, "error", err)
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		slog.Warn("設定ファイルのJSON解析失敗、デフォルト使用", "path", path, "error", err)
	}

	validateConfig(&cfg)
	return cfg
}

// validateConfig は設定値の妥当性をチェックし、不正値をデフォルトに修正する
func validateConfig(cfg *Config) {
	if cfg.EngineDisplacementL <= 0 {
		slog.Warn("engine_displacement_l が不正、デフォルト使用", "value", cfg.EngineDisplacementL)
		cfg.EngineDisplacementL = 1.3
	}
	// 0 も弾く。他の項目が <= 0 を見ているのにここだけ < 0 だった。
	// 0 のまま通すと calcFuelEconomy の `if correction > 0` を素通りし、
	// 補正なし (×1.0) で走る。約 30% の過少になるのに警告も出ない。
	if cfg.FuelRateCorrection <= 0 {
		slog.Warn("fuel_rate_correction が不正、デフォルト使用", "value", cfg.FuelRateCorrection)
		cfg.FuelRateCorrection = defaultFuelRateCorrection
	}
	if cfg.FuelTankL <= 0 {
		slog.Warn("fuel_tank_l が不正、デフォルト使用", "value", cfg.FuelTankL)
		cfg.FuelTankL = 46
	}
	if cfg.MaxSpeedKmh <= 0 || cfg.MaxSpeedKmh > 400 {
		slog.Warn("max_speed_kmh が不正、デフォルト使用", "value", cfg.MaxSpeedKmh)
		cfg.MaxSpeedKmh = 180
	}
	if cfg.LocalAPIPort <= 0 || cfg.LocalAPIPort > 65535 {
		slog.Warn("local_api_port が不正、デフォルト使用", "value", cfg.LocalAPIPort)
		cfg.LocalAPIPort = 9090
	}
	if cfg.MaxPS <= 0 {
		slog.Warn("max_ps が不正、デフォルト使用", "value", cfg.MaxPS)
		cfg.MaxPS = 91
	}
	if cfg.MaxTorqueKgfm <= 0 {
		slog.Warn("max_torque_kgfm が不正、デフォルト使用", "value", cfg.MaxTorqueKgfm)
		cfg.MaxTorqueKgfm = 12.6
	}
	if cfg.MaxTorqueRPM <= 0 {
		slog.Warn("max_torque_rpm が不正、デフォルト使用", "value", cfg.MaxTorqueRPM)
		cfg.MaxTorqueRPM = 3500
	}
	if cfg.MaxPSRPM <= 0 {
		slog.Warn("max_ps_rpm が不正、デフォルト使用", "value", cfg.MaxPSRPM)
		cfg.MaxPSRPM = 6000
	}
	if cfg.ThrottleIdlePct < 0 || cfg.ThrottleIdlePct > 255 {
		slog.Warn("throttle_idle_pct が不正、デフォルト使用", "value", cfg.ThrottleIdlePct)
		cfg.ThrottleIdlePct = 11.5
	}
	if cfg.ThrottleMaxPct <= cfg.ThrottleIdlePct || cfg.ThrottleMaxPct > 255 {
		slog.Warn("throttle_max_pct が不正、デフォルト使用", "value", cfg.ThrottleMaxPct)
		cfg.ThrottleMaxPct = 78
	}
	// WebSocket デフォルト値（config.json にフィールドがない場合）
	if cfg.WebSocket.BroadcastIntervalMs <= 0 {
		cfg.WebSocket.BroadcastIntervalMs = 50
	}
	if cfg.WebSocket.MaxClients <= 0 {
		cfg.WebSocket.MaxClients = 3
	}
}
