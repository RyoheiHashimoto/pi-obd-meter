#!/usr/bin/env bash
# persist-check.sh をテストする。
#
# journalctl と findmnt を偽物に差し替え、Pi で起きうる状態を1つずつ作って、
# 外れを外れとして記録するか (と、正常を正常とするか) を確かめる。
# 2026-10-02 の実際の状態 (上限 64M・前回の記録が 0 行) も入れてある。
#
# 実行: bash scripts/ops/test-persist-check.sh
set -euo pipefail
HERE=$(cd "$(dirname "$0")/../.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
ok() { echo "  ok   $*"; }
ng() { echo "  NG   $*"; fail=1; }

mkdir -p "$T/bin"
echo "boot-xyz" > "$T/boot_id"

# 偽の journalctl / findmnt。振る舞いは環境変数で決める。
cat > "$T/bin/journalctl" <<'EOF'
#!/bin/bash
case "$*" in
  *-t\ systemd-journald*)
    echo "systemd-journald[311]: System Journal (/var/log/journal/abc) is 64M, max ${FAKE_MAX}, 447.9M free." ;;
  *--list-boots*)
    for i in $(seq 1 "$FAKE_BOOTS"); do echo "$i boot"; done ;;
  *-b\ -1*_PID=1*)
    [ "$FAKE_PREV" = "yes" ] && echo "systemd[1]: Started something." ;;
esac
exit 0
EOF
cat > "$T/bin/findmnt" <<'EOF'
#!/bin/bash
case "$*" in
  *FSTYPE*/var/log/journal*) echo "$FAKE_JOURNAL_FS" ;;
  *FSTYPE*timesync*) echo "ext4" ;;
  *OPTIONS*) echo "$FAKE_LOWER_OPTS" ;;
  *) exit 0 ;;
esac
EOF
chmod +x "$T/bin/journalctl" "$T/bin/findmnt"

# run <名前> <期待の終了コード> <期待の ok> <failures に含まれるべき文字列 (空なら0件)>
run() {
    local name=$1 want_rc=$2 want_ok=$3 want_fail=$4 rc=0
    PATH="$T/bin:$PATH" OUT="$T/out.json" BOOT_ID_FILE="$T/boot_id" LOWER=/media/root-ro \
        bash "$HERE/scripts/ops/persist-check.sh" > "$T/log" 2>&1 || rc=$?
    local json; json=$(cat "$T/out.json" 2>/dev/null || echo "")
    if [ "$rc" != "$want_rc" ]; then ng "$name: 終了コード $rc (期待 $want_rc)"; cat "$T/log"; return; fi
    if ! echo "$json" | grep -q "\"ok\":$want_ok"; then ng "$name: ok が $want_ok でない: $json"; return; fi
    if ! echo "$json" | grep -q '"boot_id":"boot-xyz"'; then ng "$name: boot_id が無い: $json"; return; fi
    if [ -z "$want_fail" ]; then
        if ! echo "$json" | grep -q '"failures":\[\]'; then ng "$name: 外れが無いはずが: $json"; return; fi
    elif ! echo "$json" | grep -qF "$want_fail"; then
        ng "$name: 「$want_fail」が記録されていない: $json"; return
    fi
    ok "$name"
}

echo "persist-check.sh"
export FAKE_MAX=512M FAKE_BOOTS=3 FAKE_PREV=yes FAKE_JOURNAL_FS=ext4 FAKE_LOWER_OPTS=ro,relatime
run "すべて正常" 0 true ""

FAKE_MAX=64M run "上限が 64M に戻っている (2026-10-02 の状態)" 1 false "journal の上限が 64M"
FAKE_PREV=no run "前回の起動のシステム記録が無い (2026-10-02 の状態)" 1 false "前回の起動のシステム記録が残っていない"
FAKE_BOOTS=1 FAKE_PREV=no run "初回起動なら前回分は問わない" 0 true ""
FAKE_JOURNAL_FS=overlay run "journal が overlay の上 (再起動で消える)" 1 false "/var/log/journal が overlay の上にある"
FAKE_LOWER_OPTS=rw,relatime run "SD の下層が rw のまま" 1 false "/media/root-ro が rw のまま"

[ $fail -eq 0 ] && echo "すべて合格" || { echo "失敗あり"; exit 1; }
