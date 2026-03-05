# DYデミオ 燃費メーター — デプロイガイド

## 全体の流れ

```
Mac (開発) --rsync--> Raspberry Pi (車載) --WiFi--> Google Sheets (記録)
```

開発は2つのフェーズに分かれる。

**フェーズ1: 開発中（overlayFS OFF）**
- `make deploy` で自由にコードを転送・再起動できる
- SDカードに普通に書き込める状態
- このフェーズでは電源ブチ切りに注意（でも開発中なので許容）

**フェーズ2: 安定運用（overlayFS ON）**
- SDカードへの書き込みがゼロになる
- エンジンOFF = 電源ブチ切りでもSDが壊れない
- コード更新時だけ一時的にOFFに戻す

**最初はフェーズ1のことだけ考えればいい。** overlayFSは全部動いてから有効にする。

---

## 前提

- Mac に Go がインストール済み
- ラズパイに Raspberry Pi OS Lite (64bit) を焼いた SD が入っている
- ラズパイとMacが同じWiFiに接続されている
- SSH有効化済み（`ssh pi@raspberrypi.local` で入れる状態）

---

## 1. ラズパイの初期セットアップ

### 1-1. SD カードにOSを焼く

Raspberry Pi Imager で以下を選択：
- OS: Raspberry Pi OS Lite (64-bit)
- カスタマイズ:
  - ホスト名: `raspberrypi`
  - SSH有効化（パスワード認証）
  - WiFi設定（自宅のSSID/パスワード）
  - ユーザー: `pi` / 任意のパスワード

### 1-2. SSH鍵を登録（パスワード入力を省略するため）

```bash
# Macで実行
ssh-copy-id pi@raspberrypi.local
```

以後 `ssh pi@raspberrypi.local` がパスワードなしで通る。

### 1-3. ラズパイ側の基本設定

```bash
# ラズパイにSSHで入って実行
ssh pi@raspberrypi.local

# Bluetooth有効化の確認
sudo systemctl status bluetooth

# 必要パッケージのインストール
sudo apt update
sudo apt install -y bluez bluez-tools chromium-browser xserver-xorg xinit

# 自動起動用ディレクトリ作成
sudo mkdir -p /opt/pi-obd-meter/web/static /opt/pi-obd-meter/configs
sudo chown -R pi:pi /opt/pi-obd-meter
```

### 1-4. ELM327 Bluetooth ペアリング

```bash
# ラズパイで実行
bluetoothctl
  power on
  agent on
  scan on
  # ELM327のアドレスが表示されるのを待つ (例: XX:XX:XX:XX:XX:XX)
  pair XX:XX:XX:XX:XX:XX
  # PINを聞かれたら 1234 を入力
  trust XX:XX:XX:XX:XX:XX
  quit

# rfcommバインド（シリアルポート化）
sudo rfcomm bind 0 XX:XX:XX:XX:XX:XX
# → /dev/rfcomm0 が作成される

# 確認
ls -la /dev/rfcomm0
```

rfcommを起動時に自動バインドするには `/etc/rc.local` に追記：
```bash
rfcomm bind 0 XX:XX:XX:XX:XX:XX
```

---

## 2. 初回デプロイ

```bash
# Macのプロジェクトルートで実行
make setup-pi
```

これで以下が行われる：
1. ラズパイ上にディレクトリ作成
2. クロスコンパイル（arm64向け）
3. rsyncでバイナリ・Web UI・設定ファイルを転送
4. systemdサービスの登録＆有効化

---

## 3. 普段のデプロイ（フェーズ1: 開発中）

### コードを変更したら

```bash
make deploy
```

やっていること：
1. `GOOS=linux GOARCH=arm64 go build` でクロスコンパイル
2. `rsync` で差分のみ転送（2回目以降は変更分だけなので速い）
3. `systemctl restart` でサービス再起動

### Web UI（HTML/CSS/JS）だけ変更したら

```bash
make deploy-web
```

Goの再ビルドをスキップして、HTMLだけ転送。

### なぜ rsync なのか

- **rsync**: 差分転送。変更があったファイルだけ送る。2回目以降が速い
- **scp**: 毎回全ファイル転送。OpenSSH 9.0で非推奨になった

---

## 4. 便利コマンド

```bash
make ssh        # ラズパイにSSHで入る
make logs       # リアルタイムログ表示
make status     # サービス状態確認
make restart    # サービス再起動（ファイル転送なし）
```

---

## 5. overlayFS（SD保護）— フェーズ2: 安定運用

### いつ有効にするか

**全部動作確認が終わって「もう触らない」状態になったら。**

開発中は絶対にOFFにしておく。理由はシンプルで、overlayFSがONだと `make deploy` で書き込んだファイルが再起動で消える。

### 判断の目安

overlayFSをONにしていいタイミング：
- ELM327接続、メーター表示、Google Sheets送信、Discord通知、すべて正常動作を確認済み
- 1週間くらい普通に使って問題が出ていない
- 「しばらくコードは変更しない」と思える

### なぜ必要か

車のエンジンを切るとラズパイの電源が突然落ちる。書き込み中にブチッと切れるとSDカードが壊れる。overlayFSはファイルシステムをRAM上に置くことで、SDへの書き込みをゼロにする。

### コマンド

```bash
# overlayFSを有効にする（SD保護モード）
make overlay-on
ssh pi@raspberrypi.local 'sudo reboot'

# overlayFSを無効にする（デプロイモード）
make overlay-off
ssh pi@raspberrypi.local 'sudo reboot'
```

※ どちらも再起動が必要。`raspi-config` が再起動時に適用する設定を予約する仕組みのため。

### 安定運用中にコードを更新したくなったら

```bash
# 1. overlayFS解除
make overlay-off
ssh pi@raspberrypi.local 'sudo reboot'

# 2. 再起動を待つ（30秒くらい）
sleep 30

# 3. デプロイ
make deploy

# 4. 動作確認
make logs

# 5. 問題なければoverlayFS再有効化
make overlay-on
ssh pi@raspberrypi.local 'sudo reboot'
```

2回の再起動が必要になるが、安定運用に入ったら更新頻度は低いので問題ない。

### 運用フェーズまとめ

| フェーズ | overlayFS | デプロイ | SD保護 |
|---------|-----------|---------|--------|
| 開発中 | OFF | `make deploy` だけ | なし（注意して使う） |
| 安定運用 | ON | 上記の5ステップ | 電源ブチ切りOK |

---

## 6. Google Sheets セットアップ

### 6-1. スプレッドシート作成

1. Google Sheets で新しいスプレッドシートを作成
2. 名前を「DYデミオ 燃費メーター」にする

### 6-2. Apps Script 設定

1. 拡張機能 → Apps Script
2. `gas/webhook.gs` の内容をまるごと貼り付け
3. `DISCORD_WEBHOOK_URL` を設定（Discordで Webhook URLを発行）
4. `setup()` 関数を1回実行（シートの初期化）
5. デプロイ → 新しいデプロイ → ウェブアプリ
   - 実行するユーザー: 自分
   - アクセスできるユーザー: 全員
6. 表示されたURLをコピー

### 6-3. config.json に設定

```json
{
  "serial_port": "/dev/rfcomm0",
  "webhook_url": "https://script.google.com/macros/s/XXXXXX/exec",
  "discord_webhook": "https://discord.com/api/webhooks/XXXXXX",
  "poll_interval_ms": 500,
  "local_api_port": 9090
}
```

---

## 7. 動作確認の順番

### Step 1: ELM327接続テスト（車不要）

```bash
# ラズパイで実行
/opt/pi-obd-meter/pi-obd-scanner -port /dev/rfcomm0
```

ELM327の電源をONにして実行。ECUに繋がっていなくても `ELM327 v1.5` のバージョン応答が返ればBT接続は成功。

### Step 2: スマホからWeb UIアクセス

ラズパイのIPを確認して、スマホブラウザで:
```
http://192.168.x.x:9090/control.html
```

### Step 3: 車でエンジンONテスト

OBD-IIポートにELM327を挿して、エンジンをかけて、ラズパイ起動。
```bash
make logs  # 別ターミナルでリアルタイム監視
```

RPM、速度、水温のデータが流れてくればOK。

### Step 4: Google Sheets連携テスト

短い距離を走ってトリップを完了させ、Google Sheetsの「トリップ」シートにデータが入るか確認。

---

## ディレクトリ構成

```
Mac (開発機)
pi-obd-meter/
├── cmd/
│   ├── pi-obd-meter/main.go    # メインアプリ
│   └── pi-obd-scanner/main.go  # 診断ツール
├── internal/
│   ├── obd/                   # ELM327通信、PID、DTC
│   ├── trip/                  # トリップ追跡
│   ├── sender/                # Google Sheets送信
│   ├── notify/                # Discord通知
│   ├── display/               # 画面輝度制御
│   └── maintenance/           # メンテナンスリマインダー
├── web/static/
│   ├── meter.html             # メーター画面（5インチLCD）
│   └── control.html           # 操作画面（スマホ）
├── gas/
│   └── webhook.gs             # Google Apps Script
├── configs/
│   └── config.json
├── docs/
├── Makefile
└── go.mod

Raspberry Pi (車載)
/opt/pi-obd-meter/
├── pi-obd-meter                # バイナリ
├── pi-obd-scanner              # バイナリ
├── web/static/
│   ├── meter.html
│   └── control.html
└── configs/
    └── config.json
```

---

## トラブルシューティング

### rsyncが繋がらない
```bash
ping raspberrypi.local   # 名前解決の確認
ssh pi@raspberrypi.local # SSH自体が通るか確認
```

### ELM327にBT接続できない
```bash
bluetoothctl
  devices              # ペアリング済みデバイス一覧
  info XX:XX:XX:XX     # 接続状態確認
```

### overlayFSの状態確認
```bash
ssh pi@raspberrypi.local 'mount | grep overlay'
# 出力があればoverlayFS有効、なければ無効
```

### サービスが起動しない
```bash
make logs              # エラーログ確認
ssh pi@raspberrypi.local 'cat /opt/pi-obd-meter/configs/config.json'  # 設定確認
```
