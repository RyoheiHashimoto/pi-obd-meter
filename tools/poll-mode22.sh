#!/bin/bash
# スキャンで見つかった Mode 22 有効PID を連続ポーリングし、
# 標準PID（水温・吸気温・MAF・MAP）と並べて記録する。
#
# 使い方:  sudo bash poll-mode22.sh <scan-mode22.sh のログ> [記録先]
#
# 【重要】温度の同定には、水温(0x05) と 吸気温(0x0F) の両方との識別が必須。
# 「水温がサーモスタットで動かないこと」だけを対照にすると、
# 吸気温に連動する値を温度と誤認する。実際に #123 でそれをやって誤った。
#
# 正しい判定手順:
#   1. 候補PIDと水温・吸気温を同時に連続記録する
#   2. 候補と吸気温の相互相関を、ラグを変えて計算する
#        ピークが lag 0        → 吸気温の派生。独立した温度ではない
#        ピークが数十秒〜数分   → 独立した熱容量を持つ = 別の物体の温度
#   3. 負荷をかけた直後に遅れて上昇し、無負荷で緩やかに下降するかを確認する
set -u
IF=${IF:-can0}
SCANLOG=${1:?scan-mode22.sh のログを指定してください}
OUT=${2:-/tmp/poll22_$(date +%Y%m%d_%H%M%S).log}

PIDS=$(awk '$6=="62"{print $7 $8}' "$SCANLOG" | sort -u | tr '\n' ' ')
[ -z "$PIDS" ] && { echo "有効PIDが見つからない: $SCANLOG"; exit 1; }
echo "対象PID: $(echo "$PIDS" | wc -w) 個"
echo "記録先 : $OUT"

candump -ta "$IF" > "$OUT" 2>/dev/null &
CD=$!
trap 'kill $CD 2>/dev/null' EXIT

while true; do
  for P in $PIDS; do
    cansend "$IF" 7E0#0322${P}00000000 2>/dev/null
    sleep 0.03
  done
  # 対照群: 水温 / 吸気温 / MAF / MAP
  for P in 05 0F 10 0B; do
    cansend "$IF" 7DF#0201${P}0000000000 2>/dev/null
    sleep 0.03
  done
done
