# CLAUDE.md — DYデミオ燃費メーター

## プロジェクト概要

OBD-2対応車向けの車載燃費メーター。
Raspberry Pi 4 + ELM327 (Bluetooth) でリアルタイム燃費を5インチLCDに表示し、トリップデータをGoogle Sheetsに自動記録する。
車両固有のパラメータ（排気量、燃費計算方式、レッドゾーン等）はconfig.jsonで設定するため、OBD-2対応車であれば車種を問わず利用可能。
開発・動作確認はDYマツダデミオ（DBA-DY3W, ZJ-VE 1.3L）で行っている。

## 技術スタック

- **言語:** Go（車載バイナリ）+ HTML/CSS/JS（メーターUI）+ Google Apps Script（データ記録）
- **ターゲット:** Raspberry Pi 4 Model B 2GB (ARM64, Raspberry Pi OS Lite 64-bit)
- **ディスプレイ:** ELECROW 5インチ IPS HDMI タッチモニター (800×480) — Pi USBから給電可 (5V/1A)
- **OBD通信:** ELM327 V1.5 Bluetooth 2.0 → /dev/rfcomm0 (SPPシリアル)
- **データ保存:** Google Sheets (GAS webhook)。ラズパイ側にデータ永続化なし（overlayFS前提）
- **通知:** Discord Webhook

## ビルド & デプロイ

```bash
# ローカルビルド（Mac上でのコンパイルチェック）
go build ./cmd/pi-obd-meter
go build ./cmd/pi-obd-scanner

# クロスコンパイル（Mac → ARM64）
./scripts/deploy.sh build

# ラズパイにデプロイ（build + rsync + systemctl restart）
./scripts/deploy.sh deploy

# Web UIだけデプロイ
./scripts/deploy.sh deploy-web

# 初回セットアップ（ディレクトリ作成 + systemd登録）
./scripts/deploy.sh setup

# ログ確認
./scripts/deploy.sh logs
```

ターゲット: `GOOS=linux GOARCH=arm64`
デプロイ先: `pi@raspberrypi.local:/opt/pi-obd-meter/`

## ディレクトリ構成

```
cmd/
  pi-obd-meter/main.go      メインアプリ。OBD読取→表示→送信を統合
  pi-obd-scanner/main.go     対応PIDスキャナー（診断・初期確認用）

internal/
  obd/
    elm327.go               ELM327シリアル通信（AT初期化、PIDリクエスト、マルチPIDバッチ）
    pid.go                  OBD-2 PID定義（RPM, 速度, MAP, IAT, 水温, スロットル, O2）
    dtc.go                  DTC読取（Mode 03）。約80件の日本語エラーコード辞書
    fuel.go                 燃料消費計算（MAP方式）
  trip/
    tracker.go              トリップ追跡。車速積分で走行距離、燃料積算
  sender/
    client.go               GAS Webhook送信。メモリ内リトライキュー（最大100件）
  notify/
    discord.go              Discord Webhook。メンテナンス警告のみ（トリップ通知はGAS側）
  display/
    brightness.go           xrandr経由の輝度制御。時刻ベース自動調整
  maintenance/
    reminder.go             走行距離ベースのメンテナンスリマインダー

web/static/
  meter.html                メーター画面。270° SVGアークゲージ、60fps補間アニメーション
  control.html              スマホ操作UI。トリップリセット、DTC確認、輝度調整

gas/
  webhook.gs                Google Apps Script。doPost受信→シート書込→Discord通知→ダッシュボード更新

configs/
  config.json               アプリ設定（シリアルポート、webhook URL等）
  pi-obd-meter.service        systemdユニットファイル
  kiosk.sh                  Chromiumキオスクモード起動スクリプト

docs/
  deploy-guide.md           セットアップ・デプロイ手順（詳細）
  communication-diagram-v2.svg  通信構成図
```

## アーキテクチャ上の重要な決定

### データフロー
```
ECU → ELM327 (CAN 2.0B) → Pi (BT rfcomm) → GAS Webhook → Google Sheets + Discord
                                            → Discord直接（メンテ警告のみ）
```

### overlayFS
- ラズパイのSDカードは overlayFS で保護する（エンジンOFF = 電源断からの保護）
- **開発中は OFF**にしておく。`make deploy` で自由に書き込める
- 安定運用に入ったら ON にする。更新時だけ一時的に OFF → デプロイ → ON
- 詳細は `docs/deploy-guide.md` セクション5

### SD書き込みゼロ設計
- トリップデータ: GAS Webhookで即送信。送信失敗時はメモリ内キュー（最大100件）
- ログ: journald（RAM上）
- 設定ファイル: overlayFS有効時はRAM上のコピーを読む

### ELM327通信
- Bluetooth 2.0 Classic (SPP)。BLEはGATT複雑で不採用
- rfcomm bind → /dev/rfcomm0 でシリアルポートとして扱う
- マルチPIDバッチ: 1リクエストで複数PID取得（6-7往復 → 2往復、~3.5-4Hz）
- ECUレスポンスが律速（50-100ms/PID）。BT遅延（20-50ms）は支配的でない

### 燃費計算
- 起動時に PID 0x00 でサポートPIDをスキャンし、MAF/MAP方式を自動判定（config不要）
- **MAF方式**: PID 0x10 からMAF値を直接取得。精度高、校正不要。トヨタ・日産・ホンダ等
- **MAP方式**: MAP + 吸気温度 + RPM + 排気量 → 理想気体の状態方程式で吸入空気量推定。マツダZファミリー等MAFなし車向け
- 体積効率(VE)は `ve_coefficient` configで車種ごとに校正（満タン法）
- 排気量は `engine_displacement_cc` configで設定
- レッドゾーンは `redline_rpm` configで設定（メーターUI描画に使用）
- 車両固有の定数はすべてconfig.jsonに外出し済み。コード内にハードコードしない

### メーターUI
- 800×480 全画面、270° SVGアークゲージ
- requestAnimationFrame で60fps補間（ease-out cubic, 300ms）
- OBDデータ更新は~3.5-4Hz、アニメーションで滑らかに見せる
- 時刻ベースで自動輝度調整（xrandr --brightness）
- レッドゾーン描画は `redline_rpm` configから取得

### ディスプレイ設定 (config.txt)
ELECROW 5インチ IPS (800×480) 用。`/boot/firmware/config.txt` に追記:
```
hdmi_force_hotplug=1
max_usb_current=1
hdmi_drive=1
hdmi_group=2
hdmi_mode=87
hdmi_cvt 800 480 60 6 0 0 0
```
- `max_usb_current=1`: USB出力電流上限を引き上げ（モニターへのUSB給電に必要）
- `hdmi_force_hotplug=1`: HDMI接続検出を強制（モニター先起動でも映る）
- `hdmi_cvt 800 480 60 6 0 0 0`: 800×480 60Hz カスタムモード
- タッチ機能は車載では不使用（dtoverlay設定は不要）
- モニター給電: Pi 4 USB-A → ELECROW micro USB (5V/1A)

### DTC（診断トラブルコード）
- Mode 03 読取のみ。Mode 04（コードクリア）は意図的に未実装
- 理由: 車検前のCELリセット事故防止
- 3段階の重大度分類: critical / warning / info

### Discord通知の責任分離
- トリップ完了通知: GAS側で送信（Pi → GAS → Sheets書込 → Discord）
- メンテナンス警告: Pi から直接送信（Sheets不要）
- Pi側のdiscord.goからはトリップ通知を削除済み（二重送信防止）

## 車両固有の設定（config.json）

以下はすべてconfig.jsonで設定する。コード内にハードコードしない。

- `engine_displacement_cc`: 排気量 (例: ZJ-VE=1348, ZY-VE=1498)
- `ve_coefficient`: 体積効率 (MAP方式時のみ。0.80-0.90、満タン法で校正)
- `redline_rpm`: レッドゾーン開始回転数 (例: ZJ-VE=6500)
- 空燃比: 14.7:1（ストイキ、ガソリン車共通）
- ガソリン密度: 745 g/L（共通）

### 開発元の確認車両
- マツダ DYデミオ DBA-DY3W / ZJ-VE 1.3L 91PS / 1,090kg / 4AT / CAN 2.0B

## 注意事項

- `go.mod` のモジュール名は `pi-obd-meter`
- エラーメッセージ・ログは英語、UIとドキュメントは日本語
- config.json の webhook_url / discord_webhook は実際のURLに差し替えが必要
- config.json の車両パラメータ（排気量、燃費方式、VE、レッドゾーン）は車種に合わせて設定
- ELM327のBluetoothアドレスは実機に合わせて rfcomm bind する
- 車両固有の定数をコード内にハードコードしないこと（config.jsonから読む）
