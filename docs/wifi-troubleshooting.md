# Wi-Fi トラブルシューティング

Raspberry Pi 4 + NetworkManager 環境でのWiFi接続問題の診断・復旧手順。

## 前提

- OS: Raspberry Pi OS Lite 64-bit (Bookworm以降)
- WiFi管理: NetworkManager (`nmcli`)
- 接続プロファイル: `/etc/NetworkManager/system-connections/*.nmconnection`
- キオスクモード: WiFi未接続時は自動スキップ（`kiosk.sh` のWiFiガード）

## 1. SSHで接続できる場合

### 診断

```bash
# WiFi接続状態の確認
nmcli device status
nmcli connection show

# WiFiスキャン（周辺のSSID一覧）
sudo nmcli device wifi list

# 接続ログ
journalctl -u NetworkManager --no-pager -n 50

# WiFi省電力の状態確認
iw wlan0 get power_save
```

### WiFi接続プロファイルの追加

```bash
sudo nmcli connection add \
  type wifi \
  con-name "SSID名" \
  ssid "SSID名" \
  wifi-sec.key-mgmt wpa-psk \
  wifi-sec.psk "パスワード"
```

または直接ファイルを作成:

```bash
sudo tee /etc/NetworkManager/system-connections/SSID名.nmconnection > /dev/null << 'EOF'
[connection]
id=SSID名
type=wifi
autoconnect=true

[wifi]
ssid=SSID名
mode=infrastructure

[wifi-security]
key-mgmt=wpa-psk
psk=パスワード
psk-flags=0

[ipv4]
method=auto

[ipv6]
method=auto
EOF

sudo chmod 600 /etc/NetworkManager/system-connections/SSID名.nmconnection
sudo nmcli connection reload
```

### WiFi省電力の無効化（接続断の予防）

```bash
sudo tee /etc/NetworkManager/conf.d/wifi-powersave.conf > /dev/null << 'EOF'
[connection]
wifi.powersave = 2
EOF

sudo systemctl restart NetworkManager
```

`wifi.powersave` の値: `1`=デフォルト, `2`=無効, `3`=有効

## 2. SSHで接続できない場合（SDカード直接編集）

SSHもキーボードも使えないとき、MacからSDカードのext4パーティションに直接書き込む。

### 必要なツール

```bash
# macOSではext4を直接マウントできないため、debugfsを使う
brew install e2fsprogs
```

### 手順

#### Step 1: SDカードを特定

```bash
diskutil list external
# → /dev/diskN の番号を確認（Linuxパーティションは diskNs2）
```

#### Step 2: nmconnectionファイルを作成

```bash
cat > /tmp/MyWiFi.nmconnection << 'EOF'
[connection]
id=MyWiFi
type=wifi
autoconnect=true

[wifi]
ssid=MyWiFi
mode=infrastructure

[wifi-security]
key-mgmt=wpa-psk
psk=MyPassword
psk-flags=0

[ipv4]
method=auto

[ipv6]
method=auto
EOF
```

> **重要**: `psk-flags=0` が必須。これがないとNetworkManagerはキーリングからパスワードを読もうとして失敗する。

#### Step 3: debugfsで書き込み

```bash
# diskNs2 は Step 1 で確認した番号に置き換え
echo "write /tmp/MyWiFi.nmconnection /etc/NetworkManager/system-connections/MyWiFi.nmconnection
set_inode_field /etc/NetworkManager/system-connections/MyWiFi.nmconnection mode 0100600
set_inode_field /etc/NetworkManager/system-connections/MyWiFi.nmconnection uid 0
set_inode_field /etc/NetworkManager/system-connections/MyWiFi.nmconnection gid 0
quit" | sudo /opt/homebrew/opt/e2fsprogs/sbin/debugfs -w /dev/diskNs2
```

#### Step 4: イジェクト

```bash
diskutil eject /dev/diskN
```

SDカードをPiに戻して起動すれば接続される。

### debugfs の注意点

- **`rm` は使わない**: debugfsの `rm` はファイルを完全削除する。復旧困難。
- **パーミッション設定は必須**: `mode 0100600`, `uid 0`, `gid 0` を設定しないとNetworkManagerが無視する。
- **SDカードのディスク番号は毎回変わる**: 挿し直すたびに `diskutil list external` で確認すること。
- **複数行コマンドはスクリプトファイルにする**: ターミナルでの直接コピペは改行の扱いで失敗しやすい。

### スクリプト化の例

```bash
#!/bin/bash
set -e
DEV=/dev/disk4s2  # ← diskutil list で確認した番号
DEBUGFS=/opt/homebrew/opt/e2fsprogs/sbin/debugfs
FILE=/etc/NetworkManager/system-connections/MyWiFi.nmconnection
SRC=/tmp/MyWiFi.nmconnection

echo "write $SRC $FILE
set_inode_field $FILE mode 0100600
set_inode_field $FILE uid 0
set_inode_field $FILE gid 0
quit" | sudo $DEBUGFS -w $DEV

diskutil eject /dev/disk4
echo "=== DONE ==="
```

## 3. 安全装置

### キオスクのWiFiガード

`configs/kiosk.sh` にWiFi接続チェックがあり、WiFi未接続時はキオスクを起動しない:

- 30秒間WiFi接続を待つ
- タイムアウトしたらキオスク起動をスキップ → コンソールでWiFi設定が可能

### タッチパネルからのキオスク終了

バージョン表示（画面左下）を **3秒長押し** するとChromiumが終了する。
キオスクが邪魔で設定できないときに使う。

## 4. よくある問題

| 症状 | 原因 | 対策 |
|------|------|------|
| 何回か再起動すると繋がる | WiFi省電力が有効 | `wifi.powersave = 2` を設定 |
| パスワード入力ダイアログが出る | `psk-flags=0` がない | nmconnectionに `psk-flags=0` を追加 |
| キオスクが先に起動してWiFi設定できない | kiosk.serviceが先に起動 | バージョン長押しでキオスク終了、またはWiFiガードで自動スキップ |
| SSHもキーボードもない | 物理アクセス不可 | SDカードを抜いてMacからdebugfsで書き込み |
| nmconnectionを書いたのに無視される | パーミッションが644 | `chmod 600` または debugfsで `mode 0100600` |
