#!/bin/bash
# 冷感始動時の CAN ログを自動取得するためのワンショットスクリプト。
# arm ファイル `/var/lib/pi-obd-meter/cold-candump-armed` が存在する場合のみ実行され、
# 完了後に arm ファイルを削除する (= 次回起動では自動的に実行されない)。
#
# 使い方: arm するには `sudo touch /var/lib/pi-obd-meter/cold-candump-armed` を実行し、
# 次回エンジン始動 (= Pi 起動) を待つ。systemd の cold-candump.service が boot 時に
# このスクリプトを呼び、arm されていれば 10 分間 candump を回す。
#
# 出力先: /home/laurel/can-logs/cold_YYYYMMDD_HHMMSS.txt
#
# 環境変数 COLD_CANDUMP_SEC でキャプチャ秒数を上書き可能 (デフォルト 600 秒 = 10 分)。
set -u

FLAG=/var/lib/pi-obd-meter/cold-candump-armed
if [ ! -f "$FLAG" ]; then
  exit 0
fi

DURATION="${COLD_CANDUMP_SEC:-600}"
OUT_DIR=/home/laurel/can-logs
STAMP=$(date +%Y%m%d_%H%M%S)
OUT="$OUT_DIR/cold_${STAMP}.txt"

mkdir -p "$OUT_DIR"
chown laurel:laurel "$OUT_DIR" 2>/dev/null || true

# can0 が UP するまで最大 30 秒待つ (起動直後はカーネルが setup 中)
for _ in $(seq 1 30); do
  if ip link show can0 2>/dev/null | grep -q "state UP"; then
    break
  fi
  sleep 1
done

# laurel 権限で candump を実行 (ログファイル所有者が他のスクリプトと一致)
sudo -u laurel timeout "$DURATION" candump can0 -t A > "$OUT" 2>&1 || true
chown laurel:laurel "$OUT" 2>/dev/null || true

# 完了したら arm を解除 (再起動しても再実行されない)
rm -f "$FLAG"
