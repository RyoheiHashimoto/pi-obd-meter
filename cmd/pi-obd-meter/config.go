package main

import (
	"encoding/json"
	"log/slog"
	"os"

	"github.com/hashimoto/pi-obd-meter/internal/display"
	"github.com/hashimoto/pi-obd-meter/internal/maintenance"
)

// Config はアプリケーション設定
type Config struct {
	CANInterface         string                   `json:"can_interface"`
	SerialPort           string                   `json:"serial_port"`
	WebhookURL           string                   `json:"webhook_url"`
	PollIntervalMs       int                      `json:"poll_interval_ms"`
	LocalAPIPort         int                      `json:"local_api_port"`
	MaintenancePath      string                   `json:"maintenance_path"`
	WebStaticDir         string                   `json:"web_static_dir"`
	MaxSpeedKmh          int                      `json:"max_speed_kmh"`
	OBDProtocol          string                   `json:"obd_protocol"`
	EngineDisplacementL  float64                  `json:"engine_displacement_l"`
	InitialOdometerKm    float64                  `json:"initial_odometer_km"`
	ThrottleIdlePct      float64                  `json:"throttle_idle_pct"`
	ThrottleMaxPct       float64                  `json:"throttle_max_pct"`
	FuelTankL            float64                  `json:"fuel_tank_l"`
	FuelRateCorrection   float64                  `json:"fuel_rate_correction"`
	MaxPS                float64                  `json:"max_ps"`
	MaxTorqueKgfm        float64                  `json:"max_torque_kgfm"`
	MaxTorqueRPM         int                      `json:"max_torque_rpm"`
	MaxPSRPM             int                      `json:"max_ps_rpm"`
	EcoGreenKmpl         float64                  `json:"eco_green_kmpl"`
	EcoOrangeKmpl        float64                  `json:"eco_orange_kmpl"`
	TripWarnKm           float64                  `json:"trip_warn_km"`
	TripDangerKm         float64                  `json:"trip_danger_km"`
	MaintenanceReminders []maintenance.Reminder   `json:"maintenance_reminders"`
	Brightness           display.BrightnessConfig `json:"brightness"`
}

// RealtimeData はリアルタイムAPIのレスポンス（LCD用）
type RealtimeData struct {
	SpeedKmh       float64              `json:"speed_kmh"`
	RPM            float64              `json:"rpm"`
	EngineLoad     float64              `json:"engine_load"`
	ThrottlePos    float64              `json:"throttle_pos"`
	FuelEconomy    float64              `json:"fuel_economy"`
	FuelRateLH     float64              `json:"fuel_rate_lh"`
	AvgFuelEconomy float64              `json:"avg_fuel_economy"`
	TripKm         float64              `json:"trip_km"`
	CoolantTemp    float64              `json:"coolant_temp"`
	IntakeMAP      float64              `json:"intake_map"`
	Voltage        float64              `json:"voltage"`
	FuelLevel      float64              `json:"fuel_level"`
	AmbientTemp    float64              `json:"ambient_temp"`
	EngineLoadPct  float64              `json:"engine_load_pct"`
	MAFAirFlow     float64              `json:"maf_airflow"`
	ShortFuelTrim  float64              `json:"short_fuel_trim"`
	TimingAdvance  float64              `json:"timing_advance"`
	IntakeAirTemp  float64              `json:"intake_air_temp"`
	O2Voltage      float64              `json:"o2_voltage"`
	RuntimeSec     int                  `json:"runtime_sec"`
	Gear           int                  `json:"gear"`
	GearRatio      float64              `json:"gear_ratio"`
	ATRange        int                  `json:"at_range"`
	ATRangeStr     string               `json:"at_range_str"`
	Hold           bool                 `json:"hold"`
	TCLocked       bool                 `json:"tc_locked"`
	Shifting       bool                 `json:"shifting"`
	Alerts         []maintenance.Status `json:"alerts"`
	Notification   string               `json:"notification,omitempty"`
	OBDConnected   bool                 `json:"obd_connected"`
	WiFiConnected  bool                 `json:"wifi_connected"`
	PendingCount   int                  `json:"pending_count"`
	SendSending    bool                 `json:"send_sending"`
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
		FuelRateCorrection:  1.3,
		Brightness:          display.DefaultConfig(),
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
	if cfg.FuelRateCorrection < 0 {
		slog.Warn("fuel_rate_correction が負数、デフォルト使用", "value", cfg.FuelRateCorrection)
		cfg.FuelRateCorrection = 1.3
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
	if cfg.ThrottleIdlePct < 0 || cfg.ThrottleIdlePct > 100 {
		slog.Warn("throttle_idle_pct が不正、デフォルト使用", "value", cfg.ThrottleIdlePct)
		cfg.ThrottleIdlePct = 11.5
	}
	if cfg.ThrottleMaxPct <= cfg.ThrottleIdlePct || cfg.ThrottleMaxPct > 100 {
		slog.Warn("throttle_max_pct が不正、デフォルト使用", "value", cfg.ThrottleMaxPct)
		cfg.ThrottleMaxPct = 78
	}
}
