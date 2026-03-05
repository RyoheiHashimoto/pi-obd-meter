#!/bin/bash
# LCD キオスクモード起動スクリプト
# RasPiの /etc/xdg/lxsession/LXDE-pi/autostart に追加するか
# systemdサービスとして登録する

# 画面設定
export DISPLAY=:0

# スクリーンセーバー無効化
xset s off
xset -dpms
xset s noblank

# マウスカーソル非表示
unclutter -idle 0.5 -root &

# config.jsonからポート番号を取得
CONFIG="/opt/pi-obd-meter/configs/config.json"
PORT=$(grep -o '"local_api_port":[[:space:]]*[0-9]*' "$CONFIG" | grep -o '[0-9]*')
PORT="${PORT:-9090}"

# pi-obd-meterの起動を待つ
echo "Waiting for pi-obd-meter API on port ${PORT}..."
until curl -s "http://localhost:${PORT}/api/realtime" > /dev/null 2>&1; do
    sleep 2
done

# Chromiumをキオスクモードで起動（800x480 フルスクリーン）
chromium-browser \
    --kiosk \
    --noerrdialogs \
    --disable-infobars \
    --disable-translate \
    --no-first-run \
    --fast \
    --fast-start \
    --disable-features=TranslateUI \
    --disk-cache-dir=/dev/null \
    --window-size=800,480 \
    --window-position=0,0 \
    "http://localhost:${PORT}/meter.html"
