#!/bin/bash
# CANデコード表の検証用ロガー
#
# 全CAN IDを無フィルタで記録する。何を探しているか未確定の段階でIDを絞ると、
# 稀にしか流れないフレームを取りこぼすため。
#
# 重要: pi-obd-meter は CAN 接続のたびに `ip link set can0 down/up` を実行する。
# これが走行中の candump を落とすため、生死を監視して自動復帰させる。
# （この監視が無かったため 2026-08-18 の走行データを取り逃した）
set -u
OUT=/var/log/can-verify
LIMIT_MB=2048
mkdir -p "$OUT"
# 同名ファイルへの追記を避ける。
#
# Pi に RTC が無く、overlayfs 有効化後は systemd-timesyncd の保存時刻も
# 再起動で消えるため、毎回ほぼ同じ STAMP になりうる。同名ファイルに追記すると
# 複数ブートのストリームが1本に混ざり、距離パルスの積算が破綻する。実際
# 2026-09-06 のログは4ブート分が混ざり、45km の走行を 139km と誤計数した。
BASE=$(date +%Y%m%d_%H%M%S)
STAMP="$BASE"
SEQ=0
while [ -e "$OUT/raw_$STAMP.log" ] || [ -e "$OUT/api_$STAMP.log" ]; do
  SEQ=$((SEQ+1))
  STAMP="${BASE}_$SEQ"
done
RAW="$OUT/raw_$STAMP.log"
API="$OUT/api_$STAMP.log"

# PID ファイルはインスタンス毎に分ける。
# 共有していたため、複数インスタンスが互いの PID を上書きし、全員が
# 「candump が死んだ」と誤判定して多重起動していた。上の連番と合わせて、
# 1本のログには1本の candump しか書かないことを保証する。
PIDF="/run/can-verify-candump.$$.pid"
cleanup() { kill "$(cat "$PIDF" 2>/dev/null)" 2>/dev/null; rm -f "$PIDF"; }
trap cleanup EXIT INT TERM

start_candump() {
  candump -ta can0 >> "$RAW" 2>/dev/null &
  echo $! > "$PIDF"
}
start_candump

i=0
while true; do
  # --- candump の生存監視。落ちていれば即再起動 ---
  if ! kill -0 "$(cat "$PIDF" 2>/dev/null)" 2>/dev/null; then
    echo "# $(date -Is) candump restarted" >> "$RAW"
    start_candump
  fi

  ts=$(date +%s)
  body=$(curl -s --max-time 2 http://localhost:9090/api/realtime 2>/dev/null)
  [ -n "$body" ] && echo "$ts $body" >> "$API"

  # 60秒ごとに容量点検。上限超過なら最古から削除
  i=$((i+1))
  if [ $((i % 60)) -eq 0 ]; then
    used=$(du -sm "$OUT" 2>/dev/null | cut -f1)
    while [ "${used:-0}" -gt "$LIMIT_MB" ]; do
      oldest=$(ls -t "$OUT"/raw_*.log 2>/dev/null | tail -1)
      [ -z "$oldest" ] && break
      [ "$oldest" = "$RAW" ] && break
      rm -f "$oldest" "${oldest/raw_/api_}"
      used=$(du -sm "$OUT" 2>/dev/null | cut -f1)
    done
  fi
  sleep 1
done
