#!/bin/bash
# 不正電断でPiが起動不能になるのを防ぐ (issue #124 第1層)
#
# 【背景】
# 2026-08 に SD が読めなくなり Pi が起動しなくなった。調べた結果、
# ファイルシステムの破損自体は軽微で、致命的だったのは起動側の設定だった。
#
#   fsck.repair=yes が終了コード 4 (未修復のエラーが残る) を返す
#     → systemd が systemd-fsck-root.service を失敗扱いにする
#     → emergency モードに落ちて起動が止まる
#
# 画面もキーボードも無い車載機では emergency シェルに入れないため、
# SD を抜いて Mac で直すしかなくなる。壊れたことより「壊れたときに
# 起動を諦めること」の方が問題だった。
#
# 【このスクリプトがやること】
#   1. fsck を preen (自動修復できるものだけ直して先へ進む) に変更
#   2. fsck が失敗しても起動を続行させる
#   3. journald をRAM運用にして SD への書き込みを激減させる
#   4. swap を無効化 (SD書き込みの最大要因)
#   5. noatime で読み込みのたびの書き込みを止める
#
# べき等。何度実行してもよい。--rollback で元に戻す。

set -euo pipefail

CMDLINE=/boot/firmware/cmdline.txt
[ -f "$CMDLINE" ] || CMDLINE=/boot/cmdline.txt
BACKUP_DIR=/var/lib/pi-obd-meter/boot-hardening
STAMP=$(date +%Y%m%d-%H%M%S)

die() { echo "エラー: $*" >&2; exit 1; }
note() { echo "[harden-boot] $*"; }

[ "$(id -u)" -eq 0 ] || die "root で実行すること (sudo)"
[ -f "$CMDLINE" ] || die "$CMDLINE が見つからない"

# ---------------------------------------------------------------- rollback
if [ "${1:-}" = "--rollback" ]; then
    latest=$(ls -t "$BACKUP_DIR"/cmdline.txt.* 2>/dev/null | head -1) || true
    [ -n "${latest:-}" ] || die "バックアップが無い"
    cp "$latest" "$CMDLINE"
    note "cmdline.txt を $latest から復元した"
    latest=$(ls -t "$BACKUP_DIR"/fstab.* 2>/dev/null | head -1) || true
    [ -n "${latest:-}" ] && { cp "$latest" /etc/fstab; note "fstab を復元した"; }
    rm -f /etc/systemd/journald.conf.d/pi-obd-volatile.conf
    note "再起動すると元の設定に戻る"
    exit 0
fi

mkdir -p "$BACKUP_DIR"

# ---------------------------------------------- 1. fsck を止まらない設定に
cp "$CMDLINE" "$BACKUP_DIR/cmdline.txt.$STAMP"
note "cmdline.txt を退避: $BACKUP_DIR/cmdline.txt.$STAMP"

line=$(tr -d '\n' < "$CMDLINE")

# 既存の fsck 指定をすべて外してから付け直す (べき等にするため)
for k in fsck.repair fsck.mode; do
    line=$(echo "$line" | sed -E "s/(^| )${k}=[^ ]*//g")
done

# preen: 自動で直せるものだけ直す。直せなければ諦めて起動を続ける。
# repair=yes は「直りきらなかった」だけで起動を止めてしまう。
line="$line fsck.mode=auto fsck.repair=preen"

# 余分な空白を潰す
line=$(echo "$line" | tr -s ' ' | sed 's/^ //; s/ $//')
echo "$line" > "$CMDLINE"
note "cmdline.txt を更新: fsck.mode=auto fsck.repair=preen"

# ------------------------------- 2. fsck が失敗しても emergency に落ちない
# systemd-fsck-root は cmdline だけでは制御しきれないので、
# 失敗を無視する drop-in を置く。
mkdir -p /etc/systemd/system/systemd-fsck-root.service.d
cat > /etc/systemd/system/systemd-fsck-root.service.d/keep-booting.conf <<'CONF'
# fsck が直しきれなくても起動を続ける。
# 画面もキーボードも無い車載機では emergency シェルに入れないため、
# 起動を止めることは「詰み」を意味する。多少壊れたまま起動して
# 遠隔で直せる方が、確実に良い。
[Service]
SuccessExitStatus=0 1 2 4
CONF
note "systemd-fsck-root: 終了コード 1/2/4 も成功扱いにした"

mkdir -p /etc/systemd/system/emergency.service.d
cat > /etc/systemd/system/emergency.service.d/no-console-wait.conf <<'CONF'
# emergency に落ちてもコンソール入力を待たずに再起動を試みる。
# 車載機では誰も応答できないため、待ち続けるより再起動した方がよい。
[Service]
ExecStartPre=/bin/sh -c 'echo "emergency に落ちた。90秒後に再起動する" > /dev/console'
ExecStartPost=/bin/sh -c 'sleep 90; systemctl reboot -f'
CONF
note "emergency: 90秒待って自動再起動するようにした"

# ----------------------------------------- 3. journald を RAM 運用にする
mkdir -p /etc/systemd/journald.conf.d
cat > /etc/systemd/journald.conf.d/pi-obd-volatile.conf <<'CONF'
# ログをRAMだけに置き、SDへ一切書かない。
# SDへの書き込みは不正電断で壊れる最大の要因であり、
# journald は常時書き続けるため影響が大きい。
# 走行ログは別途 /var/log/ に明示的に書いているものだけ残す。
[Journal]
Storage=volatile
RuntimeMaxUse=32M
CONF
note "journald: RAM運用 (Storage=volatile, 上限32M)"

# ------------------------------------------------------- 4. swap を無効化
if systemctl list-unit-files 2>/dev/null | grep -q dphys-swapfile; then
    systemctl disable --now dphys-swapfile 2>/dev/null || true
    note "dphys-swapfile を無効化"
fi
swapoff -a 2>/dev/null || true

# --------------------------------------------- 5. noatime で書き込みを削る
cp /etc/fstab "$BACKUP_DIR/fstab.$STAMP"
if ! grep -qE '^[^#].*\s/\s.*noatime' /etc/fstab; then
    # root 行の第4フィールドに noatime を足す
    awk 'BEGIN{OFS="\t"}
         /^#/ {print; next}
         $2=="/" && $4 !~ /noatime/ {$4=$4",noatime"; print; next}
         {print}' /etc/fstab > /etc/fstab.new
    mv /etc/fstab.new /etc/fstab
    note "fstab: root に noatime を追加"
else
    note "fstab: noatime は既に入っている"
fi

systemctl daemon-reload
sync

note ""
note "適用完了。再起動後に有効になる。"
note "元に戻すには: sudo $0 --rollback"
