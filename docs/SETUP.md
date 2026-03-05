# pi-obd-meter 実機セットアップガイド

DYデミオ (ZJ-VE 1.3L) × Raspberry Pi × Elecrow 5" IPS LCD

---

## Phase 1: LCD表示確認（机上）

### 1-1. Elecrow LCD接続

```bash
# HDMIケーブルでPiとLCDを接続
# USB給電: PiのUSB3端子からLCDへ（0.6A程度なので問題なし）
```

### 1-2. 解像度設定

`/boot/firmware/config.txt` を編集:

```ini
# Elecrow 5" IPS 800x480
hdmi_group=2
hdmi_mode=87
hdmi_cvt=800 480 60 6 0 0 0
hdmi_drive=2

# 画面回転が必要な場合
# display_rotate=0  (0=通常, 1=90°, 2=180°, 3=270°)
```

再起動して800×480で表示されることを確認:

```bash
sudo reboot
# 起動後
fbset -s  # 解像度確認
```

### 1-3. ブラウザ表示テスト

```bash
# テスト用にChromiumでmeter.htmlを開く
chromium-browser --start-fullscreen http://localhost:8080/static/meter.html
```

---

## Phase 2: OBD-II接続テスト（机上 → 車内）

### 2-1. Bluetooth OBDアダプタのペアリング

```bash
# Bluetoothサービス確認
sudo systemctl status bluetooth

# スキャン & ペアリング
bluetoothctl
> power on
> scan on
# OBDアダプタの電源ON（車のOBDポートに挿してACCオン）
# "OBDII" や "ELM327" のようなデバイスが見つかるはず
> pair XX:XX:XX:XX:XX:XX
> trust XX:XX:XX:XX:XX:XX
> quit
```

### 2-2. シリアルポートバインド

```bash
# rfcommでシリアルポートを作成
sudo rfcomm bind /dev/rfcomm0 XX:XX:XX:XX:XX:XX 1

# 永続化: /etc/systemd/system/rfcomm.service を作成
```

`/etc/systemd/system/rfcomm.service`:

```ini
[Unit]
Description=RFCOMM OBD-II Bluetooth Serial
After=bluetooth.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/bin/rfcomm bind /dev/rfcomm0 XX:XX:XX:XX:XX:XX 1
ExecStop=/usr/bin/rfcomm release /dev/rfcomm0

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable rfcomm
sudo systemctl start rfcomm
```

### 2-3. OBD通信テスト

```bash
# screenやminicomでELM327と直接通信テスト
screen /dev/rfcomm0 38400

# ELM327コマンド
ATZ          # リセット
ATE0         # エコーOFF
ATSP0        # プロトコル自動検出
010C         # RPM取得
010D         # 車速取得
0105         # 冷却水温取得
```

正常なら `41 0C XX XX` のようなレスポンスが返る。

### 2-4. USB OBDの場合

```bash
# USB ELM327の場合はペアリング不要
ls /dev/ttyUSB*   # デバイス確認
screen /dev/ttyUSB0 38400
```

---

## Phase 3: pi-obd-meterアプリ起動

### 3-1. プロジェクトデプロイ

```bash
# リポジトリをクローン
cd ~
git clone https://github.com/YOUR_REPO/pi-obd-meter.git
cd pi-obd-meter

# Go依存関係インストール
go mod download

# ビルド
go build -o pi-obd-meter ./cmd/server
```

### 3-2. 設定ファイル

`config.yaml` (例):

```yaml
obd:
  port: /dev/rfcomm0       # Bluetooth OBD
  # port: /dev/ttyUSB0     # USB OBD
  baud: 38400

server:
  port: 8080
  static_dir: ./web/static

engine:
  displacement_l: 1.348     # ZJ-VE
  thermal_efficiency: 0.28
  max_power_ps: 91
  max_power_rpm: 6000
  max_torque_nm: 124
  max_torque_rpm: 3500
```

### 3-3. 動作確認

```bash
# アプリ起動
./pi-obd-meter

# 別ターミナルでブラウザ確認
chromium-browser http://localhost:8080/static/meter.html
```

---

## Phase 4: キオスクモード自動起動

### 4-1. systemdサービス作成

`/etc/systemd/system/pi-obd-meter.service`:

```ini
[Unit]
Description=pi-obd-meter OBD Server
After=network.target rfcomm.service
Wants=rfcomm.service

[Service]
Type=simple
User=pi
WorkingDirectory=/home/pi/pi-obd-meter
ExecStart=/home/pi/pi-obd-meter/pi-obd-meter
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable pi-obd-meter
sudo systemctl start pi-obd-meter
```

### 4-2. Chromiumキオスクモード自動起動

`~/.config/autostart/kiosk.desktop`:

```ini
[Desktop Entry]
Type=Application
Name=OBD Meter Kiosk
Exec=/bin/bash -c 'sleep 5 && chromium-browser --kiosk --noerrdialogs --disable-infobars --disable-session-crashed-bubble --incognito --check-for-update-interval=31536000 http://localhost:8080/static/meter.html'
X-GNOME-Autostart-enabled=true
```

`sleep 5` はGoサーバーの起動を待つため。

### 4-3. 画面設定の最適化

```bash
# スクリーンセーバー/画面OFF無効化
sudo apt install xscreensaver  # 既にあれば不要
# xscreensaverの設定で「無効」にする

# または xsetで直接:
# ~/.xinitrc や autostart に追加
xset s off          # スクリーンセーバーOFF
xset -dpms          # DPMS（省電力）OFF
xset s noblank      # ブランキングOFF
```

### 4-4. マウスカーソル非表示

```bash
sudo apt install unclutter
# autostart に追加
unclutter -idle 0.1 -root &
```

---

## Phase 5: 車載テスト

### チェックリスト

- [ ] Pi電源: シガーソケット→USB給電（5V/3A以上推奨）
- [ ] LCD電源: PiのUSB3から供給 or 別系統
- [ ] OBDアダプタ: OBD-IIポートに接続（DYデミオはハンドル下）
- [ ] ACC ON → Pi自動起動 → メーター表示確認
- [ ] 走行中の瞬間燃費・速度・RPMが正常に変化
- [ ] 水温が暖機後に安定（85-95℃付近）
- [ ] LCD視認性: 昼間の直射日光下でも見えるか
- [ ] LCD角度: IPS視野角で上下方向も確認
- [ ] 振動: 走行中にLCDが安定しているか

### トラブルシューティング

| 症状 | 原因と対処 |
|------|-----------|
| LCD映らない | config.txt確認、HDMIケーブル挿し直し |
| OBD接続エラー | rfcomm確認、ACC ONか確認 |
| 燃費データ来ない | PID 0x5E (MAF) 非対応の場合あり → MAP計算に切替 |
| Pi起動遅い | SDカード速度、不要サービス無効化 |
| 画面が暗い | LCD輝度調整（OSD or config） |

---

## 起動シーケンス（最終形）

```
ACC ON
  → Pi電源ON
    → systemd: rfcomm.service (Bluetooth OBDバインド)
    → systemd: pi-obd-meter.service (Goサーバー起動)
    → autostart: kiosk.desktop (Chromiumキオスクモード)
      → http://localhost:8080/static/meter.html 表示
        → WebSocketでリアルタイムOBDデータ受信
          → メーター描画開始
```

ACC OFF → Pi安全シャットダウン（要検討: GPIO or USBパワーダウン検知）
