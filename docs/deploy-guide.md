# DYデミオ 燃費メーター — デプロイガイド

## 全体の流れ

```
Mac (開発) --rsync--> Raspberry Pi (車載) --WiFi--> Google Sheets (記録)
```

開発は2つのフェーズに分かれる。

**フェーズ1: 開発中（overlayFS OFF）**
- `./scripts/deploy.sh deploy` で自由にコードを転送・再起動できる
- SDカードに普通に書き込める状態
- このフェーズでは電源ブチ切りに注意（でも開発中なので許容）

**フェーズ2: 安定運用（overlayFS ON）**
- SDカードへの書き込みがゼロになる
- エンジンOFF = 電源ブチ切りでもSDが壊れない
- コード更新時だけ一時的にOFFに戻す

**最初はフェーズ1のことだけ考えればいい。** overlayFSは全部動いてから有効にする。

---

## 接続設定

ラズパイへのSSH接続先は `scripts/deploy.sh` 内の `PI` 変数で一元管理する。
環境変数 `PI_HOST` で上書きも可能。

```bash
# デフォルト接続先の確認
head -10 scripts/deploy.sh | grep PI=
```

以下のドキュメントでは `$PI` をラズパイのSSH先として記載する。

---

## 前提

- Mac に Go がインストール済み
- ラズパイに Raspberry Pi OS Lite (64bit) を焼いた SD が入っている
- ラズパイとMacが同じWiFiに接続されている
- SSH有効化済み（`./scripts/deploy.sh ssh` で入れる状態）

---

## 1. ラズパイの初期セットアップ

### 1-1. SD カードにOSを焼く

Raspberry Pi Imager で以下を選択：
- OS: Raspberry Pi OS Lite (64-bit)
- カスタマイズ:
  - ホスト名: 任意（`deploy.sh` の `PI` 変数と合わせる）
  - SSH有効化（パスワード認証）
  - WiFi設定（自宅のSSID/パスワード）
  - ユーザー: 任意（`deploy.sh` の `PI` 変数と合わせる）

### 1-2. SSH鍵を登録（パスワード入力を省略するため）

```bash
# Macで実行（$PI は deploy.sh の接続先に読み替え）
ssh-copy-id $(head -10 scripts/deploy.sh | grep -oP '(?<=PI="\$\{PI_HOST:-).+(?=\})')
```

または手動で:
```bash
ssh-copy-id user@hostname.local
```

以後 `./scripts/deploy.sh ssh` がパスワードなしで通る。

### 1-3. ラズパイ側の基本設定

```bash
# ラズパイにSSHで入って実行
./scripts/deploy.sh ssh

# Bluetooth有効化の確認
sudo systemctl status bluetooth

# 必要パッケージのインストール
sudo apt update
sudo apt install -y bluez bluez-tools chromium-browser xserver-xorg xinit

# 自動起動用ディレクトリ作成
sudo mkdir -p /opt/pi-obd-meter/web/static /opt/pi-obd-meter/configs
sudo chown -R $(whoami):$(whoami) /opt/pi-obd-meter
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
./scripts/deploy.sh setup
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
./scripts/deploy.sh deploy
```

やっていること：
1. `GOOS=linux GOARCH=arm64 go build` でクロスコンパイル
2. `rsync` で差分のみ転送（2回目以降は変更分だけなので速い）
3. `systemctl restart` でサービス再起動

### Web UI（HTML/CSS/JS）だけ変更したら

```bash
./scripts/deploy.sh deploy-web
```

Goの再ビルドをスキップして、HTMLだけ転送。

### なぜ rsync なのか

- **rsync**: 差分転送。変更があったファイルだけ送る。2回目以降が速い
- **scp**: 毎回全ファイル転送。OpenSSH 9.0で非推奨になった

---

## 4. 便利コマンド

```bash
./scripts/deploy.sh ssh        # ラズパイにSSHで入る
./scripts/deploy.sh logs       # リアルタイムログ表示
./scripts/deploy.sh status     # サービス状態確認
./scripts/deploy.sh restart    # サービス再起動（ファイル転送なし）
```

---

## 5. overlayFS（SD保護）— フェーズ2: 安定運用

### いつ有効にするか

**全部動作確認が終わって「もう触らない」状態になったら。**

開発中は絶対にOFFにしておく。理由はシンプルで、overlayFSがONだと `deploy` で書き込んだファイルが再起動で消える。

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
./scripts/deploy.sh overlay-on
# ラズパイを再起動（SSH先は deploy.sh の設定値を使用）
./scripts/deploy.sh ssh  # 入ったら: sudo reboot

# overlayFSを無効にする（デプロイモード）
./scripts/deploy.sh overlay-off
./scripts/deploy.sh ssh  # 入ったら: sudo reboot
```

※ どちらも再起動が必要。`raspi-config` が再起動時に適用する設定を予約する仕組みのため。

### 安定運用中にコードを更新したくなったら

```bash
# 1. overlayFS解除
./scripts/deploy.sh overlay-off
./scripts/deploy.sh ssh  # 入ったら: sudo reboot

# 2. 再起動を待つ（30秒くらい）
sleep 30

# 3. デプロイ
./scripts/deploy.sh deploy

# 4. 動作確認
./scripts/deploy.sh logs

# 5. 問題なければoverlayFS再有効化
./scripts/deploy.sh overlay-on
./scripts/deploy.sh ssh  # 入ったら: sudo reboot
```

2回の再起動が必要になるが、安定運用に入ったら更新頻度は低いので問題ない。

### 運用フェーズまとめ

| フェーズ | overlayFS | デプロイ | SD保護 |
|---------|-----------|---------|--------|
| 開発中 | OFF | `./scripts/deploy.sh deploy` だけ | なし（注意して使う） |
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
http://<raspi-ip>:9090/control.html
```

### Step 3: 車でエンジンONテスト

OBD-IIポートにELM327を挿して、エンジンをかけて、ラズパイ起動。
```bash
./scripts/deploy.sh logs  # 別ターミナルでリアルタイム監視
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
├── scripts/
│   └── deploy.sh              # 開発・デプロイスクリプト
├── docs/
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
# deploy.sh 経由で接続確認
./scripts/deploy.sh ssh
# 上記が失敗する場合、PI_HOST を確認
head -10 scripts/deploy.sh | grep PI=
```

### ELM327にBT接続できない
```bash
bluetoothctl
  devices              # ペアリング済みデバイス一覧
  info XX:XX:XX:XX     # 接続状態確認
```

### overlayFSの状態確認
```bash
./scripts/deploy.sh ssh
# ラズパイ上で:
mount | grep overlay
# 出力があればoverlayFS有効、なければ無効
```

### サービスが起動しない
```bash
./scripts/deploy.sh logs     # エラーログ確認
./scripts/deploy.sh status   # サービス状態確認
```
