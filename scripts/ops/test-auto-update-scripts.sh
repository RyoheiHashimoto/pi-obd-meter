#!/usr/bin/env bash
# auto-update.sh の scripts 配布をテストする。
#
# 見るのは1点だけ。「再起動で配布先が消えても、次の起動でネットワーク無しに
# 元へ戻せるか」。これが出来ていなかったために、2026-09-06 以降の ops
# スクリプトの修正が実機へ1つも届いていなかった (#191)。
#
# 実行: bash scripts/ops/test-auto-update-scripts.sh
set -euo pipefail

HERE=$(cd "$(dirname "$0")/../.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

fail=0
ok()   { echo "  ok   $*"; }
ng()   { echo "  NG   $*"; fail=1; }
check_file() { # path expected_content label
    if [ ! -f "$1" ]; then ng "$3: ファイルが無い ($1)"; return; fi
    if [ "$(cat "$1")" != "$2" ]; then ng "$3: 中身が違う ($(cat "$1") != $2)"; return; fi
    ok "$3"
}

# systemctl の代わり。呼ばれても何もしない。
mkdir -p "$T/bin"
cat > "$T/bin/systemctl" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$T/bin/systemctl"
PATH="$T/bin:$PATH"

# 配布元となる tar 展開済みディレクトリを作る
mkdir -p "$T/pkg/scripts/ops/systemd"
echo "NEW-drive-verify" > "$T/pkg/scripts/ops/drive-verify.py"
echo "NEW-imu-log"      > "$T/pkg/scripts/ops/imu-log.py"
cat > "$T/pkg/scripts/ops/systemd/drive-verify.service" <<'EOF'
[Service]
ExecStart=/usr/bin/python3 /usr/local/bin/drive-verify.py
EOF
cat > "$T/pkg/scripts/ops/systemd/imu-log.service" <<'EOF'
[Service]
ExecStart=/usr/local/bin/imu-log.py
EOF

# 実機の層を模す。
#   $T/root  = overlayfs の上層。再起動で消える (/usr/local, /etc)
#   $T/data  = 永続層 (/data)
#   $T/opt   = SD 側 (/opt)。移行前のフォールバック確認に使う
export DEST="$T/opt/pi-obd-meter"
export APP_DIR="$T/data/app"
export STATE_DIR="$T/data/app"
export SCRIPTS_DIR="$T/data/app/scripts"
export INSTALL_ROOT="$T/root"
export SYSTEMD_DIR="$T/root/etc/systemd/system"
export LOCKFILE="$T/lock"
export AUTO_UPDATE_LIB=1

# shellcheck source=/dev/null
. "$HERE/scripts/auto-update.sh"

echo "1) 取得したものを保管して配る"
install_scripts "$T/pkg" > /dev/null
check_file "$T/root/usr/local/bin/drive-verify.py" "NEW-drive-verify" "drive-verify.py が配られた"
check_file "$T/root/usr/local/bin/imu-log.py"      "NEW-imu-log"      "imu-log.py が配られた (対応表に無かった4本の1つ)"
check_file "$SCRIPTS_DIR/ops/drive-verify.py"      "NEW-drive-verify" "永続層に保管された"

echo "2) 再起動を模す (上層を消す。永続層は残す)"
rm -rf "$T/root"
[ -f "$T/root/usr/local/bin/drive-verify.py" ] && ng "上層を消せていない"

echo "3) ネットワーク無しで配り直せる"
deploy_scripts > /dev/null
check_file "$T/root/usr/local/bin/drive-verify.py" "NEW-drive-verify" "再起動後に復元された"
check_file "$T/root/usr/local/bin/imu-log.py"      "NEW-imu-log"      "再起動後に復元された (imu-log)"
check_file "$T/root/etc/systemd/system/drive-verify.service" \
           "$(cat "$T/pkg/scripts/ops/systemd/drive-verify.service")" "ユニットも復元された"

echo "4) 保管先がまだ無い Pi では SD 側から配る (移行中のフォールバック)"
rm -rf "$T/root" "$SCRIPTS_DIR"
mkdir -p "$DEST/scripts/ops/systemd"
echo "SD-drive-verify" > "$DEST/scripts/ops/drive-verify.py"
cp "$T/pkg/scripts/ops/systemd/drive-verify.service" "$DEST/scripts/ops/systemd/"
deploy_scripts > /dev/null
check_file "$T/root/usr/local/bin/drive-verify.py" "SD-drive-verify" "保管先が空なら SD 側を使う"

echo "5) 保管先が新しければ SD 側より優先される"
rm -rf "$T/root"
install_scripts "$T/pkg" > /dev/null
check_file "$T/root/usr/local/bin/drive-verify.py" "NEW-drive-verify" "保管先が優先された"

if [ "$fail" -ne 0 ]; then
    echo "FAIL"
    exit 1
fi
echo "PASS"
