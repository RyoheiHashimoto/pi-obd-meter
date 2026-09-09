#!/bin/sh
# メーター本体を起動する。新しければ SSD 側、無ければ SD 側を使う。
#
# 【なぜ要るか】
# / は overlayfs で上層が tmpfs のため、/opt への書き込みは再起動で消える。
# OTA (auto-update.sh) の設置先を /data (SSD) に移したので、実行時にどちらを
# 使うかを決める必要がある。
#
# 【なぜ /opt を /data へのリンクにしないか】
# メーターの起動を SSD のマウントに依存させないため。ドングルの件で /data は
# 何度も落ちており、そのときメーターが動き続けたのはバイナリが SD にあった
# から。リンクにすると SSD が落ちた瞬間にメーターごと止まる。
# ここでフォールバックしておけば、SSD が無くても古い版で動き続ける。
#
# このスクリプト自身は /opt (SD側) に置く。make deploy が overlayroot-chroot
# 経由で書くので永続する。OTA では入れ替えない。
set -u

SD_BIN=/opt/pi-obd-meter/pi-obd-meter
SSD_BIN=/data/pi-obd-meter/app/pi-obd-meter
CONFIG=/opt/pi-obd-meter/configs/config.json

# 壊れたダウンロードを掴まないよう、実行可能かつ 1MB 以上を条件にする。
# (正常なバイナリは 12MB 程度。切り詰められたものを弾く)
usable() {
    [ -x "$1" ] || return 1
    sz=$(stat -c %s "$1" 2>/dev/null || echo 0)
    [ "$sz" -ge 1048576 ]
}

if usable "$SSD_BIN"; then
    BIN=$SSD_BIN
    echo "起動: $BIN (SSD側、OTA適用済み)"
else
    BIN=$SD_BIN
    if [ -e "$SSD_BIN" ]; then
        echo "警告: $SSD_BIN が使えない。SD側で起動する"
    else
        echo "起動: $BIN (SD側。OTA未適用か SSD 未マウント)"
    fi
fi

exec "$BIN" -config "$CONFIG"
