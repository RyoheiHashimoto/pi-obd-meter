#!/bin/bash
# 実走テスト: candump + スクショキャプチャの開始/停止/回収
# 使い方:
#   ./scripts/drive-test.sh start   — Pi で candump + スクショ開始
#   ./scripts/drive-test.sh stop    — 停止
#   ./scripts/drive-test.sh pull    — Mac にデータ回収
#   ./scripts/drive-test.sh status  — 状態確認

PI_HOST="${PI_HOST:-laurel@pi-obd-meter.local}"
DATE=$(date +%Y%m%d)
REMOTE_CAN_DIR="$HOME/can-logs"
REMOTE_SS_DIR="$HOME/meter-screenshots"
LOCAL_DIR="$HOME/Desktop/drive_test_${DATE}"

ssh_cmd() {
    ssh -o ConnectTimeout=10 "$PI_HOST" "$@"
}

case "${1:-status}" in
  start)
    echo "=== 実走テスト開始 ==="
    ssh_cmd bash -c "'
      mkdir -p $REMOTE_CAN_DIR $REMOTE_SS_DIR
      # candump 開始
      if pgrep -x candump >/dev/null; then
        echo \"candump: 既に実行中\"
      else
        nohup candump can0 -t A > $REMOTE_CAN_DIR/can_${DATE}.txt 2>&1 &
        echo \"candump: 開始 (PID \$!) → $REMOTE_CAN_DIR/can_${DATE}.txt\"
      fi
    '"
    # スクショ開始
    ssh_cmd /opt/pi-obd-meter/scripts/capture-screenshots.sh start
    echo "=== 走行してください ==="
    ;;

  stop)
    echo "=== 実走テスト停止 ==="
    ssh_cmd bash -c "'
      pkill candump 2>/dev/null && echo \"candump: 停止\" || echo \"candump: 未実行\"
    '"
    ssh_cmd /opt/pi-obd-meter/scripts/capture-screenshots.sh stop
    echo ""
    $0 status
    ;;

  pull)
    echo "=== データ回収: ${LOCAL_DIR} ==="
    mkdir -p "${LOCAL_DIR}/screenshots"

    echo "--- CAN ログ ---"
    scp "${PI_HOST}:${REMOTE_CAN_DIR}/can_${DATE}.txt" "${LOCAL_DIR}/" 2>&1 && \
      echo "OK: $(du -h "${LOCAL_DIR}/can_${DATE}.txt")" || echo "FAIL: CAN ログなし"

    echo "--- スクリーンショット ---"
    scp "${PI_HOST}:${REMOTE_SS_DIR}/meter_${DATE}_*.png" "${LOCAL_DIR}/screenshots/" 2>&1 && \
      echo "OK: $(ls "${LOCAL_DIR}/screenshots/" | wc -l | tr -d ' ') 枚" || echo "FAIL: スクショなし"

    echo "--- Pi ログ ---"
    ssh_cmd "journalctl -u pi-obd-meter --no-pager --since today" > "${LOCAL_DIR}/pi-obd-meter.log" 2>&1 && \
      echo "OK: $(wc -l < "${LOCAL_DIR}/pi-obd-meter.log") 行" || echo "FAIL: ログ取得失敗"

    echo ""
    echo "=== 回収完了: ${LOCAL_DIR} ==="
    du -sh "${LOCAL_DIR}"
    ;;

  status)
    echo "=== 状態確認 ==="
    ssh_cmd bash -c "'
      echo \"--- プロセス ---\"
      ps aux | grep -E \"candump|capture\" | grep -v grep || echo \"(なし)\"
      echo \"--- CAN ログ ---\"
      ls -lh $REMOTE_CAN_DIR/can_${DATE}.txt 2>/dev/null && wc -l $REMOTE_CAN_DIR/can_${DATE}.txt || echo \"(なし)\"
      echo \"--- スクリーンショット ---\"
      ls $REMOTE_SS_DIR/meter_${DATE}_*.png 2>/dev/null | wc -l | tr -d \" \" | xargs -I{} echo \"{} 枚\" || echo \"(なし)\"
    '"
    ;;

  *)
    echo "Usage: $0 [start|stop|pull|status]"
    exit 1
    ;;
esac
