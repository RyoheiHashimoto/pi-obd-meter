# 設定ガイド

`configs/config.json` で車両ごとのパラメータを設定する。

## 設定例 (DYデミオ)

```json
{
  "serial_port": "/dev/rfcomm0",
  "webhook_url": "https://script.google.com/macros/s/XXXXXX/exec",
  "poll_interval_ms": 500,
  "local_api_port": 9090,
  "maintenance_path": "/var/lib/pi-obd-meter/maintenance.json",
  "web_static_dir": "",
  "obd_protocol": "6",
  "max_speed_kmh": 180,
  "engine_displacement_l": 1.3,
  "initial_odometer_km": 98000,
  "throttle_idle_pct": 11.5,
  "throttle_max_pct": 75,
  "fuel_tank_l": 40,
  "fuel_rate_correction": 1.3,
  "maintenance_reminders": [...],
  "brightness": {...}
}
```

---

## 全パラメータ一覧

### 通信・接続

| パラメータ | 型 | デフォルト | 説明 |
|---|---|---|---|
| `serial_port` | string | `"/dev/rfcomm0"` | ELM327 のシリアルポート |
| `webhook_url` | string | `""` | GAS Webhook の URL。空の場合はデータ送信しない |
| `obd_protocol` | string | `"6"` | ELM327 の ATSP コマンド値。`"0"`=自動検出, `"6"`=CAN 11bit 500k |
| `poll_interval_ms` | int | `500` | OBD ポーリング間隔 (ms)。ECU応答速度に依存 |
| `local_api_port` | int | `9090` | メーター UI 用のローカル API ポート |

### 車両パラメータ

| パラメータ | 型 | デフォルト | 説明 |
|---|---|---|---|
| `engine_displacement_l` | float | `1.3` | エンジン排気量 (L)。燃費推定・ECO閾値の自動導出に使用 |
| `max_speed_kmh` | int | `180` | 速度メーターの最大表示値 (km/h) |
| `initial_odometer_km` | float | `0` | 初期累積走行距離 (km)。メーター実値に合わせて設定 |
| `fuel_tank_l` | float | `40` | 燃料タンク容量 (L)。TRIP 警告閾値の導出に使用 |
| `fuel_rate_correction` | float | `1.3` | 燃料レート補正係数。理論値と実燃費の乖離を補正 |

### スロットル表示

| パラメータ | 型 | デフォルト | 説明 |
|---|---|---|---|
| `throttle_idle_pct` | float | `11.5` | アイドル時のスロットル開度 (%)。表示のゼロ基準 |
| `throttle_max_pct` | float | `78` | 全開時のスロットル開度 (%)。表示の100%基準 |

> **車種依存度: 高。** ECU によってアイドル時の報告値が 5-20% まで幅がある。
> 実車でアイドル時の値を読み取って `throttle_idle_pct` を設定する。
> `pi-obd-scanner` で PID 0x11 の値を確認できる。

### ファイルパス

| パラメータ | 型 | デフォルト | 説明 |
|---|---|---|---|
| `maintenance_path` | string | `"/var/lib/pi-obd-meter/maintenance.json"` | メンテナンス状態の保存先 |
| `web_static_dir` | string | `""` | Web UI 配信元ディレクトリ。空 = go:embed (本番)、パス指定 = ファイルシステム (開発) |

---

## 輝度設定

時刻ベースで画面輝度を自動調整する。`brightness` オブジェクトで設定。

```json
{
  "brightness": {
    "hdmi_output": "HDMI-A-1",
    "schedule": [
      {"hour": 5,  "brightness": 0.6, "label": "早朝"},
      {"hour": 7,  "brightness": 1.0, "label": "昼間"},
      {"hour": 17, "brightness": 0.7, "label": "夕方"},
      {"hour": 19, "brightness": 0.5, "label": "夜間"},
      {"hour": 22, "brightness": 0.3, "label": "深夜"}
    ]
  }
}
```

| パラメータ | 型 | デフォルト | 説明 |
|---|---|---|---|
| `hdmi_output` | string | `"HDMI-1"` | xrandr の出力名 |
| `schedule[].hour` | int | — | 時間帯の開始時刻 (0-23) |
| `schedule[].brightness` | float | — | 輝度 (0.0-1.0) |

- 1分間隔でチェック、値変更時のみ `xrandr --brightness` を実行
- 0:00-4:59 はスケジュール最後のエントリの輝度を継続

---

## メンテナンスリマインダー

```json
{
  "maintenance_reminders": [
    {
      "id": "oil_change",
      "name": "エンジンオイル交換",
      "type": "distance",
      "interval_km": 3000,
      "warning_pct": 0.8,
      "last_reset_km": 97000
    },
    {
      "id": "shaken",
      "name": "車検",
      "type": "date",
      "interval_days": 730,
      "warning_pct": 0.9,
      "last_reset_at": "2026-02-20T00:00:00+09:00"
    }
  ]
}
```

### 項目フィールド

| フィールド | 型 | 説明 |
|---|---|---|
| `id` | string | 一意の識別子（GAS との同期に使用） |
| `name` | string | 表示名 |
| `type` | string | `"distance"` (距離ベース) または `"date"` (日付ベース) |
| `interval_km` | int | 交換/実施間隔 (km)。type=distance の場合 |
| `interval_days` | int | 有効期間 (日)。type=date の場合 |
| `warning_pct` | float | 警告表示開始の進捗率 (0.0-1.0)。例: 0.8 = 80%到達で橙表示 |
| `last_reset_km` | float | 最終リセット時の累積走行距離 (km)。type=distance の場合 |
| `last_reset_at` | string | 最終リセット日時 (ISO 8601)。type=date の場合 |

### デフォルト項目

| 項目 | 種別 | 間隔 | 警告 |
|---|---|---|---|
| エンジンオイル交換 | 距離 | 3,000 km | 80% |
| エアフィルター交換 | 距離 | 20,000 km | 85% |
| タイヤローテーション | 距離 | 10,000 km | 80% |
| 車検 | 日付 | 730日 | 90% |
| ATF交換 | 距離 | 40,000 km | 90% |

---

## 車種チューニング

他車種に適用する際にチェックが必要な項目。

### config.json で対応（ビルド不要）

| 項目 | パラメータ | DYデミオ | 例: 2.0L セダン |
|---|---|---|---|
| エンジン排気量 | `engine_displacement_l` | 1.3 | 2.0 |
| スロットルアイドル開度 | `throttle_idle_pct` | 11.5 | 8.0-15.0 |
| スロットル最大開度 | `throttle_max_pct` | 75 | 70-85 |
| 燃料タンク容量 | `fuel_tank_l` | 40 | 60 |
| 燃料レート補正係数 | `fuel_rate_correction` | 1.3 | 給油燃費と比較して調整 |
| メーター最大速度 | `max_speed_kmh` | 180 | 260 |
| OBDプロトコル | `obd_protocol` | "6" (CAN 11bit) | "0" (自動検出) |
| メンテ項目 | `maintenance_reminders` | 上記参照 | 車種推奨間隔に合わせる |

### 排気量・タンク容量から自動導出される値

排気量とタンク容量を設定すれば、以下の閾値が自動で算出される:

| 項目 | 導出式 | 1.3L/40L | 2.0L/60L |
|---|---|---|---|
| アイドル燃料消費 | `0.6 × 排気量` L/h | 0.78 | 1.20 |
| ECO 緑閾値 (km/L) | `round(20 / 排気量, 1)` | 15.4 | 10.0 |
| ECO 橙閾値 (km/L) | `round(8 / 排気量, 1)` | 6.2 | 4.0 |
| TRIP 警告 (km) | `tank × ecoGreen × 0.5` | 308 | 300 |
| TRIP 危険 (km) | `tank × ecoGreen × 0.85` | 523 | 510 |

### `fuel_rate_correction` の調整方法

1. 給油時に満タン法で実燃費を計算
2. メーターの ECO 表示値（平均燃費）と比較
3. 補正係数 = 理論燃費 / 実燃費
4. 例: 理論値 16 km/L に対して実燃費 12 km/L → `fuel_rate_correction = 1.33`

数回の給油で傾向を見て調整するとよい。

### ハードコード値（ソース変更が必要）

一般的な車種ではそのまま使えるが、必要に応じて変更可能な値。
詳細は [calculation-logic.md](calculation-logic.md) を参照。

| 項目 | ファイル | 現在値 | 汎用性 |
|---|---|---|---|
| 速度帯カラー閾値 | `gauge.js` | 120/100/80/60/30 km/h | ほぼ汎用 |
| 水温閾値 | `indicators.js` | 70/105 °C | ほぼ汎用 |
| RPM閾値 | `indicators.js` | 3000/4500 rpm | 車種による |
| エンブレ負荷閾値 | `fuel.go` | 5.0% | ほぼ汎用 |
| エンブレMAP閾値 | `fuel.go` | 35.0 kPa | ほぼ汎用 |
| 燃費表示最低速度 | `fuel.go` | 10.0 km/h | ほぼ汎用 |
| ECO排気量係数 (km/L) | `api.go` | 20 / 8 | 排気量に連動 |
