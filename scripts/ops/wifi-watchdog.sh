#!/bin/bash
# 内蔵WiFi/ドングルが切れていたら繋ぎ直す。NetworkManagerが諦めた後の保険。
#
# 重要: pi-obd-meter は CAN 接続のたびに `ip link set can0 down/up` を実行する。
# （この監視が無かったため 2026-08-18 の走行データを取り逃した）
LOG=/var/log/wifi-watchdog.log
GW=192.168.179.1
log(){ echo "$(date '+%F %T') $*" >> $LOG; }

# 既に到達できるなら何もしない
if ping -c1 -W2 $GW >/dev/null 2>&1; then exit 0; fi

log "ゲートウェイ不達。復旧を試みます"
nmcli radio wifi on 2>/dev/null

# 内蔵(wlan0)を先に試す。
#
# 従来はドングル(Laurel-Wi-Fi-usb)を先に試していたが、記録上ドングルで
# 復旧できた例は無く、成功した2回 (2026-09-06 00:42 / 02:48) はどちらも
# 内蔵だった。ドングルのドライバ (aic8800) は初期化に失敗することがあり
# (issue #183)、先に試すぶんだけ復旧が遅れる。
for C in Laurel-Wi-Fi Laurel-Wi-Fi-usb; do
  nmcli -t -f NAME con show 2>/dev/null | grep -qx "$C" || continue
  log "  $C を起動"
  nmcli con up "$C" >/dev/null 2>&1 && { sleep 5; ping -c1 -W2 $GW >/dev/null 2>&1 && { log "  → 復旧 ($C)"; exit 0; }; }
done

# ここで無線を切って入れ直すことはしない。
#
# 2026-09-05 の記録では 8 回リセットして、明確に復旧した例が 1 つも無い。
# 6 回は 3〜35 秒後にまた「不達」になり、復旧した 1 回はその 1 秒後に走った
# pmf 設定の自動巻き戻しで説明がつく。効果が無いうえ、NetworkManager が
# 接続を試みている最中に無線を切るので、復旧を妨げうる。次の周期に任せる。
log "  復旧できず。次の周期に再試行する"
exit 0
