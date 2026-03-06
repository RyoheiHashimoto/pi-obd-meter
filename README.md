# pi-obd-meter

**OBD-2 車載メーター + 自動燃費記録**

Raspberry Pi + ELM327 で速度・RPM・負荷・スロットルを5インチLCDにリアルタイム表示。
給油時の燃費はタンク残量の変化から自動算出し、Google Sheetsに記録する。
スマホからはGAS Webアプリで燃費履歴・メンテナンス状態を確認できる。

## 対応車種

**OBD-2ポートがあり、ELM327で通信できる車ならほぼ全車対応。** 2000年代半ば以降の国産車であれば大抵動く（日本車のOBD-2義務化は2010年だが、それ以前から搭載している車も多い）。

### 必要なPID

| PID | 用途 | 必須 |
|-----|------|------|
| 0x0C | RPM | メーター表示 |
| 0x0D | 車速 | メーター表示 + 距離積算 |
| 0x04 | エンジン負荷 | メーター表示 |
| 0x11 | スロットル開度 | メーター表示 |
| 0x2F | 燃料タンクレベル | 給油検出 + 燃費算出 |

`pi-obd-scanner` で事前に対応PIDを確認できる:

```bash
./pi-obd-scanner -port /dev/rfcomm0
```

### 対応できない車

- EV / HV の電動走行モード（エンジン回転なし）
- 1996年以前の旧車（OBD-2未搭載）
- 燃料タンクレベル PID (0x2F) が返せない車（給油検出が動かない）

### 動作確認済み車種

| 車種 | 型式 | エンジン | OBDプロトコル | 検出PID数 |
|------|------|----------|---------------|-----------|
| マツダ DYデミオ | DBA-DY3W | ZJ-VE 1.3L | CAN 11bit 500kbaud (ATSP6) | 28 |

## データフロー

```
ECU → ELM327 (BT) → Raspberry Pi → meter.html (車載LCD: 速度/RPM/負荷/スロットル)
                                   → GAS Webhook → Google Sheets (トリップ/給油/メンテ記録)
                                                 → doGet Webアプリ (スマホ: 燃費履歴/メンテ状態)
```

## 給油自動検出

1. エンジン始動時にタンク残量 (PID 0x2F) を3回読み取り平均
2. 前回保存値との差分 >= 5% → 給油と判定
3. `燃費 = 走行距離 / ((開始時タンク% - 直近タンク%) / 100 * タンク容量L)`
4. Google Sheetsに自動記録

手動計算は不要。給油するだけで燃費が記録される。

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
  "fuel_tank_capacity_l": 44,
  "redline_rpm": 6500,
  "max_speed_kmh": 180,
  "max_rpm": 8000,
  "refuel_min_increase_pct": 5.0,
  "maintenance_reminders": [
    { "id": "oil_change", "name": "エンジンオイル交換", "type": "distance", "interval_km": 3000, "warning_pct": 0.8 }
  ]
}
```

| パラメータ | 説明 |
|-----------|------|
| `fuel_tank_capacity_l` | 燃料タンク容量 (L) |
| `redline_rpm` | レッドゾーン開始回転数 |
| `refuel_min_increase_pct` | 給油判定の最小タンク増加率 (%) |
| `maintenance_reminders` | メンテナンスリマインダー項目 |

## プロジェクト構成

```
pi-obd-meter/
├── cmd/
│   ├── pi-obd-meter/        # メインアプリ（車載）
│   └── pi-obd-scanner/      # PIDスキャナー（診断用）
├── internal/
│   ├── obd/               # ELM327通信、PID定義、DTC
│   ├── trip/              # トリップ追跡 + 燃料状態永続化
│   ├── sender/            # Google Sheets送信（GAS webhook）
│   ├── display/           # 画面輝度制御
│   └── maintenance/       # メンテナンスリマインダー
├── web/static/
│   └── meter.html         # メーター画面（5インチLCD, 60fps）
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
| `deploy-web` | Web UIのみ転送 |
| `setup` | 初回セットアップ |
| `ssh` | ラズパイにSSH接続 |
| `logs` | リアルタイムログ表示 |
| `status` | サービス状態確認 |
| `restart` | サービス再起動 |
| `overlay-on` | overlayFS有効化（SD保護） |
| `overlay-off` | overlayFS無効化 |
| `release-install [version]` | GitHub Releasesからインストール |

## CI/CD

### CI（自動）

GitHub Actions で PR / main push 時に自動実行:
- golangci-lint（errcheck, govet, staticcheck, gofmt 等）
- `go test -race` + カバレッジ計測
- ホストビルド + ARM64 クロスコンパイル

### CD（手動）

Pi へのデプロイは意図的に手動。車載組み込みシステムのため、壊れたバイナリが自動デプロイされると走行中に復旧できない。

```bash
# タグを打つ → GitHub Actions が ARM64 バイナリを自動ビルド → Release 作成
git tag v0.4.0 && git push --tags

# Pi 側でリリースをインストール
./scripts/deploy.sh release-install v0.4.0
```
