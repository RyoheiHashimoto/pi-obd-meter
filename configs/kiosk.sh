#!/bin/bash
# LCD キオスクモード起動スクリプト
# kiosk.service (systemd) から自動起動される
# 手動実行: bash /opt/pi-obd-meter/configs/kiosk.sh

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

# WiFi接続を待つ（未接続ならデスクトップ操作可能 — 新規WiFi設定用）
echo "Waiting for WiFi connection..."
until ip addr show wlan0 2>/dev/null | grep -q 'inet '; do
    sleep 5
done
echo "WiFi connected."

# pi-obd-meterの起動を待つ
echo "Waiting for pi-obd-meter API on port ${PORT}..."
until curl -s "http://localhost:${PORT}/api/realtime" > /dev/null 2>&1; do
    sleep 2
done

# Chromiumをキオスクモードで起動（800x480 フルスクリーン）
# Chromium翻訳無効化の設定を配置
KIOSK_PROFILE="/tmp/chromium-kiosk"
mkdir -p "${KIOSK_PROFILE}/Default"
cat > "${KIOSK_PROFILE}/Default/Preferences" << 'EOF'
{"translate":{"enabled":false},"translate_blocked_languages":["ja","en"],"intl":{"accept_languages":"ja"},"browser":{"enable_spellchecking":false}}
EOF

chromium \
    --kiosk \
    --noerrdialogs \
    --disable-infobars \
    --disable-translate \
    --no-first-run \
    --disable-features=Translate,TranslateUI \
    --password-store=basic \
    --disable-extensions \
    --user-data-dir="${KIOSK_PROFILE}" \
    --lang=ja \
    --disk-cache-dir=/dev/null \
    --window-size=800,480 \
    --window-position=0,0 \
    "http://localhost:${PORT}/meter.html"
