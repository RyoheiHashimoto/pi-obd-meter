# CLAUDE.md — DYデミオ車載メーター

## プロジェクト概要

OBD-2対応車向けの車載メーター + 走行記録システム。
Raspberry Pi 4 + ELM327 (Bluetooth) で速度・RPM・スロットル・インマニ圧・瞬間燃費を5インチLCDにリアルタイム表示。
走行距離・メンテナンス状態を Google Sheets に自動記録し、スマホから給油記録・ODO補正・メンテナンス管理を行う。
開発・動作確認はDYマツダデミオ（DBA-DY3W, ZJ-VE 1.3L）で行っている。

## 技術スタック

- **言語:** Go（車載バイナリ、`log/slog` 構造化ログ）+ HTML/CSS/JS（メーターUI）+ Google Apps Script（データ記録 + Webダッシュボード）
- **ターゲット:** Raspberry Pi 4 Model B 2GB (ARM64, Raspberry Pi OS Lite 64-bit)
- **ディスプレイ:** ELECROW 5インチ IPS HDMI タッチモニター (800×480) — Pi USBから給電可 (5V/1A)
- **OBD通信:** ELM327 V1.5 Bluetooth 2.0 → /dev/rfcomm0 (SPPシリアル)
- **データ保存:** Google Sheets (GAS webhook)。ローカル状態ファイルはアトミック書き込み（tmp+rename+fsync）で電源断保護

## ブランチ戦略

- **develop**: 日常開発ブランチ。push → CI + dev ビルド自動デプロイ（CI成功時のみ）
- **main**: リリースブランチ。**ブランチ保護あり**（CI `test` ジョブ通過必須、直接push不可、admin bypass可）
- **GASデプロイ**: develop/main push時（`gas/` 変更時）に自動デプロイ
- **Pi 自動更新**: `auto-update.timer` (systemd) が2分間隔で GitHub をポーリング、新ビルド検出時に自動更新

### ワークフロー
1. develop で開発・`make deploy`（即時）または push（CI成功後に2分以内に自動デプロイ）
2. リリース時: `make release` → PR作成 (develop→main) → CI待機 → マージ → タグ push
3. GitHub Actions が ARM64 バイナリをビルド・リリース
4. Pi が2分以内に検出して自動更新

### GitHub Actions ワークフロー

| ワークフロー | ファイル | トリガー |
|---|---|---|
| CI | `ci.yml` | push / PR (main, develop) |
| Deploy Dev | `deploy-dev.yml` | CI成功 (develop) — `workflow_run` トリガー |
| Release | `release.yml` | タグ `v*` push |
| Deploy GAS | `deploy-gas.yml` | push (main, develop) + `gas/` 変更 |
| Claude Review | `claude-review.yml` | PR open / `@claude` メンション |

### Pi 自動更新の仕組み
- `scripts/auto-update.sh` が2分間隔で実行（`configs/auto-update.timer`）
- Stable release → `dev-latest` pre-release の順にチェック
- 新ビルド検出時: ダウンロード → バイナリ + web/static 差し替え → サービス再起動
- バージョン管理: `/var/lib/pi-obd-meter/{release-version,dev-version}`

## ビルド & デプロイ

```bash
# 開発（Mac上で実行、develop ブランチ）
make deploy          # ビルド + rsync転送 + サービス再起動
make logs            # リアルタイムログ表示
make ssh             # ラズパイにSSH接続
make status          # サービス状態確認
make restart         # サービス再起動（転送なし）

# リリース（PRベース: develop→main → タグ push → GitHub Actions → Pi 自動更新）
make release         # パッチ自動インクリメント (v0.3.1 → v0.3.2)
make release V=v1.0.0  # バージョン明示指定

# GAS デプロイ (clasp — develop/main push時にCI/CDで自動実行)
make deploy-gas      # gas/webhook.gs を GAS に push（ローカルから手動実行時）

# ビルド・テスト
make build           # ローカルビルド
make test            # テスト実行
make lint            # golangci-lint
make check           # lint + test

# 初回セットアップ（deploy.sh 直接）
./scripts/deploy.sh setup
```

ターゲット: `GOOS=linux GOARCH=arm64`
デプロイ先: `$PI_HOST:/opt/pi-obd-meter/`（デフォルト: `laurel@pi-obd-meter.local`、`scripts/deploy.sh` で設定）

## ディレクトリ構成

```
cmd/
  pi-obd-meter/
    main.go                   エントリポイント + graceful shutdown
    app.go                    アプリケーションロジック + OBDポーリングループ
    api.go                    ローカルHTTP API (/api/realtime, /api/config 等)
    config.go                 設定読み込み + バリデーション
    fuel.go                   燃費計算 (MAF / MAP Speed-Density / 負荷×RPM)
    filter.go                 OBDスパイク除去フィルター
    update.go                 自動更新 (go-selfupdate)
  pi-obd-scanner/main.go     対応PIDスキャナー（診断・初期確認用、DTC読取含む）

internal/
  obd/
    elm327.go               ELM327シリアル通信（AT初期化、PIDリクエスト、マルチPIDバッチ）
    pids.go                 OBD-2 PID定義（RPM, 速度, 負荷, スロットル, 水温, MAF, MAP）
    dtc.go                  DTC読取（Mode 03）。スキャナーコマンドから使用
  trip/
    tracker.go              トリップ追跡。車速積分で走行距離、燃料消費量を積算。0.1kmごとに状態永続化
  sender/
    client.go               GAS Webhook送信。Send/SendWithResponse + メモリ内リトライキュー（最大100件、指数バックオフ）
  display/
    brightness.go           xrandr経由の輝度制御。時刻ベース自動調整（config で設定可能）
  maintenance/
    reminder.go             走行距離/日付ベースのメンテナンスリマインダー
  atomicfile/
    write.go                アトミックファイル書き込み（tmp + rename + fsync）

web/
  embed.go                  go:embed で static/ をバイナリに埋め込み
  static/
    meter.html              メーター画面HTML
    meter.css               CSS Custom Properties でテーマ管理
    js/
      main.js               エントリポイント + APIポーリング + Toast + キオスク終了
      gauge.js              速度ゲージ(針+アーク) + RPMアーク + スロットルアーク + ギア/レンジ表示 + 下部インジケーター(TEMP/TRIP/ECO) + 60fps LERP補間
      indicators.js         右パネルインジケーター (GEAR/ECO/TRIP/TEMP/MAP/MAF/O2/TRIM)
    fonts/
      Orbitron-*.ttf        速度・数値表示フォント
      ShareTechMono-*.woff2 リードアウト表示フォント

gas/
  webhook.gs                Google Apps Script。doPost(トリップ/メンテ受信) + doGet(スマホ用ダッシュボード)
  .clasp.json               clasp プロジェクト設定（スクリプトID）
  appsscript.json           GAS マニフェスト（タイムゾーン・ランタイム・webapp設定）

configs/
  config.json               アプリ設定（シリアルポート、webhook URL、車両パラメータ等）
  pi-obd-meter.service      systemd メインサービス
  kiosk.service             systemd キオスクモード
  kiosk.sh                  Chromiumキオスクモード起動スクリプト（WiFiガード付き）
  auto-update.service       systemd 自動更新 (oneshot)
  auto-update.timer         systemd 自動更新タイマー（2分間隔）

scripts/
  deploy.sh                 開発・デプロイスクリプト
  auto-update.sh            Pi 自動更新スクリプト（GitHub Releases チェック）

docs/
  setup-guide.md            セットアップガイド
  development.md            開発・CI/CD・リリースフロー
  configuration.md          config.json 全パラメータ・車種チューニング
  calculation-logic.md      算出ロジック・閾値一覧
  wifi-troubleshooting.md   Wi-Fi トラブルシューティング
```

## アーキテクチャ上の重要な決定

### データフロー
```
ECU → ELM327 (CAN 2.0B) → Pi (BT rfcomm) → meter.html（車載LCD: 速度/RPM/スロットル/MAP/燃費）
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

### 瞬間燃費推定（3段階フォールバック）
- **MAFセンサー搭載車**: MAF (g/s) → 燃料消費量 (L/h) → 燃費 (km/L)（最も正確）
- **MAPセンサー搭載車**: Speed-Density法: MAP/大気圧 × RPM/2 × 排気量 → 吸入空気量 → 燃費
- **上記なし（DYデミオ等）**: エンジン負荷 × RPM × 排気量から吸入空気量を推定
- 物理定数: 理論空燃比 14.7、ガソリン密度 750 g/L、空気密度 1.225 g/L
- エンブレ判定: MAP < 35 kPa（優先） or 負荷 < 5%（フォールバック）
- 低速域 (<10 km/h) では燃費表示しない（クリープ等でのノイズ回避）

### SD書き込み低減
- swap無効化（`deploy.sh setup` で `dphys-swapfile` を停止・無効化）
- 状態ファイル（maintenance.json, trip_state.json）: アトミック書き込み（tmp+rename+fsync）で電源断保護
- トリップ状態: 0.1km（100m）走行ごとに保存（距離ベース）
- 送信失敗データ: メモリ内キュー（最大100件、指数バックオフ 5m→30m）
- ログ: journald（RAM上）
- 起動時にGASからODO復元（`type: "restore"`）。電源断でリセットされた場合のフォールバック

### ELM327通信
- Bluetooth 2.0 Classic (SPP)。BLEはGATT複雑で不採用
- rfcomm bind → /dev/rfcomm0 でシリアルポートとして扱う
- マルチPIDバッチ: 1リクエストで複数PID取得（6-7往復 → 2往復）
- ECUレスポンスが律速（50-100ms/PID）。BT遅延（20-50ms）は支配的でない

### メーターUI
- 800×480 全画面、速度の270° SVGアークゲージ
- 内側にスロットルアーク（HSL連続グラデーション: 青→赤）
- 外側にRPMアーク（レッドゾーン背景付き）
- ゲージ左上にレンジ(P/R/N/D/S/L)、右上にギア番号、その下にHOLD/LOCKラベル
- 下部にTEMP(左)・TRIP(中)・ECO(右) アイコン付きインジケーター
- 右パネルにインジケーター8項目（GEAR/ECO/TRIP/TEMP/MAP/MAF/O2/TRIM）
- CSS/JS分離済み（meter.html + meter.css + js/main.js + js/gauge.js + js/indicators.js）
- CSS Custom Properties で色・レイアウトを一元管理
- requestAnimationFrame で60fps LERP補間、OBDデータは200msポーリング
- 時刻ベースで自動輝度調整（xrandr --brightness、config で設定可能）
- 画面3秒長押しでキオスク終了

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

### データ送信の責任分離
- **トリップ完了**: Pi → GAS Webhook (type: "trip") → Google Sheets
- **メンテナンス状態**: Pi → GAS Webhook (type: "maintenance") → Google Sheets（始動時 + 5分間隔）
- **状態復元**: Pi起動時 → GAS Webhook (type: "restore") → ODO/最終給油距離を取得
- **リアルタイム表示**: Pi → meter.html（車載LCD、ローカルHTTP API経由）
- **給油記録**: スマホ → GAS doGet/doPost → Google Sheets（手動入力、燃費自動算出）
- **ODO補正・メンテリセット**: スマホ → GAS → Pi（次回メンテ送信レスポンスで反映）

### ローカルAPI
- `GET /api/config` — max_speed_kmh, ECO閾値, スロットル設定, version
- `GET /api/realtime` — 速度・RPM・負荷・スロットル・MAP・燃費・トリップ・接続状態
- `GET /api/maintenance` — メンテナンス全項目の進捗
- `GET /api/health` — OBD/WiFi接続・キューサイズ・uptime・バージョン
- `POST /api/kiosk/stop` — キオスクモード終了

### Graceful Shutdown
- SIGINT/SIGTERM受信時にトリップ状態とメンテナンス状態を保存してから終了
- バージョン: `-ldflags "-X main.version=vX.Y.Z"` でビルド時に埋め込み

### Web UI 埋め込み（go:embed）
- `web/embed.go` で `web/static/` をバイナリに埋め込み（`go:embed static`）
- 本番: 埋め込みファイルから配信（バイナリ1つで完結）
- 開発: config.json の `web_static_dir` にパスを指定すればファイルシステムから配信

### 自動更新（2系統）
- **起動時 (go-selfupdate)**: `cmd/pi-obd-meter/update.go` でGitHub Releasesをチェック、バイナリ差し替え
- **2分間隔 (auto-update.sh)**: `scripts/auto-update.sh` (systemd timer) でstable/devビルドをチェック、バイナリ + web/static 差し替え
- リリースアセット: `pi-obd-meter_linux_arm64.tar.gz`（selfupdate 互換命名）

### GAS 自動デプロイ（clasp）
- `gas/` 配下の変更を main/develop に push → GitHub Actions が自動実行
- ESLint で構文チェック → `clasp push` (HEAD) → `clasp deploy` (本番)
- ESLint でエラー検出時はデプロイ中止
- `make deploy-gas` でローカルから手動 push も可能
- 初回のみ `clasp login` で Google OAuth 認証が必要（`~/.clasprc.json`）

## 車両固有の設定（config.json）

以下はすべてconfig.jsonで設定する。コード内にハードコードしない。

- `serial_port`: ELM327のシリアルポート (例: /dev/rfcomm0)
- `webhook_url`: GAS WebアプリのURL
- `engine_displacement_l`: エンジン排気量 (例: ZJ-VE=1.3) — 燃費推定に使用
- `max_speed_kmh`: 速度メーター最大値 (例: 180)
- `initial_odometer_km`: 初期ODO値 (km)
- `web_static_dir`: Web UI配信元（空 = 埋め込みファイル使用、開発時にパス指定可）
- `throttle_idle_pct`: スロットルアイドル開度 (例: 11.5) — スロットル表示のゼロ基準
- `throttle_max_pct`: スロットル最大開度 (例: 75) — スロットル表示の100%基準
- `fuel_tank_l`: 燃料タンク容量 (例: 40) — トリップ警告閾値の導出に使用
- `fuel_rate_correction`: 燃料レート補正係数 (例: 1.3) — 理論値と実燃費の乖離を補正
- `obd_protocol`: OBDプロトコル (例: "6" = CAN 11bit 500k)
- `poll_interval_ms`: ポーリング間隔 (例: 500)
- `local_api_port`: ローカルAPIポート (デフォルト: 9090)
- `brightness`: 輝度スケジュール設定（`hdmi_output` + `schedule[]`）
- `maintenance_reminders`: メンテナンス項目の配列（ID, 名前, タイプ, 間隔, 警告閾値）

### 開発元の確認車両
- マツダ DYデミオ DBA-DY3W / ZJ-VE 1.3L 91PS / 1,090kg / 4AT / CAN 2.0B

## 注意事項

- `go.mod` のモジュール名は `github.com/hashimoto/pi-obd-meter`
- エラーメッセージ・ログは日本語
- config.json の webhook_url は実際のGAS WebアプリURLに差し替えが必要
- ELM327のBluetoothアドレスは実機に合わせて rfcomm bind する
- 車両固有の定数をコード内にハードコードしないこと（config.jsonから読む）
