# DYデミオ 燃費メーター — デプロイガイド

## 全体の流れ

```
Mac (開発) --rsync--> Raspberry Pi (車載) --WiFi--> Google Sheets (記録)
                                                 --> GAS Webアプリ (スマホ閲覧)

git tag → GitHub Actions → Release → Pi 起動時に自動更新（go-selfupdate）
```

### デプロイ方法

- **開発中**: `./scripts/deploy.sh deploy` でバイナリ + 設定をrsync転送
- **リリース**: `git tag vX.Y.Z && git push --tags` → Pi が次回起動時に自動更新
- Web UI はバイナリに埋め込み済み（`go:embed`）のため、バイナリ1つで完結

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

### 1-2. SSH鍵を登録

```bash
ssh-copy-id user@hostname.local
```

以後 `./scripts/deploy.sh ssh` がパスワードなしで通る。

### 1-3. ラズパイ側の基本設定

```bash
./scripts/deploy.sh ssh

# Bluetooth有効化の確認
sudo systemctl status bluetooth

# 必要パッケージのインストール
sudo apt update
sudo apt install -y bluez bluez-tools chromium-browser xserver-xorg xinit
```

### 1-4. ELM327 Bluetooth ペアリング

> **注意**: Pi 4 は WiFi と Bluetooth が同じチップを共有している。Bluetooth 操作中に WiFi が不安定になることがある。

#### Step 1: Bluetooth アダプタ準備

```bash
sudo rfkill unblock bluetooth
sudo systemctl restart bluetooth
sudo hciconfig hci0 class 0x200000
sudo hciconfig hci0 piscan
```

#### Step 2: ELM327 スキャン & ペアリング

ELM327 の電源スイッチを ON にし、車のキーを ACC 以上にしてから実行：

```bash
hcitool scan
# → 00:1D:A5:XX:XX:XX  OBDII のように表示される

bluetoothctl
  pair XX:XX:XX:XX:XX:XX
  # PINを聞かれたら 1234 を入力
  trust XX:XX:XX:XX:XX:XX
  quit
```

#### Step 3: rfcomm バインド

```bash
sudo rfcomm bind 0 XX:XX:XX:XX:XX:XX
ls -la /dev/rfcomm0
```

#### 起動時の自動バインド

`/etc/rc.local` の `exit 0` の前に追記：
```bash
hciconfig hci0 class 0x200000
hciconfig hci0 piscan
rfcomm bind 0 XX:XX:XX:XX:XX:XX
```

---

## 2. 初回デプロイ

```bash
./scripts/deploy.sh setup
```

これで以下が行われる：
1. ラズパイ上にディレクトリ作成
2. swap無効化（SD書き込み削減）
3. systemdサービスの登録＆有効化
4. クロスコンパイル（arm64向け）
5. rsyncでバイナリ・設定ファイルを転送

---

## 3. 普段のデプロイ

### 開発中: コードを変更したら

```bash
./scripts/deploy.sh deploy
```

### リリース: タグを打つだけ

```bash
git tag v0.4.0 && git push --tags
# → GitHub Actions が ARM64 バイナリをビルド → Release 作成
# → Pi は次回エンジンON時に自動更新
```

Web UI はバイナリに埋め込まれているため、別途転送する必要はない。

---

## 4. 便利コマンド

```bash
./scripts/deploy.sh ssh        # ラズパイにSSHで入る
./scripts/deploy.sh logs       # リアルタイムログ表示
./scripts/deploy.sh status     # サービス状態確認
./scripts/deploy.sh restart    # サービス再起動（ファイル転送なし）
./scripts/deploy.sh shutdown   # 安全にシャットダウン
./scripts/deploy.sh reboot     # 再起動
```

---

## 5. SD カード保護

エンジンOFF = 電源断からSDカードを守るため、以下の対策を行っている。

### 自動（setup 時に適用済み）
- **swap 無効化**: `dphys-swapfile` を停止・無効化
- **ログ**: journald（RAM上、SDに書き込まない）

### アプリ側の対策
- **アトミック書き込み**: maintenance.json / trip_state.json は tmp + rename + fsync で保存。電源断でファイルが壊れない
- **GAS復元**: 起動時に GAS から累計走行距離を取得。万一ローカル状態が失われても復旧可能
- **メモリ内キュー**: 送信失敗データはメモリ上で保持（指数バックオフで再送）

---

## 6. Google Sheets セットアップ

### 6-1. スプレッドシート作成

1. Google Sheets で新しいスプレッドシートを作成

### 6-2. Apps Script 設定

1. 拡張機能 → Apps Script
2. `gas/webhook.gs` の内容をまるごと貼り付け
3. `setup()` 関数を1回実行（シート初期化: トリップ / 給油記録 / メンテ状態）
4. デプロイ → 新しいデプロイ → ウェブアプリ
   - 実行するユーザー: 自分
   - アクセスできるユーザー: 全員
5. 表示されたURLをコピー

### 6-3. config.json に設定

```json
{
  "serial_port": "/dev/rfcomm0",
  "webhook_url": "https://script.google.com/macros/s/XXXXXX/exec",
  "engine_displacement_l": 1.3,
  "max_speed_kmh": 180
}
```

### 6-4. スマホでダッシュボード確認

デプロイしたWebアプリURL（doGetのURL）をスマホのブラウザで開く。
ホーム画面に追加すると、ネイティブアプリのように使える。

表示内容:
- 通算燃費
- 直近10件の給油履歴（日付、距離、燃費、給油量）
- メンテナンス進捗バー（緑/橙/赤）

---

## 7. 動作確認の順番

### Step 1: ELM327接続テスト（車不要）

```bash
/opt/pi-obd-meter/pi-obd-scanner -port /dev/rfcomm0
```

ELM327の電源をONにして実行。ECUに繋がっていなくても `ELM327 v1.5` の応答が返ればBT接続は成功。

### Step 2: 車でエンジンONテスト

OBD-IIポートにELM327を挿して、エンジンをかけて、ラズパイ起動。
```bash
./scripts/deploy.sh logs  # 別ターミナルでリアルタイム監視
```

RPM、速度のデータが流れてくればOK。

### Step 3: メーター表示確認

5インチLCDに速度・RPMゲージ、中央にスロットル/負荷バーが表示される。

### Step 4: Google Sheets連携テスト

1. 短い距離を走ってトリップを完了 → 「トリップ」シートにデータが入るか確認
2. 給油してからエンジン始動 → 「給油記録」シートに燃費が記録されるか確認
3. エンジン始動 → 「メンテ状態」シートが更新されるか確認
4. GAS WebアプリURLをスマホで開く → ダッシュボードが表示されるか確認

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
│   ├── trip/                  # トリップ追跡 + 燃料状態永続化
│   ├── sender/                # Google Sheets送信
│   ├── display/               # 画面輝度制御
│   └── maintenance/           # メンテナンスリマインダー
├── web/
│   ├── embed.go               # go:embed でstatic/をバイナリに埋め込み
│   └── static/
│       ├── meter.html         # メーター画面（5インチLCD）
│       ├── meter.css
│       └── meter.js
├── gas/
│   └── webhook.gs             # Google Apps Script
├── configs/
│   └── config.json
├── scripts/
│   └── deploy.sh
└── docs/

Raspberry Pi (車載)
/opt/pi-obd-meter/
├── pi-obd-meter                # バイナリ（Web UI埋め込み済み）
├── pi-obd-scanner              # バイナリ
└── configs/
    └── config.json
```

---

## トラブルシューティング

### rsyncが繋がらない
```bash
./scripts/deploy.sh ssh
head -10 scripts/deploy.sh | grep PI=
```

### ELM327にBT接続できない
```bash
bluetoothctl
  devices
  info XX:XX:XX:XX
```

### OBD読み取りエラーが連続する
連続10回エラーで自動再接続を試みる。ログに「再接続を試みます」と表示される。
Bluetooth接続が不安定な場合は `rfcomm release 0 && rfcomm bind 0 XX:XX:XX:XX:XX:XX` で再バインド。

### サービスが起動しない
```bash
./scripts/deploy.sh logs
./scripts/deploy.sh status
```
