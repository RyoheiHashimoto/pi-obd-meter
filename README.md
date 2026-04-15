# pi-obd-meter

**OBD-2 / CAN 直結 車載メーター + 走行記録 + スマホダッシュボード**

Raspberry Pi 4 + CAN HAT で速度・RPM・スロットル・インマニ圧・瞬間燃費を5インチLCDにリアルタイム表示。
走行距離・メンテナンス状態を Google Sheets に自動記録し、スマホから給油記録・ODO補正・メンテナンス管理ができる。

描画は **WPE WebKit (cog)** を Wayland コンポジタ **labwc** 上で実行するキオスク構成。SVG `<use>` ベースの fake bloom で
軽量かつグローのある質感、オープニングアニメ Phase 1〜3、ACC 時の全体消灯、フリーズ自動復帰の watchdog 内蔵。

## 対応車種

**CAN 11bit 500kbaud (標準的な OBD-2 CAN) の車ならほぼ全車対応。** 2000年代半ば以降の国産車であれば大抵動く（日本車のOBD-2義務化は2010年、CAN 義務化は2008年）。ELM327 Bluetooth モードはフォールバック。

### 推奨: CAN 直結 (SocketCAN)

CAN HAT (例: Waveshare RS485 CAN HAT, PiCAN2 等) を Pi の GPIO に接続し、OBD ポートから CAN High/Low を直結。SocketCAN (`can0`) でカーネルが全フレームをパッシブ受信する。

- **レイテンシ極低** (Bluetooth なし = 数ms で値更新)
- **全 CAN フレーム可視** (ECU 間通信、AT 制御、BCM 等も取れる)
- **対応 PID が多い** (OBD クエリに頼らず CAN 生フレーム直読み)

DYデミオ (DBA-DY3W) での検出内訳:

| CAN ID | 内容 | 周期 |
|---|---|---|
| 0x201 | RPM + 車速 + エンジン負荷 | 100Hz |
| 0x230 | ギア + ギア比 + トルク要求 | 40Hz |
| 0x231 | レンジ (P/R/N/D/S/L) + HOLD + TC ロックアップ + 変速中 | 40Hz |
| 0x420 | 水温 + 距離パルス | 10Hz |
| 0x430 | オルタ負荷 + 電圧 + 大気圧 | 20Hz |
| 0x4B0 | 4輪個別速度 | 71Hz |
| OBD PID | MAF / 燃料トリム / 点火タイミング / 吸気温度 / O2 等 18 種 | クエリ時 |

### フォールバック: ELM327 Bluetooth

Classic Bluetooth (SPP) で `/dev/rfcomm0` 経由。CAN HAT がない環境での簡易構成。対応 PID は少なく、レイテンシは 50-100ms/PID。

### 対応できない車

- EV / HV の電動走行モード（エンジン回転なし）
- 1996年以前の旧車（OBD-2未搭載）

### 動作確認済み車種

| 車種 | 型式 | エンジン | プロトコル | 検出PID数 |
|------|------|----------|---------------|-----------|
| マツダ DYデミオ | DBA-DY3W | ZJ-VE 1.3L | CAN 11bit 500kbaud | 28 |

## 機能

### 車載メーター (5インチ LCD)
- 速度の270° SVGアークゲージ（速度帯で色が変化）
- スロットル開度アーク（内側、HSLグラデーション）
- RPMアーク（外側、レッドゾーン背景付き）
- ゲージ左上にレンジ、右上にギア番号、HOLD/LOCKラベル
- 右パネル: バキューム計 (-1.0〜0 Bar) + 4行インジケーター (ECO/TEMP/TRIP/OIL)
- **fake bloom** による軽量な光の尾 (針は 0.6s 残像追従、アークは静的幅広グロー)
- 大数値 (speed/RPM/MAP) にオフセット影で立体感
- **オープニングアニメ** Phase 1 (針 sweep out) → Phase 2 (sweep back) → Phase 3 (テキストフェードイン) → 通常
- **ACC 検出** (CAN エンジン ECU 1秒無通信) で全体フェードアウト、再接続で自動復帰
- **フリーズ watchdog** (rAF 3秒停止で `location.reload()`) + エラー自動ロギング
- WebSocket 優先 (/ws/realtime) + HTTP フォールバック
- 60fps、時刻ベース自動輝度調整
- 画面3秒長押しでキオスク終了

### 自動記録 (Google Sheets)
- **トリップ記録**: 走行距離・最高速度・平均速度・走行時間・アイドル時間・燃料消費量
- **メンテナンス状態**: 走行距離/日付ベースのリマインダー（エンジン始動時 + 5分間隔で送信）
- **燃費推定**: MAF > MAP (Speed-Density) > 負荷×RPM の優先順位で自動選択

### スマホダッシュボード (GAS Webアプリ)
- 給油記録（日付・距離・給油量 → 燃費自動算出）
- ODO補正（メーター実値との差分を補正）
- メンテナンス進捗確認・リセット
- ダークテーマ、ホーム画面追加対応

## アーキテクチャ

![アーキテクチャ](docs/architecture.svg)

### データフロー

```
ECU ──CAN──> CAN HAT ──SocketCAN──> pi-obd-meter (Go)
                                      ├──WebSocket──> meter.html (cog/WPE WebKit on labwc)
                                      └──HTTPS──────> GAS Webhook ──> Google Sheets
                                                        ↕
                                   スマホブラウザ ──> GAS doGet (給油記録/ODO補正/メンテ管理)
```

### キオスク構成

```
systemd ──> greetd (autologin as laurel)
              └─> labwc (Wayland compositor)
                   ├─> swaybg (黒背景)
                   └─> cog --ozone-platform=wayland --kiosk http://localhost:9090/meter.html
```

- `greetd` がユーザー自動ログイン、`labwc` が Wayland コンポジタ、`cog` が WPE WebKit ランチャ
- X11/lightdm/Chromium は使わない (Wayland 白フラッシュ回避、低メモリ)
- フロントは HTML+CSS+SVG+JS、埋め込み (go:embed) で単一バイナリ配信

### 通信構成

![通信構成図](docs/communication-diagram-v2.svg)

## ハードウェア

### パーツリスト (CAN 直結構成)

| # | パーツ | 選定品 | 目安価格 |
|---|--------|--------|---------|
| 1 | CAN HAT | Waveshare RS485 CAN HAT (MCP2515) | ~2,000 |
| 2 | Raspberry Pi | Pi 4 Model B 2GB (技適あり) | ~7,000-9,200 |
| 3 | ケース | GeeekPi アルミケース (デュアルファン) | ~2,000 |
| 4 | ディスプレイ | ELECROW 5インチ IPS HDMI (800x480) | ~5,699 |
| 5 | microSD | SanDisk MAX ENDURANCE 32GB | ~1,200 |
| 6 | モニター固定 | スマホホルダー | ~300-1,000 |
| - | 電源 | シガーソケット USB-C (5V/3A) | - |
| - | OBD ケーブル | OBD-II to DB9 or 直結線 | ~500 |

**合計: 約18,000-20,000円**

### 選定理由

- **CAN HAT MCP2515**: SPI 接続、12MHz クリスタル (実装差に注意、詳細 [docs/configuration.md](docs/configuration.md))。BT より遥かに低レイテンシ・高情報量
- **Pi 4 2GB**: ARM64, WiFi/BT内蔵。WPE WebKit + labwc の常駐負荷に耐える
- **アルミケース+デュアルファン**: ダッシュボード上は79-85°Cに達するため
- **MAX ENDURANCE**: 書込耐久15,000時間。journald/状態ファイル/auto-update での摩耗に耐える
- **ELECROW 5インチ IPS**: HDMI接続、IPSパネル(178°広視野角)、Pi USBから給電可

## セットアップ

詳細は [docs/setup-guide.md](docs/setup-guide.md) を参照。

### クイックスタート (CAN 直結)

```bash
# 1. CAN HAT 有効化 (Pi 側)
# /boot/firmware/config.txt に:
#   dtparam=spi=on
#   dtoverlay=mcp2515-can0,oscillator=12000000,interrupt=25
# /etc/network/interfaces.d/can0 で ip link up can0 bitrate 500000 を設定

# 2. 初回セットアップ（ディレクトリ作成 + systemd登録 + swap無効化）
./scripts/deploy.sh setup

# 3. Google Apps Script のセットアップ
#    GASエディタでwebhook.gsを貼り付けてデプロイ
#    → URLをconfigs/config.json の webhook_url に設定
#    ※ 以降の更新は make deploy-gas または git push で自動反映

# 4. デプロイ
make deploy
```

### キオスク関連パッケージ

```bash
sudo apt install -y greetd labwc cog swaybg wlr-randr
```

### ディスプレイ設定

ELECROW 5インチ用に `/boot/firmware/config.txt` へ追記:

```
hdmi_force_hotplug=1
max_usb_current=1
hdmi_drive=1
hdmi_group=2
hdmi_mode=87
hdmi_cvt 800 480 60 6 0 0 0
```

## 車両設定

`configs/config.json` で車両ごとのパラメータを設定する。全パラメータの詳細は [docs/configuration.md](docs/configuration.md) を参照。

```json
{
  "can_interface": "can0",
  "serial_port": "/dev/rfcomm0",
  "webhook_url": "https://script.google.com/macros/s/XXXXXX/exec",
  "engine_displacement_l": 1.3,
  "max_speed_kmh": 180,
  "initial_odometer_km": 98000,
  "fuel_rate_correction": 1.3,
  "throttle_idle_pct": 1,
  "throttle_max_pct": 197,
  "fuel_tank_l": 40,
  "obd_protocol": "6",
  "poll_interval_ms": 50,
  "local_api_port": 9090,
  "maintenance_reminders": [
    { "id": "oil_change", "name": "エンジンオイル交換", "type": "distance", "interval_km": 3000, "warning_pct": 0.8 }
  ]
}
```

`can_interface` が空文字列だと ELM327 (`serial_port`) フォールバック。CAN 直結時は `"can0"` を指定。

他車種への適用時に必要なチューニング項目は [docs/calculation-logic.md](docs/calculation-logic.md) を参照。

## プロジェクト構成

```
pi-obd-meter/
├── cmd/
│   ├── pi-obd-meter/         # メインアプリ（車載）
│   │   ├── main.go           #   エントリポイント + graceful shutdown + SSH 復旧
│   │   ├── app.go            #   アプリケーションロジック
│   │   ├── api.go            #   ローカル HTTP API (/api/realtime, /api/client-error 等)
│   │   ├── can_loop.go       #   CAN 直結ループ (SocketCAN)
│   │   ├── obd_loop.go       #   ELM327 ループ (フォールバック)
│   │   ├── config.go         #   設定読み込み + バリデーション
│   │   ├── fuel.go           #   燃費計算 (MAF / MAP / 負荷×RPM)
│   │   ├── filter.go         #   OBDスパイク除去フィルター
│   │   └── update.go         #   自動更新 (go-selfupdate)
│   └── pi-obd-scanner/       # PIDスキャナー（診断用）
├── internal/
│   ├── can/                  # SocketCAN ラッパ + フレームデコーダ
│   ├── obd/                  # ELM327通信、PID定義、DTC
│   ├── trip/                 # トリップ追跡（車速積分、燃料積算、状態永続化）
│   ├── sender/               # GAS Webhook送信（リトライキュー）
│   ├── display/              # 画面輝度制御（時刻ベース、wlr-randr）
│   ├── maintenance/          # メンテナンスリマインダー
│   └── atomicfile/           # アトミックファイル書き込み
├── web/
│   ├── embed.go              # go:embed でstatic/をバイナリに埋め込み
│   └── static/
│       ├── meter.html        # メーター画面HTML
│       ├── meter.css         # CSS (Custom Properties + ACC フェード + 起動アニメ)
│       ├── js/
│       │   ├── main.js       # エントリ + WebSocket + watchdog + エラー通知
│       │   ├── gauge.js      # 速度ゲージ + fake bloom + 針残像 + オフセット影
│       │   └── indicators.js # 右パネル バキューム + 4行インジケーター + アイコン bloom
│       └── fonts/            # Orbitron, ShareTechMono
├── gas/
│   ├── webhook.gs            # Google Apps Script（記録 + Webダッシュボード）
│   ├── .clasp.json
│   └── appsscript.json
├── configs/
│   ├── config.json           # 実機 config
│   ├── config.mac.json       # Mac ローカル demo 用
│   ├── pi-obd-meter.service  # systemd メインサービス
│   ├── cog-kiosk.sh          # cog キオスク起動スクリプト (labwc autostart から)
│   ├── kiosk.sh              # (互換: 旧 Chromium 版)
│   ├── auto-update.service   # systemd 自動更新 (oneshot)
│   └── auto-update.timer     # systemd 自動更新タイマー (2分間隔)
├── scripts/
│   ├── deploy.sh             # 開発・デプロイスクリプト
│   ├── auto-update.sh        # Pi 自動更新スクリプト
│   ├── capture-screenshots.sh
│   └── drive-test.sh         # 実走テスト (candump + スクショ)
├── docs/                     # ドキュメント
├── .github/workflows/        # CI/CD (ci.yml, deploy-dev.yml, release.yml, deploy-gas.yml)
├── CLAUDE.md                 # AI開発支援用プロジェクト説明
└── go.mod
```

## 開発

詳細は [docs/development.md](docs/development.md) を参照。

### ローカル Demo (Mac)

```bash
# CAN 接続を disable した Mac 用 config を使用
go run ./cmd/pi-obd-meter -demo -config configs/config.mac.json

# Safari で開く (WPE WebKit と同じエンジン = 実機に近い挙動)
open -a Safari http://localhost:9090/meter.html
```

### Make ターゲット

```bash
make deploy          # ビルド + rsync転送 + サービス再起動
make logs            # リアルタイムログ表示
make ssh             # ラズパイにSSH接続
make status          # サービス状態確認
make restart         # サービス再起動（転送なし）

make test            # テスト実行
make lint            # golangci-lint
make check           # lint + test
make build           # ローカルビルド

make deploy-gas      # gas/webhook.gs を GAS に push
make release         # パッチ自動インクリメント (develop → main PR)
make release V=v1.1.0  # バージョン明示指定
```

### CI/CD

![CI/CD パイプライン](docs/cicd.svg)

| ワークフロー | トリガー | 内容 |
|---|---|---|
| **CI** | push / PR (main, develop) | lint + test + build (host + ARM64) |
| **Deploy Dev** | CI成功 (develop) | ARM64ビルド → `dev-latest` pre-release |
| **Release** | タグ `v*` push | ARM64ビルド → GitHub Release |
| **Deploy GAS** | push (main, develop) + `gas/` 変更 | ESLint → clasp push → clasp deploy |
| **Claude Review** | PR open / `@claude` メンション | AIコードレビュー |

**main ブランチは保護されており**、CI (`test` ジョブ) の通過が必須。

### Pi 自動更新

`auto-update.timer` (systemd) が 2分間隔で GitHub Releases をポーリング。
新ビルド検出時にダウンロード → バイナリ + `web/static/` 差し替え → サービス再起動を自動実行。

1. Stable release (`latest`) を優先チェック
2. なければ dev build (`dev-latest` pre-release) をチェック

### 起動時間

NetworkManager 遅延起動 (10s timer) + plymouth 削除 + 不要サービス停止で、
`systemd-analyze` 上 **約 7 秒 (kernel 2.2s + userspace 4.8s)**。

メーター表示完了まで ~9-10s (cog + WebKit 起動含む)。詳細は #109。

## ドキュメント

| ドキュメント | 内容 |
|---|---|
| [docs/setup-guide.md](docs/setup-guide.md) | Raspberry Pi 初期セットアップ、CAN HAT 設定、ディスプレイ、GAS設定 |
| [docs/development.md](docs/development.md) | ブランチ戦略、CI/CD、リリースフロー、デプロイ |
| [docs/configuration.md](docs/configuration.md) | config.json 全パラメータ、車種チューニング |
| [docs/calculation-logic.md](docs/calculation-logic.md) | 燃費推定・インジケーター・閾値の算出ロジック |
| [docs/wifi-troubleshooting.md](docs/wifi-troubleshooting.md) | Wi-Fi 接続問題の診断・復旧手順 |

## ライセンス

MIT
