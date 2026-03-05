package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Discord はDiscord Webhookで通知を送信する
type Discord struct {
	webhookURL string
	client     *http.Client
	username   string
}

// NewDiscord は新しいDiscord通知クライアントを作成する
func NewDiscord(webhookURL string) *Discord {
	return &Discord{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: 10 * time.Second},
		username:   "🚗 DYデミオ",
	}
}

// discordPayload はDiscord Webhookのペイロード
type discordPayload struct {
	Username string         `json:"username,omitempty"`
	Content  string         `json:"content,omitempty"`
	Embeds   []discordEmbed `json:"embeds,omitempty"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color"`
	Fields      []discordField `json:"fields,omitempty"`
	Footer      *discordFooter `json:"footer,omitempty"`
	Timestamp   string         `json:"timestamp,omitempty"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordFooter struct {
	Text string `json:"text"`
}

// SendTrip はトリップ完了通知を送信する
func (d *Discord) SendTrip(tripID string, distanceKm, fuelUsedL, avgEcon, maxSpeed float64, drivingMin float64) error {
	embed := discordEmbed{
		Title: "⛽ トリップ完了",
		Color: 0x00B0F0, // マツダブルー的な
		Fields: []discordField{
			{Name: "走行距離", Value: fmt.Sprintf("%.1f km", distanceKm), Inline: true},
			{Name: "平均燃費", Value: fmt.Sprintf("%.1f km/L", avgEcon), Inline: true},
			{Name: "燃料消費", Value: fmt.Sprintf("%.2f L", fuelUsedL), Inline: true},
			{Name: "最高速度", Value: fmt.Sprintf("%.0f km/h", maxSpeed), Inline: true},
			{Name: "走行時間", Value: fmt.Sprintf("%.0f 分", drivingMin), Inline: true},
		},
		Footer:    &discordFooter{Text: tripID},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	return d.send(discordPayload{Username: d.username, Embeds: []discordEmbed{embed}})
}

// SendMaintenanceAlert はメンテナンスリマインダー通知を送信する
func (d *Discord) SendMaintenanceAlert(name string, remaining string, isOverdue bool) error {
	title := fmt.Sprintf("🔧 %s", name)
	color := 0xFFA500 // オレンジ（警告）
	desc := fmt.Sprintf("残り %s", remaining)

	if isOverdue {
		color = 0xFF0000 // 赤（超過）
		desc = fmt.Sprintf("⚠️ %s 超過しています！", remaining)
	}

	embed := discordEmbed{
		Title:       title,
		Description: desc,
		Color:       color,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	return d.send(discordPayload{Username: d.username, Embeds: []discordEmbed{embed}})
}

// SendRaw は任意のテキストメッセージを送信する
func (d *Discord) SendRaw(message string) error {
	return d.send(discordPayload{Username: d.username, Content: message})
}

func (d *Discord) send(payload discordPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := d.client.Post(d.webhookURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("Discord送信エラー: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Discord APIエラー: status %d", resp.StatusCode)
	}

	return nil
}
