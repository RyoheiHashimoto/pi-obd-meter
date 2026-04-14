#!/bin/bash
# LCD キオスクモード起動スクリプト (Wayland/labwc 対応)
# labwc の autostart (~/.config/labwc/autostart) から起動される

# config.jsonからポート番号を取得
CONFIG="/opt/pi-obd-meter/configs/config.json"
PORT=$(grep -o '"local_api_port":[[:space:]]*[0-9]*' "$CONFIG" | grep -o '[0-9]*')
PORT="${PORT:-9090}"

# pi-obd-meterの起動を待つ
echo "Waiting for pi-obd-meter API on port ${PORT}..."
until curl -s "http://localhost:${PORT}/api/realtime" > /dev/null 2>&1; do
    sleep 1
done

# Chromiumプロファイル（毎回クリーン起動）
KIOSK_PROFILE="/tmp/chromium-kiosk"
rm -rf "${KIOSK_PROFILE}"
mkdir -p "${KIOSK_PROFILE}/Default"
cat > "${KIOSK_PROFILE}/Default/Preferences" << 'EOF'
{"translate":{"enabled":false},"translate_blocked_languages":["ja","en"],"intl":{"accept_languages":"ja"},"browser":{"enable_spellchecking":false}}
EOF

# Wayland ネイティブ + 黒背景初期化（白フラッシュ対策）
exec chromium \
    --ozone-platform=wayland \
    --enable-features=UseOzonePlatform \
    --kiosk \
    --noerrdialogs \
    --disable-infobars \
    --disable-translate \
    --no-first-run \
    --disable-features=Translate,TranslateUI \
    --disable-background-networking \
    --disable-sync \
    --disable-default-apps \
    --disable-session-crashed-bubble \
    --disable-component-update \
    --password-store=basic \
    --disable-extensions \
    --user-data-dir="${KIOSK_PROFILE}" \
    --lang=ja \
    --disk-cache-dir=/dev/null \
    --window-size=800,480 \
    --window-position=0,0 \
    --default-background-color=000000ff \
    --hide-scrollbars \
    --force-device-scale-factor=1 \
    "http://localhost:${PORT}/meter.html"
