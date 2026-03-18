# CAN キャプチャテスト手順書

DY デミオの未特定 CAN ID (0x200, 0x210, 0x430) のデータを ELM327 経由で取得する。

## 必要なもの

- モバイルバッテリー（Pi 給電用、テスト1-2 のみ）
- スマホ or ノートPC（SSH 用）
- 所要時間: 約15-20分（出発前）

## 前日準備（Mac から）

```bash
# スクリプトを Pi に転送
scp scripts/can_capture.py laurel@pi-obd-meter.local:/opt/pi-obd-meter/scripts/

# または make deploy で全体転送
make deploy
```

## 当日の手順

### 0. Pi 起動（エンジンかける前に）

1. モバイルバッテリーで Pi に給電（USB-C）
2. キーを **ACC** まで回す（エンジンはかけない）
3. Pi が起動するまで1分待つ
4. スマホから SSH:
   ```
   ssh laurel@pi-obd-meter.local
   ```

### 1. メーター停止 & キャプチャ開始

```bash
sudo systemctl stop pi-obd-meter
python3 /opt/pi-obd-meter/scripts/can_capture.py
```

スクリプトが対話モードで起動し、3テストを順番にガイドする。

### テスト1: 0x430（電圧/補機）— 約1分

画面の指示に従う:

1. **Enter** でキャプチャ開始
2. 10秒そのまま（ACC 状態のデータ取得）
3. **エンジン始動**
4. 20秒アイドル
5. **エアコン ON** → 10秒 → **OFF**
6. **ヘッドライト ON** → 10秒 → **OFF**
7. 自動終了（60秒）

### テスト2: 0x200（エンジンセンサー）— 約10分

1. **Enter** でキャプチャ開始
2. アイドル放置（暖機）
3. 水温計が安定するまで待つ
4. **Ctrl+C** で早期終了 OK

> コールドスタート直後からが理想。暖機による値の変化を記録する。

### テスト3: 0x210（AT 制御）— 出発時

1. 停車中に **P → N → D → L** をゆっくり切り替え（各5秒キープ）
2. **Enter** でキャプチャ開始
3. D レンジで発進 → **40km/h** まで加速（変速を記録）
4. 10秒巡航（ロックアップ確認）
5. 減速 → 停車
6. **Ctrl+C** で終了

### 2. メーター復帰

```bash
sudo systemctl start pi-obd-meter
```

モバイルバッテリーを外して車の USB に差し替える。メーターが表示されれば OK。

## データの場所

Pi 上: `/tmp/can_capture/`

```
YYYYMMDD_HHMM_430_voltage.csv
YYYYMMDD_HHMM_200_warmup.csv
YYYYMMDD_HHMM_210_at.csv
```

## データ回収（ドライブ後）

```bash
scp laurel@pi-obd-meter.local:/tmp/can_capture/*.csv ~/Desktop/
```

## トラブルシューティング

### シリアル接続エラー

```bash
# rfcomm が切れている場合
sudo rfcomm bind 0 <ELM327のBTアドレス>
# アドレス確認
bluetoothctl devices
```

### Pi に SSH できない

- Pi とスマホが同じ WiFi に接続されているか確認
- モバイルバッテリーの出力が十分か確認（Pi 4 は 5V/3A 推奨）
- Pi 起動に1-2分かかるので待つ

### BUFFER FULL が出る

単一 ID フィルタ (AT CRA) が効いていない可能性。スクリプトが自動で設定するが、手動で確認:
```bash
python3 /opt/pi-obd-meter/scripts/can_capture.py 430 30 /tmp/test.csv
```
