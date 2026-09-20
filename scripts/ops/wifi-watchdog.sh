#!/bin/bash
# 内蔵WiFi/ドングルが切れていたら繋ぎ直す。NetworkManagerが諦めた後の保険。
#
# 重要: pi-obd-meter は CAN 接続のたびに `ip link set can0 down/up` を実行する。
# （この監視が無かったため 2026-08-18 の走行データを取り逃した）
# テストから差し替えられるようにしておく。本番では既定値のまま。
# 差し替えられないと、到達する側・しない側の両方の経路を一度も動かさずに
# 実機へ配ることになる。
GW=${GW:-192.168.179.1}

# journal に出す。以前は /var/log/wifi-watchdog.log に書いていたが、
# root は overlayfs の tmpfs で、このパスは /data にリンクされていない。
# つまり再起動のたびに消えていた。起動時の association 失敗 (#184) を
# 追っているのに、記録が起動の境界で毎回失われていた。
#
# 自前の日時も付けない。Pi に RTC が無く、WiFi が繋がって NTP が効くまで
# 壁時計が当てにならない。journald は単調時間も持っているので
# `journalctl -u wifi-watchdog -o short-monotonic` で正しい順序が読める。
#
# 永続化には journald が Storage=persistent であることが要る
# (harden-boot.sh で設定。2026-09-09 に volatile から変更した)。
log(){ echo "$*"; }

# --- 起動ごとの結果を貯める (#184) ---
#
# 対症療法は実測ですべて否定されている。無線の off/on (8回試して復旧例ゼロ)、
# 撃ち直しの高速化 (試行回数は増えない)、ルーターの混在モード、roamoff /
# feature_disable (適用済みで効果なし)、信号強度・BSSID固定・省電力・CPU負荷。
# ファームが join に status 1 を返すところまでは掴めていて、そこから先は
# ドライバ/ファーム側にある。
#
# 残っている打ち手は「成功する boot と失敗する boot の差を探す」こと。
# 同じファームでも繋がる起動と繋がらない起動がある以上、差はどこかにある。
# 手で集めるのは続かないので、毎分動いているこの watchdog に貯めさせる。
#
# 置き先は /data (永続)。/var/log は overlayfs の tmpfs で再起動のたびに
# 消えるため、起動の境界を跨ぐ記録には使えない (log(){} のコメント参照)。
REC_DIR=${REC_DIR:-/data/log/wifi-boot}
# boot_id は起動ごとに変わるので、これで起動を区切る。
# Pi では必ず読めるが、読めない環境 (テスト用の Mac 等) では unknown に落ちる。
# その場合 unknown.ok が残り続けて以後の起動が記録されなくなるので、
# GW と同じくテストから差し替えられるようにしておく。
# リダイレクトは左から右に評価されるので、2>/dev/null を < より先に置く。
# 逆順だと、ファイルが無い環境でシェル自身のオープン失敗メッセージが漏れる。
BOOT_ID=${BOOT_ID:-$(tr -d '-' 2>/dev/null < /proc/sys/kernel/random/boot_id || echo unknown)}
REC="$REC_DIR/$BOOT_ID"
UP=$(cut -d. -f1 /proc/uptime 2>/dev/null || echo 0)
mkdir -p "$REC_DIR" 2>/dev/null

# 試行回数は成功側 (.ok に何回目で繋がったかを書く) と失敗側 (数えて書き戻す)
# の両方で使うので、分岐の手前で一度だけ読む。
# cat は空ファイルでも終了コード 0 を返すので `|| echo 0` では拾えない。
# 電断で中身を失った .tries を読むと、成功側は tries= と空欄になり、
# 失敗側は $(( + 1 )) の算術式エラーでこの先の復旧処理ごと落ちる。
# 数字でなければ 0 に倒す。
TRIES=$(cat "${REC}.tries" 2>/dev/null)
case "$TRIES" in '' | *[!0-9]*) TRIES=0 ;; esac

if ping -c1 -W2 $GW >/dev/null 2>&1; then
  # 繋がった。最初の1回だけ書く。以後の周期では何もしない。
  if [ ! -e "${REC}.ok" ]; then
    printf 'up=%s tries=%s iface=%s rssi=%s\n' \
      "$UP" \
      "$TRIES" \
      "$(nmcli -t -f DEVICE,STATE dev 2>/dev/null | grep ':connected$' | head -1 | cut -d: -f1)" \
      "$(iw dev wlan0 link 2>/dev/null | awk '/signal:/{print $2}')" \
      > "${REC}.ok" 2>/dev/null
    log "接続を記録: $(cat "${REC}.ok" 2>/dev/null)"
    # 古い起動の記録を間引く。1起動あたり最大3ファイル (.ok/.tries/.dmesg)
    # なので 900 個残す = 300 起動ぶん (1日10起動として1か月)。
    # 1ファイル数十バイトなので容量は問題にならない。消せなくても失敗させない。
    ls -t "$REC_DIR" 2>/dev/null | tail -n +901 | while read -r f; do
      rm -f "$REC_DIR/$f" 2>/dev/null
    done
  fi
  exit 0
fi

# 繋がっていない。何回目か数え、ドライバが何を言ったかを残す。
# brcmfmac の行は上書きで良い (最後の状態だけ分かればよい)。
echo $((TRIES + 1)) > "${REC}.tries" 2>/dev/null
dmesg 2>/dev/null | grep -i brcmf | tail -5 > "${REC}.dmesg" 2>/dev/null

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
