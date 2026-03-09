# pi-obd-meter

**OBD-2 車載メーター + 走行記録 + スマホダッシュボード**

Raspberry Pi + ELM327 で速度・RPM・負荷・スロットル・瞬間燃費を5インチLCDにリアルタイム表示。
走行距離・メンテナンス状態を Google Sheets に自動記録し、スマホから給油記録・ODO補正・メンテナンス管理ができる。

## 対応車種

**OBD-2ポートがあり、ELM327で通信できる車ならほぼ全車対応。** 2000年代半ば以降の国産車であれば大抵動く（日本車のOBD-2義務化は2010年だが、それ以前から搭載している車も多い）。

### 必要なPID

| PID | 用途 | 必須 |
|-----|------|------|
| 0x0C | RPM | メーター表示 + 燃費推定 |
| 0x0D | 車速 | メーター表示 + 距離積算 |
| 0x04 | エンジン負荷 | メーター表示 + 燃費推定 |
| 0x11 | スロットル開度 | メーター表示 |

`pi-obd-scanner` で事前に対応PIDを確認できる:

```bash
./pi-obd-scanner -port /dev/rfcomm0
```

### 対応できない車

- EV / HV の電動走行モード（エンジン回転なし）
- 1996年以前の旧車（OBD-2未搭載）

### 動作確認済み車種

| 車種 | 型式 | エンジン | OBDプロトコル | 検出PID数 |
|------|------|----------|---------------|-----------|
| マツダ DYデミオ | DBA-DY3W | ZJ-VE 1.3L | CAN 11bit 500kbaud (ATSP6) | 28 |

## 機能

### 車載メーター (5インチ LCD)
- 速度 + RPM の270° SVGアークゲージ
- スロットル・エンジン負荷の縦バー（ラギング指数で緑/橙/赤に色分け）
- 瞬間燃費インジケーター（ECO/NORMAL/POWER）
- 水温・トリップ距離表示
- 60fps補間アニメーション、時刻ベース自動輝度調整

### 自動記録 (Google Sheets)
- **トリップ記録**: 走行距離・最高速度・平均速度・走行時間・アイドル時間
- **メンテナンス状態**: 走行距離/日付ベースのリマインダー（エンジン始動ごとに送信）
- **瞬間燃費推定**: エンジン負荷 × RPM × 排気量から燃料消費量を推算

### スマホダッシュボード (GAS Webアプリ)
- 給油記録（日付・距離・給油量 → 燃費自動算出）
- ODO補正（メーター実値との差分を補正）
- メンテナンス進捗確認・リセット
- ダークテーマ、ホーム画面追加対応

## データフロー

```
ECU → ELM327 (BT) → Raspberry Pi → meter.html (車載LCD: 速度/RPM/負荷/スロットル/燃費)
                                   → GAS Webhook → Google Sheets (トリップ/メンテ記録)
                                                 ↕
                               スマホブラウザ → GAS doGet (給油記録/ODO補正/メンテ管理)
```

## ハードウェア

### パーツリスト

| # | パーツ | 選定品 | 目安価格 |
|---|--------|--------|---------|
| 1 | ELM327 | Zappa V1.5 BT2.0 スイッチ付き | ~1,550 |
| 2 | Raspberry Pi | Pi 4 Model B 2GB (技適あり) | ~7,000-9,200 |
| 3 | ケース | GeeekPi アルミケース (デュアルファン) | ~2,000 |
| 4 | ディスプレイ | ELECROW 5インチ IPS HDMI (800x480) | ~5,699 |
| 5 | microSD | SanDisk MAX ENDURANCE 32GB | ~1,200 |
| 6 | モニター固定 | スマホホルダー | ~300-1,000 |
| - | 電源 | シガーソケット USB-C (5V/3A) | - |

**合計: 約18,000-20,000円**

### 選定理由

- **ELM327 BT2.0**: Classic Bluetooth (SPP) で rfcomm 互換。BLEはGATT複雑で不採用
- **Pi 4 2GB**: ARM64, WiFi/BT内蔵。2GBで十分
- **アルミケース+デュアルファン**: ダッシュボード上は79-85°Cに達するため
- **MAX ENDURANCE**: 書込耐久15,000時間。ドラレコ用途想定のSDで車載に適合
- **ELECROW 5インチ IPS**: HDMI接続、IPSパネル(178°広視野角)、Pi USBから給電可

## セットアップ

詳細は [docs/deploy-guide.md](docs/deploy-guide.md) を参照。

### クイックスタート

```bash
# 1. 初回セットアップ（ディレクトリ作成 + systemd登録）
./scripts/deploy.sh setup

# 2. Google Apps Script にwebhook.gsを貼り付けてデプロイ
#    → URLをconfigs/config.json の webhook_url に設定

# 3. デプロイ
./scripts/deploy.sh deploy
```

### ELM327 Bluetooth 接続の注意

- `hciconfig hci0 class 0x200000` と `hciconfig hci0 piscan` を先に実行（BR/EDRスキャン用）
- `hcitool scan` でClassic Bluetoothのスキャンを行う（`bluetoothctl scan on` はBLEのみの場合がある）
- DYデミオは CAN 11bit 500kbaud (ATSP6) を明示指定する必要がある

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

`configs/config.json` で車両ごとのパラメータを設定する。

```json
{
  "serial_port": "/dev/rfcomm0",
  "webhook_url": "https://script.google.com/macros/s/XXXXXX/exec",
  "engine_displacement_l": 1.3,
  "max_speed_kmh": 180,
  "initial_odometer_km": 98500,
  "maintenance_reminders": [
    { "id": "oil_change", "name": "エンジンオイル交換", "type": "distance", "interval_km": 3000, "warning_pct": 0.8 }
  ]
}
```

| パラメータ | 説明 |
|-----------|------|
| `serial_port` | ELM327のシリアルポート |
| `webhook_url` | GAS WebアプリのURL |
| `engine_displacement_l` | エンジン排気量 (L) — 燃費推定に使用 |
| `max_speed_kmh` | 速度メーター最大値 |
| `initial_odometer_km` | 初期ODO値 (km) |
| `maintenance_reminders` | メンテナンスリマインダー項目 |

## プロジェクト構成

```
pi-obd-meter/
├── cmd/
│   ├── pi-obd-meter/        # メインアプリ（車載）
│   └── pi-obd-scanner/      # PIDスキャナー（診断用）
├── internal/
│   ├── obd/               # ELM327通信、PID定義、DTC
│   ├── trip/              # トリップ追跡（車速積分で走行距離を積算）
│   ├── sender/            # Google Sheets送信（GAS webhook + リトライキュー）
│   ├── display/           # 画面輝度制御
│   └── maintenance/       # メンテナンスリマインダー（距離/日付ベース）
├── web/
│   ├── embed.go           # go:embed でstatic/をバイナリに埋め込み
│   └── static/
│       ├── meter.html     # メーター画面（5インチLCD, 60fps）
│       ├── meter.css
│       └── meter.js
├── gas/
│   └── webhook.gs         # Google Apps Script（記録 + Webダッシュボード）
├── configs/
│   ├── config.json
│   └── pi-obd-meter.service
├── docs/
│   └── deploy-guide.md
├── scripts/
│   └── deploy.sh         # 開発・デプロイスクリプト
├── CLAUDE.md
└── go.mod
```

## deploy.sh コマンド一覧

```bash
./scripts/deploy.sh <command>
```

| コマンド | 用途 |
|---------|------|
| `build` | クロスコンパイル (ARM64) |
| `deploy` | ビルド + rsync転送 + サービス再起動 |
| `setup` | 初回セットアップ（swap無効化含む） |
| `ssh` | ラズパイにSSH接続 |
| `logs` | リアルタイムログ表示 |
| `status` | サービス状態確認 |
| `restart` | サービス再起動 |
| `release-install [version]` | GitHub Releasesからインストール |

## CI/CD

### CI（自動）

GitHub Actions で PR / main push 時に自動実行:
- golangci-lint（errcheck, govet, staticcheck, gofmt 等）
- `go test -race` + カバレッジ計測
- ホストビルド + ARM64 クロスコンパイル

### CD（自動更新）

タグ push → GitHub Actions が ARM64 バイナリをビルド → Release 作成。
Pi は次回起動時（エンジンON）に GitHub Releases を自動チェックし、新バージョンがあればバイナリをアトミックに差し替えて再起動する（go-selfupdate）。

```bash
# タグを打つだけで Pi に自動配信される
git tag v0.4.0 && git push --tags
```

Web UI はバイナリに埋め込み済み（`go:embed`）のため、バイナリ1つで完結する。

手動インストールも可能:
```bash
# Pi 上で実行
./scripts/deploy.sh release-install v0.4.0
```
