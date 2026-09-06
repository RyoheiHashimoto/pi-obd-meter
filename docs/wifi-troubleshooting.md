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

画面のどこでも **3秒長押し** するとChromiumが終了する。
キオスクが邪魔で設定できないときに使う。

## 4. よくある問題

| 症状 | 原因 | 対策 |
|------|------|------|
| 接続後にしばらくして切れる | WiFi省電力が有効 | `wifi.powersave = 2` を設定 |
| 起動直後の接続だけ失敗し、数回目で繋がる | **未解決**。省電力ではない (繋がった後は安定) | [起動直後の association 失敗](#5-起動直後の-association-失敗-未解決) を読む |
| パスワード入力ダイアログが出る | `psk-flags=0` がない | nmconnectionに `psk-flags=0` を追加 |
| キオスクが先に起動してWiFi設定できない | kiosk.serviceが先に起動 | 画面3秒長押しでキオスク終了、またはWiFiガードで自動スキップ |
| SSHもキーボードもない | 物理アクセス不可 | SDカードを抜いてMacからdebugfsで書き込み |
| nmconnectionを書いたのに無視される | パーミッションが644 | `chmod 600` または debugfsで `mode 0100600` |

## 5. 起動直後の association 失敗 (未解決)

```
wpa_supplicant: CTRL-EVENT-ASSOC-REJECT bssid=00:00:00:00:00:00 status_code=16
```

起動直後に数回 (1回あたり約0.4秒) 失敗し、3回目あたりで成功する。
**繋がった後は安定**(2時間で切断0回)。電波は -26 dBm / 品質 93 で問題なく、
同じAPに他の機器は正常に繋がる。カーネル (`brcmfmac`) は何も記録しない。
BSSIDが全ゼロなのは、APの拒否ではなくドライバ側の失敗報告であることを示す。

既知の未解決バグ (RPi-Distro/firmware-nonfree issue #34) と症状が一致する。
`roamoff=1` は Raspberry Pi OS の既定で有効。追加しても意味がない。

### この症状で絶対にやらないこと

1. **接続のやり直しを短時間に繰り返して回数を数える実験をしない。**
   2026-09-07 未明に2回実施し、2回ともPiをネットワークから失った。復帰まで
   十数分〜数時間かかり、走行中の車で作業していたユーザーに再始動を繰り返させた。
   得られた情報はゼロ。**回数を数えたいなら、永続 journal に溜まるのを待つ。**
2. **wifi-watchdog に「無線オフ/オン」を足し戻さない。**
   2026-09-05 に8回リセットして明確な復旧はゼロ、6回は3〜35秒で再発した。
   成功した1回は1秒後に走った pmf 自動巻き戻しで説明がつく。
   効果が無いうえ、NetworkManager が接続中に無線を切るので復旧を妨げる。
   判断の根拠は `scripts/ops/wifi-watchdog.sh` の28-33行に書いてある。
3. **ネットワーク設定を試すときは自動巻き戻しを併用する。**
   ```bash
   sudo systemd-run --on-active=180 --unit=wifi-revert /path/revert.sh
   ```
   「失敗したらN秒後に自動で元に戻す」。遠隔で試すために用意した仕組み。

### 調べ方 (実機を壊さない順)

```bash
journalctl --list-boots                      # 過去の起動が残っているか
journalctl -b -1 | grep -c ASSOC-REJECT      # 起動ごとの失敗回数を数える
cat /sys/module/brcmfmac/parameters/roamoff  # 既定で 1
lsmod | grep brcmfmac                        # brcmfmac + brcmfmac_cyw の2モジュール構成
```

`feature_disable` は sysfs に出ない宣言 (perm 0) のため、
**設定が効いているかどうかを読み出しで確認できない。** 効いている前提で話を進めないこと。

### 起動時に大量に出るときは USB を疑う

2026-09-07 の起動では ASSOC-REJECT が16回出たが、同じ起動で AIC8800 ドングルが
3回再列挙し、その 0.5 秒後に同じハブ上の SSD が落ちている。
無線だけを見ずに `dmesg` を**全部**読むこと。

詳細と経緯: [handover-2026-09-07.md](handover-2026-09-07.md)
