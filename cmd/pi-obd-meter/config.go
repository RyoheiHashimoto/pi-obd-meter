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
	return cfg
}
