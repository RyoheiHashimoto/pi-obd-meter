#!/bin/bash
# メーター画面の定期スクリーンキャプチャ（実走テスト用）
# 使い方: ./capture-screenshots.sh [start|stop]

DIR="$HOME/meter-screenshots"
PIDFILE="/tmp/meter-screenshots.pid"
INTERVAL=10

case "${1:-start}" in
  start)
    mkdir -p "$DIR"
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
      echo "既に実行中 (PID $(cat "$PIDFILE"))"
      exit 1
    fi
    echo "スクリーンショット開始: ${INTERVAL}秒間隔 → $DIR"
    (
      while true; do
        DISPLAY=:0 scrot "$DIR/meter_$(date +%Y%m%d_%H%M%S).png" 2>/dev/null
        sleep "$INTERVAL"
      done
    ) &
    echo $! > "$PIDFILE"
    echo "PID: $(cat "$PIDFILE")"
    ;;
  stop)
    if [ -f "$PIDFILE" ]; then
      kill "$(cat "$PIDFILE")" 2>/dev/null
      rm -f "$PIDFILE"
      COUNT=$(ls "$DIR"/*.png 2>/dev/null | wc -l)
      echo "停止。${COUNT} 枚撮影済み → $DIR"
    else
      echo "実行中のキャプチャなし"
    fi
    ;;
  *)
    echo "Usage: $0 [start|stop]"
    ;;
esac
