#!/bin/bash
# cog (WPEWebKit) キオスク起動
# labwc autostart から実行される

# 古い orphan cog を確実に殺す (greetd 再起動時にプロセスが残る問題の対策)
pkill -f 'cog.*meter.html' 2>/dev/null

export XCURSOR_THEME=blank
export XCURSOR_SIZE=1
export WEBKIT_FORCE_COMPOSITING_MODE=1
export WEBKIT_DISABLE_COMPOSITING_MODE=0

CONFIG=/opt/pi-obd-meter/configs/config.json
PORT=$(grep -o '"local_api_port":[[:space:]]*[0-9]*' "$CONFIG" | grep -o '[0-9]*')
PORT=${PORT:-9090}

until curl -s "http://localhost:${PORT}/api/realtime" > /dev/null 2>&1; do
    sleep 1
done

exec cog \
    --platform=wl \
    --bg-color=#000000FF \
    --scale=1 \
    "http://localhost:${PORT}/meter.html"
