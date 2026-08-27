#!/bin/bash
# harden-boot.sh の効果を「実際に壊して」確認する (issue #124)
#
# Mac 側から実行する。Pi に SSH できる状態で使うこと。
#
# 【なぜ壊すのか】
# 設定を入れただけでは「不正電断で起動不能にならない」ことを確かめられない。
# 2026-08 の障害も、事前に一度でも電源を引き抜いて試していれば気づけた。
# 仕掛けたら想定している障害を自分で起こして、復帰まで見届ける。
#
# 【何をするか】
#   1. 次回起動時に fsck を強制する状態にする
#   2. sysrq で同期せずに即再起動する (電源を引き抜いたのと同じ状態)
#   3. Pi が戻ってきてメーターが動くまで待つ
#   4. これを指定回数くり返す
#
# 使い方: ./verify-boot-resilience.sh <PiのIP> [回数]

set -uo pipefail

HOST="${1:?PiのIPを指定すること}"
ROUNDS="${2:-3}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no"
USER=laurel

note() { echo "[verify] $*"; }
fail() { echo "[verify] 失敗: $*" >&2; exit 1; }

wait_for_pi() {
    local limit=${1:-180} waited=0
    while [ $waited -lt "$limit" ]; do
        if $SSH "$USER@$HOST" true 2>/dev/null; then return 0; fi
        sleep 5; waited=$((waited+5))
        printf '.'
    done
    echo
    return 1
}

$SSH "$USER@$HOST" true 2>/dev/null || fail "Pi に SSH できない"

note "設定の確認"
$SSH "$USER@$HOST" '
  echo "  cmdline: $(grep -o "fsck[^ ]*" /boot/firmware/cmdline.txt 2>/dev/null || grep -o "fsck[^ ]*" /boot/cmdline.txt)"
  echo "  fsck成功扱い: $(systemctl show systemd-fsck-root -p SuccessExitStatus --value)"
  echo "  journald: $(systemctl show systemd-journald -p Environment --value | head -c 40)$(grep -h Storage /etc/systemd/journald.conf.d/*.conf 2>/dev/null)"
  echo "  swap: $(swapon --show=NAME --noheadings | wc -l) 個有効"
' || fail "設定を読めない"

for i in $(seq 1 "$ROUNDS"); do
    note ""
    note "===== $i / $ROUNDS 回目 ====="

    # 起動回数カウンタを上げて次回 fsck を強制する。
    # 実際の破損を作るのは危険なので、fsck が走る状況を再現する。
    $SSH "$USER@$HOST" 'sudo tune2fs -C 60 $(findmnt -no SOURCE /) >/dev/null 2>&1 || true'
    note "次回起動で fsck が走るようにした"

    before=$($SSH "$USER@$HOST" 'cat /proc/uptime | cut -d. -f1')
    note "現在の稼働秒数: $before"

    # 同期せずに即再起動 = 電源を引き抜いたのと同じ。
    # これが車でエンジンを切ったときに起きていること。
    note "電源断を再現する (sysrq: 同期なし即再起動)"
    $SSH "$USER@$HOST" 'sudo sh -c "echo 1 > /proc/sys/kernel/sysrq; echo b > /proc/sysrq-trigger"' 2>/dev/null || true

    sleep 10
    printf '[verify] 復帰を待つ'
    if ! wait_for_pi 240; then
        fail "$i 回目で Pi が戻ってこなかった。手動で確認すること"
    fi
    echo
    note "SSH 復帰"

    after=$($SSH "$USER@$HOST" 'cat /proc/uptime | cut -d. -f1')
    [ "$after" -lt "$before" ] || fail "再起動していない (稼働秒数 $before → $after)"
    note "再起動を確認 (稼働秒数 $after)"

    # メーターが上がるまで待つ
    ok=0
    for _ in $(seq 1 24); do
        if $SSH "$USER@$HOST" 'systemctl is-active --quiet pi-obd-meter' 2>/dev/null; then ok=1; break; fi
        sleep 5
    done
    [ $ok -eq 1 ] || fail "$i 回目でメーターが起動しなかった"
    note "メーター起動を確認"

    $SSH "$USER@$HOST" '
      echo "  fsck の結果: $(systemctl is-active systemd-fsck-root 2>/dev/null) / $(systemctl show systemd-fsck-root -p ExecMainStatus --value 2>/dev/null)"
      echo "  今回の起動での異常: $(journalctl -b -p err --no-pager -q 2>/dev/null | wc -l) 件"
    '
done

note ""
note "===== $ROUNDS 回すべて復帰した ====="
note "不正電断で起動不能にならないことを実測で確認した"
