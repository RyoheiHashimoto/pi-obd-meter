# セットアップガイド

## 全体の流れ

```
Mac (開発) --rsync--> Raspberry Pi (車載) --WiFi--> Google Sheets (記録)
                                                 --> GAS Webアプリ (スマホ閲覧)

git tag → GitHub Actions → Release → Pi が2分以内に自動更新
```

### デプロイ方法

| 方法 | コマンド | 反映時間 |
|---|---|---|
| 開発デプロイ (rsync) | `make deploy` | 即時 |
| develop push | git push → CI → dev-latest | 2分以内 |
| リリース | `make release` → タグ push → Release | 2分以内 |

Web UI はバイナリに埋め込み済み（`go:embed`）のため、バイナリ1つで完結。

---

## 前提

- Mac に Go がインストール済み
- ラズパイに Raspberry Pi OS Lite (64bit) を焼いた SD が入っている
- ラズパイとMacが同じWiFiに接続されている
- SSH有効化済み

---

## 1. ラズパイの初期セットアップ

### 1-1. SD カードにOSを焼く

Raspberry Pi Imager で以下を選択:
- OS: **Raspberry Pi OS Lite (64-bit)**
- カスタマイズ:
  - ホスト名: 任意（`scripts/deploy.sh` の接続先と合わせる）
  - SSH有効化（パスワード認証）
  - WiFi設定（自宅のSSID/パスワード）
  - ユーザー名: 任意

### 1-2. SSH鍵を登録

```bash
ssh-copy-id user@hostname.local
```

以後 `./scripts/deploy.sh ssh` がパスワードなしで通る。

接続先は `scripts/deploy.sh` の `PI` 変数で一元管理。環境変数 `PI_HOST` で上書き可能。

```bash
# デフォルト接続先の確認
head -10 scripts/deploy.sh | grep PI=
```

### 1-3. ラズパイ側の基本設定

```bash
./scripts/deploy.sh ssh

# Bluetooth有効化の確認
sudo systemctl status bluetooth

# 必要パッケージのインストール
sudo apt update
sudo apt install -y bluez bluez-tools chromium-browser xserver-xorg xinit unclutter
```

---

## 2. ELM327 Bluetooth ペアリング

> **注意**: Pi 4 は WiFi と Bluetooth が同じチップを共有している。Bluetooth 操作中に WiFi が不安定になることがある。

### Step 1: Bluetooth アダプタ準備

```bash
sudo rfkill unblock bluetooth
sudo systemctl restart bluetooth
sudo hciconfig hci0 class 0x200000
sudo hciconfig hci0 piscan
```

### Step 2: ELM327 スキャン & ペアリング

ELM327 の電源スイッチを ON にし、車のキーを ACC 以上にしてから実行:

```bash
hcitool scan
# → 00:1D:A5:XX:XX:XX  OBDII のように表示される

bluetoothctl
  pair XX:XX:XX:XX:XX:XX
  # PINを聞かれたら 1234 を入力
  trust XX:XX:XX:XX:XX:XX
  quit
```

### Step 3: rfcomm バインド

```bash
sudo rfcomm bind 0 XX:XX:XX:XX:XX:XX
ls -la /dev/rfcomm0
```

### 起動時の自動バインド

`/etc/rc.local` の `exit 0` の前に追記:
```bash
hciconfig hci0 class 0x200000
hciconfig hci0 piscan
rfcomm bind 0 XX:XX:XX:XX:XX:XX
```

---

## 3. ディスプレイ設定

ELECROW 5インチ IPS (800x480) 用に `/boot/firmware/config.txt` へ追記:

```
hdmi_force_hotplug=1
max_usb_current=1
hdmi_drive=1
hdmi_group=2
hdmi_mode=87
hdmi_cvt 800 480 60 6 0 0 0
```

| 設定 | 説明 |
|---|---|
| `max_usb_current=1` | USB出力電流上限を引き上げ（モニターへのUSB給電に必要） |
| `hdmi_force_hotplug=1` | HDMI接続検出を強制（モニター先起動でも映る） |
| `hdmi_cvt 800 480 60 6 0 0 0` | 800x480 60Hz カスタムモード |

---

## 4. 初回デプロイ

```bash
./scripts/deploy.sh setup
```

これで以下が行われる:
1. ラズパイ上にディレクトリ作成 (`/opt/pi-obd-meter/`, `/var/lib/pi-obd-meter/`)
2. swap無効化（SD書き込み削減）
3. systemdサービスの登録・有効化:
   - `pi-obd-meter.service` — メインアプリ
   - `kiosk.service` — Chromium キオスクモード
   - `auto-update.timer` — 自動更新（2分間隔）
4. ARM64 クロスコンパイル + rsync転送

---

## 5. Google Sheets セットアップ

### 5-1. スプレッドシート作成

Google Sheets で新しいスプレッドシートを作成。

### 5-2. Apps Script 設定

1. 拡張機能 → Apps Script
2. `gas/webhook.gs` の内容をまるごと貼り付け
3. `setup()` 関数を1回実行（シート初期化: トリップ / 給油記録 / メンテ状態）
4. デプロイ → 新しいデプロイ → ウェブアプリ
   - 実行するユーザー: 自分
   - アクセスできるユーザー: 全員
5. 表示されたURLをコピー

### 5-3. config.json に設定

```json
{
  "webhook_url": "https://script.google.com/macros/s/XXXXXX/exec"
}
```

### 5-4. スマホでダッシュボード確認

デプロイしたWebアプリURL（doGetのURL）をスマホのブラウザで開く。
ホーム画面に追加すると、ネイティブアプリのように使える。

表示内容:
- 通算燃費
- 直近10件の給油履歴（日付、距離、燃費、給油量）
- メンテナンス進捗バー（緑/橙/赤）

---

## 6. キオスクモード

Chromium をフルスクリーンで自動起動し、5インチ LCD にメーター画面を表示する。

### 仕組み

- `pi-obd-meter.service` 起動後に `kiosk.service` が起動
- `kiosk.sh` が WiFi 接続を30秒待ち、API の起動を待ってから Chromium を起動
- スクリーンセーバー無効化、マウスカーソル非表示
- Chromium の翻訳バー・初回セットアップ・拡張機能を無効化
- ディスクキャッシュを `/dev/null` に設定（SD書き込み回避）

### キオスク終了

**画面のどこでも3秒長押し** するとキオスクが終了する。
WiFi設定やコンソール操作が必要な時に使用。

### WiFi ガード

WiFi 未接続時はキオスクを起動しない。
これにより、SSH もキーボードも使えないときにコンソールで WiFi 設定が可能。

---

## 7. SD カード保護

エンジンOFF = 電源断からSDカードを守るための対策。

### 自動（setup 時に適用済み）
- **swap 無効化**: `dphys-swapfile` を停止・無効化
- **ログ**: journald（RAM上、SDに書き込まない）

### アプリ側の対策
- **アトミック書き込み**: maintenance.json / trip_state.json は tmp + rename + fsync で保存。電源断でファイルが壊れない
- **GAS復元**: 起動時に GAS から累計走行距離を取得。万一ローカル状態が失われても復旧可能
- **メモリ内キュー**: 送信失敗データはメモリ上で保持（指数バックオフ 5分→30分で再送、最大100件）
- **距離ベース保存**: トリップ状態は0.1km（100m）走行ごとに保存。時間ベースより電源断時の距離ロスが少ない

---

## 8. 動作確認の順番

### Step 1: ELM327接続テスト（車不要）

```bash
/opt/pi-obd-meter/pi-obd-scanner -port /dev/rfcomm0
```

ELM327の電源をONにして実行。ECUに繋がっていなくても `ELM327 v1.5` の応答が返ればBT接続は成功。

### Step 2: 車でエンジンONテスト

OBD-IIポートにELM327を挿して、エンジンをかけて、ラズパイ起動。
```bash
make logs  # 別ターミナルでリアルタイム監視
```

RPM、速度のデータが流れてくればOK。

### Step 3: メーター表示確認

5インチLCDに速度アークゲージ、スロットルアーク、右パネルにインジケーターが表示される。

### Step 4: Google Sheets連携テスト

1. 短い距離を走ってエンジン停止 → 「トリップ」シートにデータが入るか確認
2. エンジン始動 → 「メンテ状態」シートが更新されるか確認
3. GAS WebアプリURLをスマホで開く → ダッシュボードが表示されるか確認
4. 給油記録を入力 → 燃費が算出されるか確認

---

## ディレクトリ構成（デプロイ後）

```
Mac (開発機)
pi-obd-meter/
├── cmd/pi-obd-meter/      # メインアプリ
├── cmd/pi-obd-scanner/    # 診断ツール
├── internal/              # Go パッケージ群
├── web/static/            # メーター UI (go:embed でバイナリに埋め込み)
├── gas/                   # Google Apps Script
├── configs/               # 設定ファイル + systemd ユニット
├── scripts/               # デプロイ + 自動更新スクリプト
└── docs/                  # ドキュメント

Raspberry Pi (車載)
/opt/pi-obd-meter/
├── pi-obd-meter           # バイナリ（Web UI埋め込み済み）
├── pi-obd-scanner         # バイナリ
├── configs/
│   ├── config.json        # 車両設定
│   └── kiosk.sh           # キオスク起動スクリプト
├── scripts/
│   └── auto-update.sh     # 自動更新スクリプト
└── web/static/            # 開発用: web_static_dir 指定時のみ使用

/var/lib/pi-obd-meter/
├── maintenance.json       # メンテナンス状態（アトミック書き込み）
├── trip_state.json        # トリップ状態（0.1kmごと保存）
├── release-version        # インストール済み stable バージョン
└── dev-version            # インストール済み dev ビルドの published_at
```

---

## トラブルシューティング

### rsyncが繋がらない
```bash
./scripts/deploy.sh ssh          # SSH自体の接続確認
head -10 scripts/deploy.sh | grep PI=  # 接続先の確認
```

### ELM327にBT接続できない
```bash
bluetoothctl
  devices              # ペアリング済みデバイスの一覧
  info XX:XX:XX:XX     # デバイスの詳細
```

### OBD読み取りエラーが連続する
連続10回エラーで自動再接続を試みる。ログに「再接続を試みます」と表示される。
Bluetooth接続が不安定な場合は `rfcomm release 0 && rfcomm bind 0 XX:XX:XX:XX:XX:XX` で再バインド。

### サービスが起動しない
```bash
make logs              # メインアプリのログ
make status            # サービス状態
./scripts/deploy.sh kiosk-logs  # キオスクのログ
```

### WiFi問題
詳細は [wifi-troubleshooting.md](wifi-troubleshooting.md) を参照。
