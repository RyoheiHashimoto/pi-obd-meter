#!/bin/bash
# LCD キオスクモード起動スクリプト
# kiosk.service (systemd) から自動起動される
# 手動実行: bash /opt/pi-obd-meter/configs/kiosk.sh

# Wayland 環境設定（Raspberry Pi OS bookworm+ は labwc）
export WAYLAND_DISPLAY=wayland-0
export XDG_RUNTIME_DIR="/run/user/$(id -u)"

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
    --ozone-platform=wayland \
    "http://localhost:${PORT}/meter.html"
