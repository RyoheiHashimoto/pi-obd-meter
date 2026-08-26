#!/bin/bash
# Mode 22 拡張PID 全数スキャン（0x0000〜0xFFFF）
#
# 使い方:  sudo bash scan-mode22.sh [開始高位バイト] [終了高位バイト]
#   例:    sudo bash scan-mode22.sh 00 FF    # 全部（約33分）
#          sudo bash scan-mode22.sh 00 3F    # 前半だけ
#
# エンジン稼働中に実行すること。応答は 7E8 に返る。
# 有効なPIDは 0x62 で応答し、無効なPIDは 7F 22 31 (requestOutOfRange) を返す。

set -u
IF=${IF:-can0}
FROM=${1:-00}
TO=${2:-FF}
OUT=/tmp/scan22_full_$(date +%Y%m%d_%H%M%S).log
STEP=${STEP:-0.03}

command -v cansend >/dev/null || { echo "cansend が無い"; exit 1; }
cansend "$IF" 7DF#0201050000000000 || { echo "CAN送信に失敗。can0 は UP か？"; exit 1; }

echo "記録先: $OUT"
candump -ta "$IF",7E8:7F8 > "$OUT" 2>/dev/null &
CD=$!
trap 'kill $CD 2>/dev/null' EXIT
sleep 0.5

hi_start=$((16#$FROM)); hi_end=$((16#$TO))
total=$(( (hi_end - hi_start + 1) * 256 ))
sent=0
for i in $(seq $hi_start $hi_end); do
  HI=$(printf "%02X" "$i")
  for j in $(seq 0 255); do
    LO=$(printf "%02X" "$j")
    cansend "$IF" 7E0#0322${HI}${LO}00000000 2>/dev/null
    sleep "$STEP"
  done
  sent=$(( sent + 256 ))
  found=$(awk '$6=="62"' "$OUT" | wc -l)
  printf "\r  %s帯 完了  %d/%d (%.0f%%)  有効PID %d 個" "$HI" "$sent" "$total" "$(echo "$sent $total" | awk '{print $1/$2*100}')" "$found"
done
echo

sleep 2
kill $CD 2>/dev/null; wait $CD 2>/dev/null

echo
echo "=== 有効PID一覧 ==="
awk '$6=="62"{ printf "  %s%s : ", $7, $8; for(i=9;i<=NF;i++) printf "%s ", $i; print "" }' "$OUT" | sort -u
echo
echo "有効PID数: $(awk '$6=="62"' "$OUT" | sort -u -k7,8 | wc -l)"
echo "否定応答 : $(awk '$6=="7F"' "$OUT" | wc -l)"
echo "記録先   : $OUT"
