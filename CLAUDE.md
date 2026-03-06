# CLAUDE.md — DYデミオ燃費メーター

## プロジェクト概要

OBD-2対応車向けの車載メーター + 自動燃費記録システム。
Raspberry Pi 4 + ELM327 (Bluetooth) で速度・RPM・負荷・スロットルを5インチLCDにリアルタイム表示。
給油時の燃費は、エンジン始動時のタンク残量変化から自動算出し、Google Sheetsに記録する。
スマホからはGAS Webアプリで燃費履歴・メンテナンス状態を確認できる。
開発・動作確認はDYマツダデミオ（DBA-DY3W, ZJ-VE 1.3L）で行っている。

## 技術スタック

- **言語:** Go（車載バイナリ）+ HTML/CSS/JS（メーターUI）+ Google Apps Script（データ記録 + Webダッシュボード）
- **ターゲット:** Raspberry Pi 4 Model B 2GB (ARM64, Raspberry Pi OS Lite 64-bit)
- **ディスプレイ:** ELECROW 5インチ IPS HDMI タッチモニター (800×480) — Pi USBから給電可 (5V/1A)
- **OBD通信:** ELM327 V1.5 Bluetooth 2.0 → /dev/rfcomm0 (SPPシリアル)
- **データ保存:** Google Sheets (GAS webhook)。ラズパイ側にデータ永続化なし（overlayFS前提）

## ビルド & デプロイ

```bash
# ローカルビルド（Mac上でのコンパイルチェック）
go build ./cmd/pi-obd-meter
go build ./cmd/pi-obd-scanner

# クロスコンパイル（Mac → ARM64）
./scripts/deploy.sh build

# ラズパイにデプロイ（build + rsync + systemctl restart）
./scripts/deploy.sh deploy

# Web UIだけデプロイ
./scripts/deploy.sh deploy-web

# 初回セットアップ（ディレクトリ作成 + systemd登録）
./scripts/deploy.sh setup

# ログ確認
./scripts/deploy.sh logs
```

ターゲット: `GOOS=linux GOARCH=arm64`
デプロイ先: `$PI_HOST:/opt/pi-obd-meter/`（デフォルト: `laurel@pi-obd-meter.local`、`scripts/deploy.sh` で設定）

## ディレクトリ構成

```
cmd/
  pi-obd-meter/main.go      メインアプリ。OBD読取→表示→給油検出→送信を統合
  pi-obd-scanner/main.go     対応PIDスキャナー（診断・初期確認用、DTC読取含む）

internal/
  obd/
    elm327.go               ELM327シリアル通信（AT初期化、PIDリクエスト、マルチPIDバッチ）
    pids.go                 OBD-2 PID定義（RPM, 速度, 負荷, スロットル, 水温, 燃料タンク）
    dtc.go                  DTC読取（Mode 03）。スキャナーコマンドから使用
  trip/
    tracker.go              トリップ追跡。車速積分で走行距離 + 燃料状態永続化（給油検出用）
  sender/
    client.go               GAS Webhook送信。汎用Send + メモリ内リトライキュー（最大100件）
  display/
    brightness.go           xrandr経由の輝度制御。時刻ベース自動調整
  maintenance/
    reminder.go             走行距離/日付ベースのメンテナンスリマインダー

web/static/
  meter.html                メーター画面。速度+RPMの270° SVGアークゲージ、中央にスロットル/負荷の縦バー

gas/
  webhook.gs                Google Apps Script。doPost(トリップ/給油/メンテ受信) + doGet(スマホ用ダッシュボード)

configs/
  config.json               アプリ設定（シリアルポート、webhook URL、メンテナンス項目等）
  pi-obd-meter.service      systemdユニットファイル
  kiosk.sh                  Chromiumキオスクモード起動スクリプト

docs/
  deploy-guide.md           セットアップ・デプロイ手順（詳細）
```

## アーキテクチャ上の重要な決定

### データフロー
```
ECU → ELM327 (CAN 2.0B) → Pi (BT rfcomm) → meter.html（車載LCD: 速度/RPM/負荷/スロットル）
                                            → GAS Webhook → Google Sheets（トリップ/給油/メンテ記録）
                                                          → doGet Webアプリ（スマホ: 燃費履歴/メンテ状態）
```

### 給油自動検出
- エンジン始動時にPID 0x2F（燃料タンクレベル）を3回読み取り平均
- 前回保存値との差分 ≥ 5% → 給油と判定
- 給油検出時: `燃費 = 走行距離 / ((トリップ開始時タンク% - 直近タンク%) / 100 × タンク容量L)`
- 前回の燃料状態は `trip_state.json` に永続化（`lastFuelPct`, `tripStartFuelPct`）
- 3-5% の微増はログのみ（ノイズ/センサー揺らぎ）、3%未満は無視
- 距離 < 1km のトリップは燃費計算をスキップ

### GAS Webダッシュボード
- `doGet` で `HtmlService.createHtmlOutput()` によるモバイル対応HTMLを返す
- 表示内容: 通算燃費、直近10件の給油履歴テーブル、メンテナンス進捗バー
- ダークテーマ、外部ライブラリなし、ホーム画面追加対応
- データはすべてGoogle Sheetsから直接取得（リアルタイム）

### 2層ポーリング
- **高速 (150ms)**: RPM + 速度 + エンジン負荷 + スロットル（ReadFast）
- **全PID (750ms=150ms×5)**: 上記 + 冷却水温 + 燃料タンクレベル（ReadAll）
- 高速層はメーター表示の応答性確保、全PIDは燃料レベル追跡に使用

### overlayFS
- ラズパイのSDカードは overlayFS で保護する（エンジンOFF = 電源断からの保護）
- **開発中は OFF**にしておく。`make deploy` で自由に書き込める
- 安定運用に入ったら ON にする。更新時だけ一時的に OFF → デプロイ → ON
- 詳細は `docs/deploy-guide.md` セクション5

### SD書き込みゼロ設計
- トリップデータ: GAS Webhookで即送信。送信失敗時はメモリ内キュー（最大100件）
- ログ: journald（RAM上）
- 設定ファイル: overlayFS有効時はRAM上のコピーを読む

### ELM327通信
- Bluetooth 2.0 Classic (SPP)。BLEはGATT複雑で不採用
- rfcomm bind → /dev/rfcomm0 でシリアルポートとして扱う
- マルチPIDバッチ: 1リクエストで複数PID取得（6-7往復 → 2往復、~3.5-4Hz）
- ECUレスポンスが律速（50-100ms/PID）。BT遅延（20-50ms）は支配的でない

### メーターUI
- 800×480 全画面、速度（左）+ RPM（右）の270° SVGアークゲージ
- 中央にスロットル位置・エンジン負荷の縦バー（ラギング指数で色分け: 緑/橙/赤）
- requestAnimationFrame で60fps補間（ease-out cubic, 300ms）
- OBDデータ更新は~3.5-4Hz、アニメーションで滑らかに見せる
- 時刻ベースで自動輝度調整（xrandr --brightness）
- レッドゾーン描画は `redline_rpm` configから取得

### ディスプレイ設定 (config.txt)
ELECROW 5インチ IPS (800×480) 用。`/boot/firmware/config.txt` に追記:
```
hdmi_force_hotplug=1
max_usb_current=1
hdmi_drive=1
hdmi_group=2
hdmi_mode=87
hdmi_cvt 800 480 60 6 0 0 0
```
- `max_usb_current=1`: USB出力電流上限を引き上げ（モニターへのUSB給電に必要）
- `hdmi_force_hotplug=1`: HDMI接続検出を強制（モニター先起動でも映る）
- `hdmi_cvt 800 480 60 6 0 0 0`: 800×480 60Hz カスタムモード
- タッチ機能は車載では不使用（dtoverlay設定は不要）
- モニター給電: Pi 4 USB-A → ELECROW micro USB (5V/1A)

### データ送信の責任分離
- **トリップ完了**: Pi → GAS Webhook (type: "trip") → Google Sheets
- **給油検出**: Pi → GAS Webhook (type: "refuel") → Google Sheets（燃費計算値含む）
- **メンテナンス状態**: Pi → GAS Webhook (type: "maintenance") → Google Sheets（エンジン始動時に毎回送信）
- **リアルタイム表示**: Pi → meter.html（車載LCD、ローカルHTTP API経由）
- **燃費履歴・メンテ確認**: GAS doGet Webアプリ（スマホからアクセス）

## 車両固有の設定（config.json）

以下はすべてconfig.jsonで設定する。コード内にハードコードしない。

- `redline_rpm`: レッドゾーン開始回転数 (例: ZJ-VE=6500)
- `max_speed_kmh`: 速度メーター最大値 (例: 180)
- `max_rpm`: RPMメーター最大値 (例: 8000)
- `fuel_tank_capacity_l`: 燃料タンク容量 (例: DYデミオ=44L)
- `refuel_min_increase_pct`: 給油判定の最小増加率 (デフォルト: 5.0%)
- `maintenance_reminders`: メンテナンス項目の配列（ID, 名前, タイプ, 間隔, 警告閾値）

### 開発元の確認車両
- マツダ DYデミオ DBA-DY3W / ZJ-VE 1.3L 91PS / 1,090kg / 4AT / CAN 2.0B

## 注意事項

- `go.mod` のモジュール名は `github.com/hashimoto/pi-obd-meter`
- エラーメッセージ・ログは日本語
- config.json の webhook_url は実際のGAS WebアプリURLに差し替えが必要
- ELM327のBluetoothアドレスは実機に合わせて rfcomm bind する
- 車両固有の定数をコード内にハードコードしないこと（config.jsonから読む）
