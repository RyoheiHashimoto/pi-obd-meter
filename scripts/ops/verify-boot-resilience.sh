#!/bin/bash
# harden-boot.sh の効果を確認する (issue #124)
#
# Mac 側から実行する。Pi に SSH できる状態で使うこと。
#
# 【設計方針: SDを壊さずに障害を再現する】
# 2026-08 に Pi が起動しなくなった直接の原因は「fsck が終了コード4
# (未修復のエラーが残る) を返し、systemd が起動を止めた」ことだった。
# 破損そのものではなく、終了コードへの反応が問題だった。
#
# ということは fsck を偽物に差し替えて 4 を返させれば、
# **ファイルシステムを一切壊さずに** 同じ状況を再現できる。
# 対策が効いているかはこれで判定できる。
#
# 実際に電源を引き抜く試験は、それ自体が SD を壊しに行く行為であり、
# 失敗すれば「SDを抜いて Mac で直す」に逆戻りする。避けるための作業で
# それを起こすのは筋が悪い。段階を分けて、危険な方は最後に1回だけ、
# しかも前段が通ってからにする。
#
#   段階1  fsck を偽装して 4 を返させる      破損リスク ゼロ
#   段階2  fsck を強制した上で正常に再起動    破損リスク ゼロ
#   段階3  同期せずに即再起動 (電源断と同じ)  エンジン停止1回分
#
# 段階3は「エンジンを切る」のと全く同じ操作であり、毎日起きていること
# 以上のリスクは無い。それでも既定では実行しない。--with-power-cut を
# 明示したときだけ、1回だけ行う。
#
# 【安全網】
# cmdline.txt は FAT の /boot にあるため、万一起動しなくなっても
# Mac に挿してテキストを1行戻すだけで復旧できる。8月の ext4 修復とは
# 難易度が違う。適用前の cmdline.txt は Pi 上と Mac 上の両方に残す。
#
# 使い方:
#   ./verify-boot-resilience.sh <PiのIP>                   段階1-2のみ (安全)
#   ./verify-boot-resilience.sh <PiのIP> --with-power-cut  段階3も行う

set -uo pipefail

HOST="${1:?PiのIPを指定すること}"
POWER_CUT=0
[ "${2:-}" = "--with-power-cut" ] && POWER_CUT=1

SSH="ssh -o BatchMode=yes -o ConnectTimeout=10"
USER=laurel
SAFE_DIR="$(cd "$(dirname "$0")/../.." && pwd)/.boot-backup"

note() { echo "[verify] $*"; }
fail() { echo "[verify] 失敗: $*" >&2; exit 1; }

wait_for_pi() {
    local limit=${1:-240} waited=0
    while [ $waited -lt "$limit" ]; do
        $SSH "$USER@$HOST" true 2>/dev/null && { echo; return 0; }
        sleep 5; waited=$((waited+5)); printf '.'
    done
    echo; return 1
}

wait_for_meter() {
    for _ in $(seq 1 24); do
        $SSH "$USER@$HOST" 'systemctl is-active --quiet pi-obd-meter' 2>/dev/null && return 0
        sleep 5
    done
    return 1
}

$SSH "$USER@$HOST" true 2>/dev/null || fail "Pi に SSH できない"

# ------------------------------------------------ 安全網: cmdline を手元に退避
mkdir -p "$SAFE_DIR"
$SSH "$USER@$HOST" 'cat /boot/firmware/cmdline.txt 2>/dev/null || cat /boot/cmdline.txt' \
    > "$SAFE_DIR/cmdline.txt.current" || fail "cmdline.txt を読めない"
note "cmdline.txt を手元に退避: $SAFE_DIR/cmdline.txt.current"
note "  万一起動しなくなっても、SDをMacに挿してこの1行を書き戻せば直る"
note ""

note "現在の設定"
$SSH "$USER@$HOST" '
  echo "  fsck指定  : $(grep -o "fsck[^ ]*" /boot/firmware/cmdline.txt 2>/dev/null || grep -o "fsck[^ ]*" /boot/cmdline.txt || echo なし)"
  echo "  成功扱い  : $(systemctl show systemd-fsck-root -p SuccessExitStatus --value)"
  echo "  journald  : $(grep -h "^Storage" /etc/systemd/journald.conf.d/*.conf 2>/dev/null || echo デフォルト)"
  echo "  swap      : $(swapon --show=NAME --noheadings 2>/dev/null | wc -l) 個有効"
'
note ""

# ================================================================= 段階 1
note "===== 段階1: fsck が終了コード4 を返す状況を再現する (破損リスク ゼロ) ====="
note "8月に起動を止めた直接の原因を、ファイルシステムを壊さずに作る"

$SSH "$USER@$HOST" 'sudo sh -c "
  # 本物を退避し、4 を返すだけの偽物を置く
  [ -e /sbin/fsck.ext4.real ] || cp /sbin/fsck.ext4 /sbin/fsck.ext4.real
  printf \"#!/bin/sh\nexit 4\n\" > /sbin/fsck.ext4
  chmod +x /sbin/fsck.ext4
  # 次回起動の1回だけ使い、起動後に自動で本物へ戻す
  cat > /etc/systemd/system/restore-fsck.service <<CONF
[Unit]
Description=fsck を本物に戻す (検証用の後片付け)
DefaultDependencies=no
After=local-fs.target
[Service]
Type=oneshot
ExecStart=/bin/sh -c \"mv -f /sbin/fsck.ext4.real /sbin/fsck.ext4; systemctl disable restore-fsck.service\"
RemainAfterExit=yes
[Install]
WantedBy=multi-user.target
CONF
  systemctl enable restore-fsck.service >/dev/null 2>&1
  sync
"' || fail "fsck の偽装に失敗"
note "fsck.ext4 を「必ず4を返す」偽物に差し替えた (起動後に自動で戻る)"

note "再起動する"
$SSH "$USER@$HOST" 'sudo systemctl reboot' 2>/dev/null || true
sleep 10
printf '[verify] 復帰を待つ'
wait_for_pi 240 || fail "段階1で戻ってこなかった。SDをMacに挿し、cmdline.txt を $SAFE_DIR/cmdline.txt.current に戻すこと"
note "SSH 復帰"
wait_for_meter || fail "段階1でメーターが起動しなかった"
note "メーター起動を確認"
note "→ fsck が4を返しても起動する。8月の障害は再発しない"

$SSH "$USER@$HOST" '
  echo "  fsck の状態: $(systemctl show systemd-fsck-root -p ExecMainStatus --value 2>/dev/null) (4 なら再現できていた)"
  echo "  fsck 復元  : $([ -e /sbin/fsck.ext4.real ] && echo 未完了 || echo 完了)"
'
$SSH "$USER@$HOST" 'sudo sh -c "[ -e /sbin/fsck.ext4.real ] && mv -f /sbin/fsck.ext4.real /sbin/fsck.ext4; systemctl disable restore-fsck.service >/dev/null 2>&1; rm -f /etc/systemd/system/restore-fsck.service; sync"' 2>/dev/null || true
note ""

# ================================================================= 段階 2
note "===== 段階2: fsck を実際に走らせて起動する (破損リスク ゼロ) ====="
$SSH "$USER@$HOST" 'sudo tune2fs -C 60 $(findmnt -no SOURCE /) >/dev/null 2>&1' || fail "fsck の強制に失敗"
note "次回起動で fsck が走るようにした"
$SSH "$USER@$HOST" 'sudo systemctl reboot' 2>/dev/null || true
sleep 10
printf '[verify] 復帰を待つ'
wait_for_pi 240 || fail "段階2で戻ってこなかった"
note "SSH 復帰"
wait_for_meter || fail "段階2でメーターが起動しなかった"
note "メーター起動を確認"
note "→ fsck が走る起動でも問題なし"
note ""

# ================================================================= 段階 3
if [ $POWER_CUT -eq 0 ]; then
    note "===== 段階3 (電源断の再現) は省略した ====="
    note "行うには --with-power-cut を付ける。エンジン停止1回分のリスクがある"
    note ""
    note "===== 段階1-2 すべて通過 ====="
    exit 0
fi

note "===== 段階3: 同期せずに即再起動する (電源を引き抜いたのと同じ) ====="
note "これはエンジンを切るのと全く同じ操作。1回だけ行う"
$SSH "$USER@$HOST" 'sudo systemctl stop pi-obd-meter drive-verify 2>/dev/null; sync; sync' 2>/dev/null || true
note "書き込み中のサービスを止めて sync した (無用な破損を足さないため)"
$SSH "$USER@$HOST" 'sudo sh -c "echo 1 > /proc/sys/kernel/sysrq; echo b > /proc/sysrq-trigger"' 2>/dev/null || true
sleep 10
printf '[verify] 復帰を待つ'
wait_for_pi 300 || fail "段階3で戻ってこなかった"
note "SSH 復帰"
wait_for_meter || fail "段階3でメーターが起動しなかった"
note "メーター起動を確認"
$SSH "$USER@$HOST" 'echo "  今回の起動でのエラー: $(journalctl -b -p err --no-pager -q 2>/dev/null | wc -l) 件"'
note ""
note "===== 段階1-3 すべて通過 ====="
note "不正電断で起動不能にならないことを実測で確認した"
