#!/bin/bash
# 起動直後の WiFi 接続を早める。
#
# 【なぜ要るか】
# 内蔵WiFi (BCM4345/6) は起動直後、association を約0.4秒で失敗することがある。
#   CTRL-EVENT-ASSOC-REJECT bssid=00:00:00:00:00:00 status_code=16
# BSSID が全ゼロ = 電波のやり取り前にファームの join が即エラーを返している。
# 1回あたり2割ほどで通るので、撃ち直せば繋がる。実測 (2026-09-07):
#   失敗そのもの  0.40秒 × 23回 =  2秒
#   再試行の間隔  6.9秒  × 23回 = 43秒   ← 95%が待ち時間
#
# 待ちが長い理由は2つ。
#   1. 毎回スキャンし直す (使えない5GHzを4チャネル叩いて -52 で転ぶ)
#   2. 失敗が積むと wpa_supplicant が自分で禁止時間を伸ばす
#      CTRL-EVENT-SSID-TEMP-DISABLED duration=10 → 20 → 40...
# つまり「繋ぎに行くほど遅くなる」。2 を毎回解除して撃ち直すのがこのスクリプト。
#
# 【安全性】
# 設定を一切変更しない。接続を試すだけなので、繋がらなくなる方向には動かない。
# 接続できたら即終了。上限時間で必ず止まる。
set -u
GW=${GW:-192.168.179.1}
CON=${CON:-Laurel-Wi-Fi}
IFACE=${IFACE:-wlan0}
DEADLINE=$((SECONDS + ${MAX_SEC:-120}))
INTERVAL=${INTERVAL:-2}
LOG=/var/log/wifi-watchdog.log
log(){ echo "$(date '+%F %T') [fast-connect] $*" >> "$LOG"; }

# 疎通確認は2段構え。
#  linked(): wpa_supplicant の状態を読むだけ。ネットワークを介さないので即答。
#            ループ内はこちらを使う (ping だと1回2秒かかり、撃つより待つ時間が長くなる)
#  up():     最終確認。実際にゲートウェイまで届くか。
CTRL=""
for P in /run/wpa_supplicant /var/run/wpa_supplicant; do
  [ -d "$P" ] && CTRL="$P" && break
done
wcli(){ [ -n "$CTRL" ] && wpa_cli -p "$CTRL" -i "$IFACE" "$@" 2>/dev/null; }
linked(){ wcli status 2>/dev/null | grep -q '^wpa_state=COMPLETED'; }
up(){ ping -c1 -W1 "$GW" >/dev/null 2>&1; }

if up; then exit 0; fi
log "開始"

n=0
while [ $SECONDS -lt $DEADLINE ]; do
  n=$((n+1))
  # 最初の3回は1周の所要時間を残す。次の起動で実測値が取れる。
  [ $n -le 3 ] && log "  試行$n 開始 (経過${SECONDS}秒)"
  # 1. wpa_supplicant が自分で掛けた一時禁止を外す。
  #    これをしないと失敗が積むほど 10→20→40秒と待たされる。
  wcli enable_network 0 >/dev/null
  # 2. スキャンせずにその場で撃ち直す。association 自体は 0.4 秒で決着する。
  wcli reassociate >/dev/null
  # 3. 0.3秒刻みで最大2.1秒、状態だけを見る (ping は使わない)
  for _ in 1 2 3 4 5 6 7; do
    sleep 0.3
    if linked; then
      up && { log "接続 (${n}回目 / ${SECONDS}秒)"; exit 0; }
    fi
  done
  # 4. 5回に1回は NetworkManager 経由でも撃つ (プロファイル再適用の保険)
  if [ $((n % 5)) -eq 0 ]; then
    nmcli --wait 2 con up "$CON" >/dev/null 2>&1
    up && { log "接続 (nmcli / ${n}回目 / ${SECONDS}秒)"; exit 0; }
  fi
done
log "上限まで繋がらず (${n}回試行)。watchdog に任せる"
exit 0
