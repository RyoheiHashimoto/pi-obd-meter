# CLAUDE.md — DYデミオ車載メーター

## プロジェクト概要

OBD-2対応車向けの車載メーター + 走行記録システム。
Raspberry Pi 4 + ELM327 (Bluetooth) で速度・RPM・負荷・スロットル・瞬間燃費を5インチLCDにリアルタイム表示。
走行距離・メンテナンス状態を Google Sheets に自動記録し、スマホから給油記録・ODO補正・メンテナンス管理を行う。
開発・動作確認はDYマツダデミオ（DBA-DY3W, ZJ-VE 1.3L）で行っている。

## 技術スタック

- **言語:** Go（車載バイナリ、`log/slog` 構造化ログ）+ HTML/CSS/JS（メーターUI）+ Google Apps Script（データ記録 + Webダッシュボード）
- **ターゲット:** Raspberry Pi 4 Model B 2GB (ARM64, Raspberry Pi OS Lite 64-bit)
- **ディスプレイ:** ELECROW 5インチ IPS HDMI タッチモニター (800×480) — Pi USBから給電可 (5V/1A)
- **OBD通信:** ELM327 V1.5 Bluetooth 2.0 → /dev/rfcomm0 (SPPシリアル)
- **データ保存:** Google Sheets (GAS webhook)。ローカル状態ファイルはアトミック書き込み（tmp+rename）で電源断保護

## ビルド & デプロイ

```bash
# ローカルビルド（Mac上でのコンパイルチェック）
go build ./cmd/pi-obd-meter
go build ./cmd/pi-obd-scanner

# クロスコンパイル（Mac → ARM64）
./scripts/deploy.sh build

# ラズパイにデプロイ（build + rsync + systemctl restart）
./scripts/deploy.sh deploy

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
  pi-obd-meter/main.go      メインアプリ。OBD読取→表示→メンテ送信→燃費推定を統合
  pi-obd-scanner/main.go     対応PIDスキャナー（診断・初期確認用、DTC読取含む）

internal/
  obd/
    elm327.go               ELM327シリアル通信（AT初期化、PIDリクエスト、マルチPIDバッチ）
    pids.go                 OBD-2 PID定義（RPM, 速度, 負荷, スロットル, 水温）
    dtc.go                  DTC読取（Mode 03）。スキャナーコマンドから使用
  trip/
    tracker.go              トリップ追跡。車速積分で走行距離を積算、電源断復帰のための状態永続化
  sender/
    client.go               GAS Webhook送信。Send/SendWithResponse + メモリ内リトライキュー（最大100件、指数バックオフ）
  display/
    brightness.go           xrandr経由の輝度制御。時刻ベース自動調整
  maintenance/
    reminder.go             走行距離/日付ベースのメンテナンスリマインダー

web/
  embed.go                  go:embed で static/ をバイナリに埋め込み
  static/
    meter.html                メーター画面HTML（CSS/JSは分離）
  meter.css                 CSS Custom Properties でテーマ管理
  meter.js                  ゲージ描画・APIポーリング・シミュレーション

gas/
  webhook.gs                Google Apps Script。doPost(トリップ/メンテ受信) + doGet(スマホ用ダッシュボード)

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
ECU → ELM327 (CAN 2.0B) → Pi (BT rfcomm) → meter.html（車載LCD: 速度/RPM/負荷/スロットル/燃費）
                                            → GAS Webhook → Google Sheets（トリップ/メンテ記録）
                                                          ↕
                                     スマホブラウザ → GAS doGet（給油記録/ODO補正/メンテ管理）
```

### 給油記録（手動 — スマホダッシュボード経由）
- スマホからGASダッシュボードにアクセスし、日付・距離・給油量を入力
- GAS側で燃費を自動算出し Google Sheets に記録
- 給油記録時にトリップリセットを GAS → Pi に通知（`pending_resets` レスポンス経由）
- Pi は次回メンテナンス送信時にレスポンスから `trip_reset` を検出してトリップをリセット

### GAS Webダッシュボード
- `doGet` で `HtmlService.createHtmlOutput()` によるモバイル対応HTMLを返す
- 表示内容: 通算燃費、直近の給油履歴テーブル、メンテナンス進捗バー
- 操作: 給油記録、ODO補正、メンテナンスリセット
- ダークテーマ、外部ライブラリなし、ホーム画面追加対応
- データはすべてGoogle Sheetsから直接取得

### Pi ↔ GAS 通信サイクル
- Pi はエンジン始動時 + 5分間隔でメンテナンス状態を GAS に送信
- GAS のレスポンスに `pending_resets`（メンテリセット）、`odo_correction`（ODO補正）、`trip_reset` が含まれる
- Pi はレスポンスを処理し、リセット適用後に即座に再送信してGAS側を最新に更新

### 瞬間燃費推定
- MAFセンサーがある車: MAF (g/s) → 燃料消費量 (L/h) → 燃費 (km/L)
- MAFセンサーがない車（DYデミオ等）: エンジン負荷 × RPM × 排気量から吸入空気量を推定
- 物理定数: 理論空燃比 14.7、ガソリン密度 750 g/L、空気密度 1.225 g/L
- 低速域 (<10 km/h) では燃費表示しない（クリープ等でのノイズ回避）

### 2層ポーリング
- **高速 (150ms)**: RPM + 速度 + エンジン負荷 + スロットル（ReadFast）
- **全PID (750ms=150ms×5)**: 上記 + 冷却水温（ReadAll）
- 高速層はメーター表示の応答性確保

### SD書き込み低減
- swap無効化（`deploy.sh setup` で `dphys-swapfile` を停止・無効化）
- 状態ファイル（maintenance.json, trip_state.json）: アトミック書き込み（tmp+rename+fsync）で電源断保護
- トリップデータ: GAS Webhookで即送信。送信失敗時はメモリ内キュー（最大100件、指数バックオフ 5m→30m）
- ログ: journald（RAM上）
- 起動時にGASからODO復元（`type: "restore"`）。電源断でリセットされた場合のフォールバック

### ELM327通信
- Bluetooth 2.0 Classic (SPP)。BLEはGATT複雑で不採用
- rfcomm bind → /dev/rfcomm0 でシリアルポートとして扱う
- マルチPIDバッチ: 1リクエストで複数PID取得（6-7往復 → 2往復、~3.5-4Hz）
- ECUレスポンスが律速（50-100ms/PID）。BT遅延（20-50ms）は支配的でない

### メーターUI
- 800×480 全画面、速度の270° SVGアークゲージ + 内側にスロットルアーク
- 右パネルにインジケーター7項目（ECO/TRIP/TEMP/MAINT/SEND/WiFi/OBD）
- CSS/JS分離済み（meter.html + meter.css + meter.js）
- CSS Custom Properties で色・レイアウトを一元管理
- requestAnimationFrame で60fps補間、OBDデータは~3.5-4Hz
- API未接続時はシミュレーションモードにフォールバック
- 時刻ベースで自動輝度調整（xrandr --brightness）

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
- **メンテナンス状態**: Pi → GAS Webhook (type: "maintenance") → Google Sheets（始動時 + 5分間隔）
- **状態復元**: Pi起動時 → GAS Webhook (type: "restore") → ODO/最終給油距離を取得
- **リアルタイム表示**: Pi → meter.html（車載LCD、ローカルHTTP API経由）
- **給油記録**: スマホ → GAS doGet/doPost → Google Sheets（手動入力、燃費自動算出）
- **ODO補正・メンテリセット**: スマホ → GAS → Pi（次回メンテ送信レスポンスで反映）

### ローカルAPI
- `GET /api/config` — max_speed_kmh, version
- `GET /api/realtime` — 速度・RPM・負荷・スロットル・燃費・トリップ・接続状態
- `GET /api/maintenance` — メンテナンス全項目の進捗
- `GET /api/health` — OBD/WiFi接続・キューサイズ・uptime・バージョン

### Graceful Shutdown
- SIGINT/SIGTERM受信時にトリップ状態とメンテナンス状態を保存してから終了
- バージョン: `-ldflags "-X main.version=vX.Y.Z"` でビルド時に埋め込み

### Web UI 埋め込み（go:embed）
- `web/embed.go` で `web/static/` をバイナリに埋め込み（`go:embed static`）
- 本番: 埋め込みファイルから配信（バイナリ1つで完結）
- 開発: config.json の `web_static_dir` にパスを指定すればファイルシステムから配信

### 自動更新（go-selfupdate）
- 起動時に GitHub Releases をチェック、新バージョンがあればアトミックにバイナリ差し替え
- `rename()` によるアトミック操作で電源断時もバイナリ破損なし
- 更新後は `os.Exit(0)` → systemd `Restart=always` で新バイナリが起動
- スキップ条件: 開発ビルド (`version = "dev"`)、インターネット未接続時
- リリースアセット: `pi-obd-meter_linux_arm64.tar.gz`（goreleaser 互換命名）

## 車両固有の設定（config.json）

以下はすべてconfig.jsonで設定する。コード内にハードコードしない。

- `serial_port`: ELM327のシリアルポート (例: /dev/rfcomm0)
- `webhook_url`: GAS WebアプリのURL
- `engine_displacement_l`: エンジン排気量 (例: ZJ-VE=1.3) — 燃費推定に使用
- `max_speed_kmh`: 速度メーター最大値 (例: 180)
- `initial_odometer_km`: 初期ODO値 (km)
- `web_static_dir`: Web UI配信元（空 = 埋め込みファイル使用、開発時にパス指定可）
- `maintenance_reminders`: メンテナンス項目の配列（ID, 名前, タイプ, 間隔, 警告閾値）

### 開発元の確認車両
- マツダ DYデミオ DBA-DY3W / ZJ-VE 1.3L 91PS / 1,090kg / 4AT / CAN 2.0B

## 注意事項

- `go.mod` のモジュール名は `github.com/hashimoto/pi-obd-meter`
- エラーメッセージ・ログは日本語
- config.json の webhook_url は実際のGAS WebアプリURLに差し替えが必要
- ELM327のBluetoothアドレスは実機に合わせて rfcomm bind する
- 車両固有の定数をコード内にハードコードしないこと（config.jsonから読む）
