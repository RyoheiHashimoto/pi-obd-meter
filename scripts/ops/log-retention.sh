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

# 実体のパスに解決してから使う。
#
# /var/log/can-verify などは /data/log/... へのシンボリックリンク
# (docs/boot-resilience.md)。find も du も既定ではリンクを辿らないため、
# リンクを渡すと find は 0 件、du は 0MB を返す。つまりこのスクリプトは
# 2026-09-09 まで一度も何も消していなかった。上限 2048MB に対して
# can-verify だけで 2.6GB 溜まっていたのがその結果。
#   find <symlink> -type f   -> 0 件
#   du -sm <symlink>         -> 0 MB
# -L を付ける手もあるが、readlink -f で実体に寄せるほうが読み手に明確。
resolve() { readlink -f "$1" 2>/dev/null || echo "$1"; }
CAN_DIR=$(resolve /var/log/can-verify)
DRV_DIR=$(resolve /var/log/drive-verify)
GPS_DIR=$(resolve /var/log/gps)
KEEP_DAYS=${KEEP_DAYS:-30}      # CSV の保持日数
MAX_MB=${MAX_MB:-2048}          # 合計がこれを超えたら古い順に消す
DRY=0
[ "${1:-}" = "--dry-run" ] && DRY=1

log() { echo "[log-retention] $*"; }

# dry-run では実際に消さないので、後段の判定が「もう消える予定のファイル」を
# もう一度数えてしまう。削除予定を控えておき、後段で除外する。
# (2026-09-09: これが無かったため dry-run が 110MB と出したが実際は 0MB だった)
PLANNED=$(mktemp)
trap 'rm -f "$PLANNED"' EXIT
planned() { [ -s "$PLANNED" ] && grep -Fxq "$1" "$PLANNED"; }
run() {
    if [ $DRY -eq 1 ]; then
        log "(dry-run) $*"
        [ "${1:-}" = "rm" ] && echo "${@: -1}" >> "$PLANNED"
    else
        "$@"
    fi
}

# --- 消さないファイルを確定する ---
KEEP_SCAN=$(ls -t "$CAN_DIR"/scan22_full_*.log 2>/dev/null | head -1)
[ -n "$KEEP_SCAN" ] && log "保護: $(basename "$KEEP_SCAN")"

# --- 空・極小ファイル (起動失敗の残骸) ---
while IFS= read -r f; do
    [ "$f" = "$KEEP_SCAN" ] && continue
    run rm -f "$f"
done < <(find "$CAN_DIR" "$DRV_DIR" "$GPS_DIR" -type f -size -2k 2>/dev/null)

# --- 保持日数を超えた CSV ---
n=0
while IFS= read -r f; do
    run rm -f "$f"; n=$((n+1))
done < <(find "$CAN_DIR" "$DRV_DIR" "$GPS_DIR" -type f -name '*.csv' -mtime "+$KEEP_DAYS" 2>/dev/null)
[ $n -gt 0 ] && log "${KEEP_DAYS}日超のCSVを $n 件削除"

# --- 生の candump は解析が済めば不要。7日で消す ---
n=0
while IFS= read -r f; do
    [ "$f" = "$KEEP_SCAN" ] && continue
    run rm -f "$f"; n=$((n+1))
done < <(find "$CAN_DIR" -type f \( -name 'raw_*.log' -o -name 'warmup_*.log' -o -name 'api_*.log' \) -mtime +7 2>/dev/null)
[ $n -gt 0 ] && log "7日超の生ログを $n 件削除"

# --- 合計サイズの上限。超えたら古い順に消す ---
total_mb() { du -sm "$CAN_DIR" "$DRV_DIR" "$GPS_DIR" 2>/dev/null | awk '{s+=$1} END{print s+0}'; }
cur=$(total_mb)
if [ "$cur" -gt "$MAX_MB" ]; then
    log "合計 ${cur}MB が上限 ${MAX_MB}MB を超過。古い順に削除する"
    if [ $DRY -eq 1 ]; then
        # dry-run は削除しないので total_mb が減らない。1件見せて打ち切ると
        # 「何がどれだけ消えるのか」が分からず、確認の役に立たない。
        # 消した想定でサイズを引きながら、消える全件を出す。
        # 先の段で消える分を引いてから、足りない分だけ追加で選ぶ
        pre_kb=0
        while IFS= read -r f; do
            [ -f "$f" ] && pre_kb=$((pre_kb + $(du -k "$f" | cut -f1)))
        done < "$PLANNED"
        need_kb=$(( (cur - MAX_MB) * 1024 - pre_kb ))
        [ "$need_kb" -lt 0 ] && need_kb=0
        log "(dry-run) 先の段で $((pre_kb/1024))MB が消える"
        acc=0; cnt=0
        if [ "$need_kb" -le 0 ]; then
            log "(dry-run) 先の段だけで上限に収まる。追加の削除は無い"
        fi
        while [ "$need_kb" -gt 0 ] && IFS= read -r line; do
            sz=${line%% *}; f=${line#* }
            [ "$f" = "$KEEP_SCAN" ] && continue
            planned "$f" && continue
            acc=$((acc + sz)); cnt=$((cnt + 1))
            log "(dry-run) rm -f $f  (${sz}KB, 累計 $((acc/1024))MB)"
            [ "$acc" -ge "$need_kb" ] && break
        done < <(find "$CAN_DIR" "$DRV_DIR" "$GPS_DIR" -type f -printf '%T@\t%k\t%p\n' 2>/dev/null \
                 | sort -n | cut -f2-  | tr '\t' ' ')
        log "(dry-run) 合計 $cnt 件 / $((acc/1024))MB を削除すると上限に収まる"
    else
        while [ "$(total_mb)" -gt "$MAX_MB" ]; do
            oldest=$(find "$CAN_DIR" "$DRV_DIR" "$GPS_DIR" -type f -printf '%T@ %p\n' 2>/dev/null \
                     | grep -v "$KEEP_SCAN" | sort -n | head -1 | cut -d' ' -f2-)
            [ -z "$oldest" ] && break
            run rm -f "$oldest"
        done
    fi
fi

log "完了。can-verify $(du -sh $CAN_DIR 2>/dev/null | cut -f1) / drive-verify $(du -sh $DRV_DIR 2>/dev/null | cut -f1) / gps $(du -sh $GPS_DIR 2>/dev/null | cut -f1) / 空き $(df -h / | awk 'NR==2{print $4}')"
