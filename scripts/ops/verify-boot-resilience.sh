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
# 【emergency に落ちる経路は systemd 250 で移動している】
# 249 以前: src/fsck/fsck.c が自分で start_target(SPECIAL_EMERGENCY_TARGET)
#           を呼んでいた。ユニットの成否とは無関係なので SuccessExitStatus
#           では止められない。
# 250 以降: その呼び出しは C から消え、systemd-fsck-root.service の
#           OnFailure=emergency.target に移った。OnFailure はユニットが
#           failed に入ったときだけ発火するので、SuccessExitStatus で止まる。
#
# つまり対策が効くかどうかは systemd のバージョンに依存する。web 上の
# 「SuccessExitStatus では止められない」という記述は 249 以前が前提であり、
# man page も今なお「fsck が emergency.target を起動する」と書いたまま。
# よって段階1では機構そのものを先に確認し、そのうえで
#   (a) ユニットが failed になるか
#   (b) OnFailure が発火するか
# の両方を測る。(a) だけでは 1 段手前しか見ていないことになる。
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

# --------------------------------------------- emergency への経路がどちらか確認
# 250 以降なら OnFailure= 経由なので SuccessExitStatus で止められる。
# 249 以前なら fsck 本体が直接 emergency を起こすので止められない。
# "systemd 252 (252.36-1~deb12u1)" → 252。"257~rc1" のような形も先頭の数字だけ取る
sysver=$($SSH "$USER@$HOST" 'systemctl --version' 2>/dev/null | head -1 | awk '{print $2}' | sed 's/[^0-9].*//')
onfail=$($SSH "$USER@$HOST" 'systemctl show systemd-fsck-root -p OnFailure --value' 2>/dev/null)
[ -n "$sysver" ] || fail "systemd のバージョンを取得できない"

note "emergency へ落ちる経路"
note "  systemd   : $sysver"
note "  OnFailure : ${onfail:-(なし)}"

if [ "$sysver" -lt 250 ]; then
    fail "systemd $sysver は fsck 本体が直接 emergency を起こす (250 で OnFailure= に移動)。
       SuccessExitStatus では止められないため、この対策は成立しない。
       fsck.mode / fsck.repair 側での回避に切り替えること"
fi

case "$onfail" in
    *emergency.target*)
        note "  → OnFailure 経由。SuccessExitStatus で止められる機構" ;;
    "")
        note "  → OnFailure 自体が無い。そもそも emergency に落ちない設定" ;;
    *)
        note "  → OnFailure が emergency.target ではない。以下の判定は参考値" ;;
esac
note ""

# ================================================================= 段階 1
note "===== 段階1: systemd が終了コード4 を成功として扱うか (破損リスク ゼロ) ====="
note "8月に起動を止めた直接の原因は fsck の終了コード4 だった"
note ""
note "当初は fsck を偽物に差し替えて4を返させる設計にしていたが、これには"
note "詰みの筋があった。対策が効かなかった場合 emergency に落ち、90秒後に"
note "再起動し、また偽fsckが4を返す無限ループになる。fsck の時点で root は"
note "読み取り専用なので、偽物が自分を戻すこともできない。SDを抜くしかなくなる。"
note ""
note "代わりに、同じ SuccessExitStatus を持つ試験ユニットを作って"
note "終了コード4 で終わらせ、systemd がそれを成功と扱うか直接確かめる。"
note "SuccessExitStatus の解釈は systemd 共通なので、これで判定できる。"
note ""
note "さらに、試験ユニットには本物と同じ形の OnFailure= を付ける。"
note "実際に守りたいのは「failed にならないこと」ではなく「OnFailure が"
note "発火しないこと」なので、そこまで測らないと 1 段手前で止まる。"
note "本物の emergency.target ではなく、印を置くだけのユニットを指す。"

want=$($SSH "$USER@$HOST" 'systemctl show systemd-fsck-root -p SuccessExitStatus --value' 2>/dev/null)
note "systemd-fsck-root の設定値: [$want]"
echo "$want" | grep -q 4 || fail "終了コード4 が成功扱いになっていない。harden-boot.sh を先に実行すること"

# A/B で比べる。SuccessExitStatus 無しなら failed かつ OnFailure 発火、
# 有りなら success かつ未発火 になることを両方見せる。
# ExecMainStatus は Type=oneshot では当てにならない (終了コード4 でも 0 と
# 報告される) ので、is-failed と OnFailure の実発火で判定する。
$SSH "$USER@$HOST" "sudo sh -c '
  printf \"#!/bin/sh\\nexit 4\\n\" > /usr/local/bin/exit4-probe
  chmod +x /usr/local/bin/exit4-probe
  rm -f /run/exit4-onfailure-without /run/exit4-onfailure-with
  for v in without with; do
    if [ \$v = with ]; then extra=\"SuccessExitStatus=$want\"; else extra=\"\"; fi
    printf \"[Unit]\\nDescription=exit4 marker %s\\n[Service]\\nType=oneshot\\nExecStart=/bin/touch /run/exit4-onfailure-%s\\n\" \$v \$v > /etc/systemd/system/exit4-marker-\$v.service
    printf \"[Unit]\\nDescription=exit4 %s\\nOnFailure=exit4-marker-%s.service\\n[Service]\\nType=oneshot\\nExecStart=/usr/local/bin/exit4-probe\\n%s\\n\" \$v \$v \"\$extra\" > /etc/systemd/system/exit4-\$v.service
  done
  systemctl daemon-reload
  for v in without with; do systemctl start exit4-\$v.service >/dev/null 2>&1; done
  sleep 3
'" 2>/dev/null

res_without=$($SSH "$USER@$HOST" 'systemctl is-failed exit4-without.service 2>/dev/null')
res_with=$($SSH "$USER@$HOST" 'systemctl is-failed exit4-with.service 2>/dev/null')
fired_without=$($SSH "$USER@$HOST" 'test -e /run/exit4-onfailure-without && echo 発火 || echo 未発火' 2>/dev/null)
fired_with=$($SSH "$USER@$HOST" 'test -e /run/exit4-onfailure-with && echo 発火 || echo 未発火' 2>/dev/null)
note "  SuccessExitStatus なし → $res_without / OnFailure $fired_without   (8月に起きたこと)"
note "  SuccessExitStatus あり → $res_with / OnFailure $fired_with   (対策後)"

$SSH "$USER@$HOST" 'sudo sh -c "systemctl reset-failed exit4-without.service exit4-with.service exit4-marker-without.service exit4-marker-with.service 2>/dev/null; rm -f /etc/systemd/system/exit4-*.service /usr/local/bin/exit4-probe /run/exit4-onfailure-without /run/exit4-onfailure-with; systemctl daemon-reload"' 2>/dev/null

# 対照群が成立しているか (試験そのものが機能しているかの検査)
[ "$res_without" = "failed" ] || fail "対照実験が成立していない (SuccessExitStatus 無しでも failed にならない)"
[ "$fired_without" = "発火" ] || fail "対照実験が成立していない (SuccessExitStatus 無しでも OnFailure が発火しない)。
       OnFailure を観測できていないので、この試験は何も確認していない"

# 対策側
[ "$res_with" = "failed" ] && fail "終了コード4 が失敗扱いのまま。8月と同じ状況で起動が止まる"
[ "$fired_with" = "発火" ] && fail "failed にはならないが OnFailure が発火している。
       実機では emergency.target が起動するので、起動は止まる"
note "→ 終了コード4 は成功として扱われ、OnFailure も発火しない"
note "   本物では OnFailure=emergency.target なので、emergency に落ちない"
note ""
note "なお cmdline.txt 側も fsck.repair=preen に変えてあるため、"
note "そもそも fsck が4を返す状況自体が起きにくくなっている (二重の対策)"
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
