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

# 必要パッケージのインストール (Wayland キオスク + CAN ツール)
sudo apt update
sudo apt install -y \
  greetd labwc cog swaybg wlr-randr \
  can-utils \
  unclutter
```

X11 / Chromium / lightdm / xserver-xorg は不要。bluez も CAN 直結構成では不要 (ELM327 フォールバック時のみ必要)。

---

## 2A. CAN HAT 直結 (推奨)

### Step 1: ハードウェア接続

- CAN HAT (例: Waveshare RS485 CAN HAT、MCP2515) を Pi GPIO に装着
- CAN High/Low を OBD-II コネクタの 6 番/14 番に接続 (車種により異なる、サービスマニュアル要参照)

### Step 2: `/boot/firmware/config.txt` に追記

```
dtparam=spi=on
dtoverlay=mcp2515-can0,oscillator=12000000,interrupt=25
```

⚠ クリスタル周波数は HAT の実装差に注意。**12MHz が一般的** だが 8MHz / 16MHz もある。`oscillator=` を実装に合わせる。

### Step 3: `can0` 起動設定

`/etc/network/interfaces.d/can0`:
```
auto can0
iface can0 inet manual
  pre-up /sbin/ip link set $IFACE type can bitrate 500000 restart-ms 100
  up /sbin/ifconfig $IFACE up
  down /sbin/ifconfig $IFACE down
```

再起動後:
```bash
ip -details link show can0
candump can0  # フレーム受信確認 (エンジン始動時)
```

### Step 4: `configs/config.json` 設定

```json
{
  "can_interface": "can0",
  "serial_port": "",
  ...
}
```

---

## 2B. ELM327 Bluetooth (フォールバック)

CAN HAT が用意できない場合の簡易構成。対応 PID 少なめ、レイテンシ大。

> **注意**: Pi 4 は WiFi と Bluetooth が同じチップを共有している。Bluetooth 操作中に WiFi が不安定になることがある。

```bash
sudo apt install -y bluez bluez-tools
sudo rfkill unblock bluetooth
sudo systemctl restart bluetooth
sudo hciconfig hci0 class 0x200000
sudo hciconfig hci0 piscan

hcitool scan
# → 00:1D:A5:XX:XX:XX  OBDII のように表示される

bluetoothctl
  pair XX:XX:XX:XX:XX:XX
  # PINを聞かれたら 1234 を入力
  trust XX:XX:XX:XX:XX:XX
  quit

sudo rfcomm bind 0 XX:XX:XX:XX:XX:XX
```

起動時の自動バインド: `/etc/rc.local` の `exit 0` の前に:
```bash
hciconfig hci0 class 0x200000
hciconfig hci0 piscan
rfcomm bind 0 XX:XX:XX:XX:XX:XX
```

`configs/config.json`:
```json
{
  "can_interface": "",
  "serial_port": "/dev/rfcomm0",
  ...
}
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

## 6. キオスクモード (Wayland + WPE WebKit)

cog (WPE WebKit ランチャ) をフルスクリーンで自動起動し、5インチ LCD にメーター画面を表示する。

### 仕組み

```
systemd ──> greetd (autologin laurel)
              └─> labwc (Wayland compositor)
                   └─> ~/.config/labwc/autostart
                        ├─> swaybg (黒背景)
                        └─> /opt/pi-obd-meter/configs/cog-kiosk.sh
                              └─> cog --ozone-platform=wayland --kiosk http://localhost:9090/meter.html
```

- **greetd** が laurel ユーザーで自動ログイン
- **labwc** が Wayland コンポジタとして起動、autostart から swaybg と cog を同時起動
- **cog** (WPE WebKit 0.18.4+) が `localhost:9090/meter.html` をフルスクリーン表示
- `cog-kiosk.sh` が pi-obd-meter API 起動を待ってから cog を exec
- `XCURSOR_THEME=blank` で空カーソルテーマ指定 (labwc rc.xml で `<cursorTheme name="blank">` も併用)

Chromium / X11 / lightdm は使わない。理由:
- Wayland 起動時の白フラッシュが Chromium Ozone で残るバグがある (crbug.com/40207942)
- WPE WebKit は wl_surface の map を first-content-commit まで遅延するため白フラッシュなし
- メモリフットプリント (~100MB) が Chromium (~400MB) より大幅軽量
- 起動時間も短い

### キオスク終了

**画面のどこでも3秒長押し** するとキオスクが終了する (`POST /api/kiosk/stop` → `pkill cog`)。
SSH もキーボードも使えないときのコンソール操作用。

### カーソル非表示

Wayland では `unclutter` (X11 専用) は効かない。代わりに:
- labwc `rc.xml` の `<theme><cursorTheme name="blank"/>` で透明カーソルテーマ指定
- `/usr/share/icons/blank/cursors/default` に 1x1 透明の xcursor ファイル配置
- `meter.css` の `* { cursor: none !important }` で念押し

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
