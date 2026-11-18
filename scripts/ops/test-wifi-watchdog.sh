#!/usr/bin/env bash
# wifi-watchdog.sh をテストする。
#
# ping / nmcli / iw / dmesg を偽物に差し替え、毎分の周期を何回か回して、
# 繋ぎ直し (`nmcli con up`) を呼ぶかどうかを確かめる。
# 2026-11-15 の状態 (関連付けは生きていて ping だけが落ちる) も入れてある。
#
# 実行: bash scripts/ops/test-wifi-watchdog.sh
set -euo pipefail
HERE=$(cd "$(dirname "$0")/../.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
ok() { echo "  ok   $*"; }
ng() { echo "  NG   $*"; fail=1; }

mkdir -p "$T/bin"

# 偽の ping / nmcli / iw / dmesg。振る舞いは環境変数とファイルで決める。
# FAKE_PING=ok|ng、FAKE_ASSOC=yes|no。nmcli con up は呼ばれた回数を数える。
cat > "$T/bin/ping" <<'EOF'
#!/bin/bash
[ "$FAKE_PING" = "ok" ]
EOF
cat > "$T/bin/iw" <<'EOF'
#!/bin/bash
if [ "$FAKE_ASSOC" = "yes" ]; then
  echo "Connected to 1c:7c:98:b5:46:91 (on wlan0)"
  echo "	signal: -40 dBm"
else
  echo "Not connected."
fi
EOF
cat > "$T/bin/nmcli" <<'EOF'
#!/bin/bash
case "$*" in
  *"con show"*) printf 'Laurel-Wi-Fi\n' ;;
  *"con up"*) echo "$*" >> "$FAKE_LOG"; exit 1 ;;
esac
exit 0
EOF
printf '#!/bin/bash\nexit 0\n' > "$T/bin/dmesg"
chmod +x "$T/bin/"*

export PATH="$T/bin:$PATH" GW=192.0.2.1 FAKE_LOG="$T/conup.log"

# 1 周期を回す。起動 (BOOT_ID) ごとに記録の置き場を分ける
run() { BOOT_ID="$1" REC_DIR="$T/rec" FAKE_PING="$2" FAKE_ASSOC="$3" \
  bash "$HERE/scripts/ops/wifi-watchdog.sh" > "$T/out" 2>&1; }
conups() { [ -f "$FAKE_LOG" ] && wc -l < "$FAKE_LOG" | tr -d ' ' || echo 0; }
reset() { rm -f "$FAKE_LOG"; }

echo "== 繋がっている"
reset; run b1 ok yes
[ "$(conups)" = 0 ] && ok "繋ぎ直さない" || ng "繋ぎ直した"
[ -e "$T/rec/b1.ok" ] && ok "接続を記録した" || ng "接続を記録しなかった"

echo "== 関連付けは生きていて ping だけ落ちる (2026-11-15)"
reset; run b2 ng yes
[ "$(conups)" = 0 ] && ok "1 周期目は切らない" || ng "1 周期目で繋ぎ直した"
grep -q '切らずに待つ (1/3)' "$T/out" && ok "待っていると記録した" || ng "記録が無い: $(cat "$T/out")"
run b2 ng yes
[ "$(conups)" = 0 ] && ok "2 周期目も切らない" || ng "2 周期目で繋ぎ直した"
run b2 ng yes
[ "$(conups)" = 1 ] && ok "3 周期続いたら繋ぎ直す" || ng "3 周期目に繋ぎ直さなかった (con up $(conups) 回)"
[ ! -e "$T/rec/b2.held" ] && ok "繋ぎ直したら数え直す" || ng ".held が残った"

echo "== 途中で ping が戻ったら数え直す"
reset; run b3 ng yes; run b3 ng yes; run b3 ok yes
[ ! -e "$T/rec/b3.held" ] && ok "繋がったら .held を消す" || ng ".held が残った"
run b3 ng yes
[ "$(conups)" = 0 ] && ok "戻った後の 1 回の不達では切らない" || ng "前の回数を引き継いで繋ぎ直した"

echo "== 関連付けが無い (起動直後・切れた後)"
reset; run b4 ng no
[ "$(conups)" = 1 ] && ok "すぐに繋ぎ直す (従来どおり)" || ng "繋ぎ直さなかった"

echo "== 壊れた .held (電断で空になった)"
reset; : > "$T/rec/b5.held"; run b5 ng yes
[ "$(conups)" = 0 ] && ok "0 として扱い、切らない" || ng "繋ぎ直した"
[ "$(cat "$T/rec/b5.held")" = 1 ] && ok "1 から数え直した" || ng ".held = $(cat "$T/rec/b5.held")"

echo
[ $fail = 0 ] && echo "すべて合格" || { echo "失敗あり"; exit 1; }
