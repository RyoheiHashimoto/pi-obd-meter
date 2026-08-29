#!/bin/bash
# 走行ログを一定期間で消す。
#
# ロガーは 4.5 MB/時 書く。1日2時間走れば年 3.3GB になり、放っておくと
# いつか埋まる。埋まってから気づくのでは遅い。
#
# 消してはいけないもの:
#   scan22_full_*.log の最新1本  — poll22 が有効PIDを読む元
#
# systemd タイマーから1日1回実行する。--dry-run で確認できる。

set -uo pipefail

CAN_DIR=/var/log/can-verify
DRV_DIR=/var/log/drive-verify
KEEP_DAYS=${KEEP_DAYS:-30}      # CSV の保持日数
MAX_MB=${MAX_MB:-2048}          # 合計がこれを超えたら古い順に消す
DRY=0
[ "${1:-}" = "--dry-run" ] && DRY=1

log() { echo "[log-retention] $*"; }
run() { if [ $DRY -eq 1 ]; then log "(dry-run) $*"; else "$@"; fi; }

# --- 消さないファイルを確定する ---
KEEP_SCAN=$(ls -t "$CAN_DIR"/scan22_full_*.log 2>/dev/null | head -1)
[ -n "$KEEP_SCAN" ] && log "保護: $(basename "$KEEP_SCAN")"

# --- 空・極小ファイル (起動失敗の残骸) ---
while IFS= read -r f; do
    [ "$f" = "$KEEP_SCAN" ] && continue
    run rm -f "$f"
done < <(find "$CAN_DIR" "$DRV_DIR" -type f -size -2k 2>/dev/null)

# --- 保持日数を超えた CSV ---
n=0
while IFS= read -r f; do
    run rm -f "$f"; n=$((n+1))
done < <(find "$CAN_DIR" "$DRV_DIR" -type f -name '*.csv' -mtime "+$KEEP_DAYS" 2>/dev/null)
[ $n -gt 0 ] && log "${KEEP_DAYS}日超のCSVを $n 件削除"

# --- 生の candump は解析が済めば不要。7日で消す ---
n=0
while IFS= read -r f; do
    [ "$f" = "$KEEP_SCAN" ] && continue
    run rm -f "$f"; n=$((n+1))
done < <(find "$CAN_DIR" -type f \( -name 'raw_*.log' -o -name 'warmup_*.log' -o -name 'api_*.log' \) -mtime +7 2>/dev/null)
[ $n -gt 0 ] && log "7日超の生ログを $n 件削除"

# --- 合計サイズの上限。超えたら古い順に消す ---
total_mb() { du -sm "$CAN_DIR" "$DRV_DIR" 2>/dev/null | awk '{s+=$1} END{print s+0}'; }
cur=$(total_mb)
if [ "$cur" -gt "$MAX_MB" ]; then
    log "合計 ${cur}MB が上限 ${MAX_MB}MB を超過。古い順に削除する"
    while [ "$(total_mb)" -gt "$MAX_MB" ]; do
        oldest=$(find "$CAN_DIR" "$DRV_DIR" -type f -printf '%T@ %p\n' 2>/dev/null \
                 | grep -v "$KEEP_SCAN" | sort -n | head -1 | cut -d' ' -f2-)
        [ -z "$oldest" ] && break
        run rm -f "$oldest"
        [ $DRY -eq 1 ] && break   # dry-run では無限ループを避ける
    done
fi

log "完了。can-verify $(du -sh $CAN_DIR 2>/dev/null | cut -f1) / drive-verify $(du -sh $DRV_DIR 2>/dev/null | cut -f1) / 空き $(df -h / | awk 'NR==2{print $4}')"
