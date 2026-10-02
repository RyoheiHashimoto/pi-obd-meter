#!/bin/bash
# 再起動で消えては困る設定が、今の起動で本当に効いているかを確かめる。
#
# 【なぜ要るか】
# root は overlayfs で、上層は RAM。/etc を直接書き換えた変更は、その場では
# 効いて見えるが再起動で黙って消える。2026-09-09 に journald の上限を 512M に
# 上げたつもりが上層にしか書かれておらず、2026-10-02 まで 3 週間、前回起動の
# システム記録が毎回まるごと消えていた (docs/boot-resilience.md)。同じ形の
# 取りこぼしは auto-update の scripts (#191)、画面の配信 (2026-09-24) でも
# 起きており、どれも気づくまで何週間もかかった。
#
# 起動直後は上層が空なので、ここで見える状態 = 下層 (SD) に恒久化された状態。
# 起動のたびに確かめれば、消えた設定に次の始動で気づける。
#
# 【見ないもの】
# 設定ファイルの中身は見ない。journald の conf.d は名前順の後勝ちで、ファイルを
# 読んで結論を出し、逆の判断をした前例がある (2026-09-09)。効いている値そのもの
# (journald が起動時に出す上限、マウント先、前回起動の記録の有無) を見る。
#
# 結果は journal に出し (外れは warning)、/data に JSON で残す。
# pi-obd-meter がそれを /api/health と GAS への送信に載せる (internal/health)。
set -uo pipefail

# テストから差し替えられるようにしておく。本番では既定値のまま。
OUT=${OUT:-/data/pi-obd-meter/persist-check.json}
EXPECT_JOURNAL_MAX=${EXPECT_JOURNAL_MAX:-512M}
LOWER=${LOWER:-/media/root-ro}
BOOT_ID_FILE=${BOOT_ID_FILE:-/proc/sys/kernel/random/boot_id}

fails=()
# 先頭の <4> は systemd が warning として journal に入れる印。
ng() { fails+=("$1"); echo "<4>NG: $1"; }
ok() { echo "OK: $1"; }

# --- 1) journal の上限 ---
#
# journald は flush のたびに「System Journal (...) is 64M, max 512M, ...」と出す。
# 上限が 64M のままだと、前回の記録が改名直後に消される (時計が合う前の
# 9/6 の名前が付き、いちばん古いファイルとして扱われるため)。
max=$(journalctl -b -q --no-pager -t systemd-journald 2>/dev/null \
      | grep -o 'System Journal (/var/log/journal[^)]*) is [^,]*, max [0-9.]*[KMGT]' \
      | tail -1 | grep -o 'max [0-9.]*[KMGT]' | cut -d' ' -f2)
if [ -z "$max" ]; then
    ng "journal が SSD に書かれていない (journald の起動メッセージに System Journal が無い)"
elif [ "$max" != "$EXPECT_JOURNAL_MAX" ]; then
    ng "journal の上限が ${max} (期待 ${EXPECT_JOURNAL_MAX})"
else
    ok "journal の上限 ${max}"
fi

# --- 2) 記録と時刻の保存先が SSD か ---
#
# どちらも /etc/fstab の bind で /data へ逃がしている。overlay や tmpfs の上に
# あれば、再起動で中身が消える。
for p in /var/log/journal /var/lib/systemd/timesync; do
    fs=$(findmnt -no FSTYPE --target "$p" 2>/dev/null | head -1)
    case "$fs" in
        overlay|tmpfs|"") ng "${p} が ${fs:-不明} の上にある (再起動で消える)" ;;
        *) ok "${p} は ${fs}" ;;
    esac
done

# --- 3) 前回の起動のシステム記録が残っているか ---
#
# 1) と 2) が正しくても記録が残らない筋はありうる。結果そのものを見る。
# PID 1 (systemd) の記録はどの起動にも必ずある。前回分が無いのは初回起動だけ。
if [ "$(journalctl --list-boots -q --no-pager 2>/dev/null | wc -l)" -ge 2 ]; then
    if [ "$(journalctl -b -1 -q --no-pager -n 1 _PID=1 2>/dev/null | wc -l)" -eq 0 ]; then
        ng "前回の起動のシステム記録が残っていない"
    else
        ok "前回の起動のシステム記録あり"
    fi
fi

# --- 4) SD の下層が読み取り専用に戻っているか ---
#
# overlayroot-chroot は終了時に下層を ro へ戻すが、失敗して rw のまま残る
# ことがある (2026-10-02 に "mount point is busy" で実際に残った)。
# rw のまま電源が落ちると SD の破損につながる。起動時は必ず ro なので、
# これは定期実行の回で意味を持つ。
if findmnt "$LOWER" > /dev/null 2>&1; then
    opts=$(findmnt -no OPTIONS "$LOWER")
    case "$opts" in
        ro,*|ro) ok "${LOWER} は ro" ;;
        *) ng "${LOWER} が rw のまま (sudo overlayroot-chroot true で戻す)" ;;
    esac
fi

# --- 結果を残す ---
#
# boot_id を付ける。読む側 (internal/health) は今回の起動の結果かどうかを
# これで見分け、前の起動に書かれた古い結果を今の状態として出さない。
esc() { local s=${1//\\/\\\\}; printf '%s' "${s//\"/\\\"}"; }
boot_id=$(cat "$BOOT_ID_FILE" 2>/dev/null || echo unknown)
if [ ${#fails[@]} -eq 0 ]; then okjson=true; else okjson=false; fi
items=""
# ${a[@]+"${a[@]}"} は空の配列でも set -u で止まらない書き方 (bash 4.4 未満対策)
for f in ${fails[@]+"${fails[@]}"}; do
    items+="${items:+,}\"$(esc "$f")\""
done

mkdir -p "$(dirname "$OUT")"
tmp="${OUT}.tmp"
printf '{"boot_id":"%s","ok":%s,"failures":[%s]}\n' "$boot_id" "$okjson" "$items" > "$tmp"
# /data は電源がいつ落ちてもおかしくない。rename の前に中身を確定させないと、
# 名前だけ残って 0 バイトになる (2026-09-19 に wifi-watchdog で踏んだ)。
sync "$tmp" 2>/dev/null || sync
mv -f "$tmp" "$OUT"
sync "$(dirname "$OUT")" 2>/dev/null || sync

if [ ${#fails[@]} -gt 0 ]; then
    echo "<4>恒久化の確認: ${#fails[@]} 件外れている"
    exit 1
fi
echo "恒久化の確認: すべて OK"
