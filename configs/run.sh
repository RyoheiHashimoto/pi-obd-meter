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
# このスクリプト自身は /opt/pi-obd-meter/configs/ に置く (deploy が configs/ を
# そこへ rsync し、overlayroot-chroot 経由で下層にも書くので永続する)。
# cog-kiosk.sh など実行スクリプトが同居しているのと同じ扱い。OTA では触らない。
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

# 【新しい方を使う。SSD 側を無条件に優先してはいけない】
#
# 初版は usable なら常に SSD 側を選んでいた。そのため OTA が一度でも走ると
# make deploy で /opt に置いたバイナリが永久に使われなくなる。デプロイした
# のに反映されない、という分かりにくい壊れ方をする。2026-09-09 に別セッション
# が実機で踏んで報告してきた (deploy 直後でも SSD 側の古い md5 で起動していた)。
#
# mtime で新しい方を選ぶ。OTA も deploy も書いた時刻が入るので、
# 「最後に入れた方が動く」という素直な意味になる。
if usable "$SSD_BIN" && [ "$SSD_BIN" -nt "$SD_BIN" ]; then
    BIN=$SSD_BIN
    echo "起動: $BIN (SSD側。OTA が deploy より新しい)"
elif usable "$SSD_BIN"; then
    BIN=$SD_BIN
    echo "起動: $BIN (SD側。deploy が OTA より新しい)"
else
    BIN=$SD_BIN
    if [ -e "$SSD_BIN" ]; then
        echo "警告: $SSD_BIN が使えない。SD側で起動する"
    else
        echo "起動: $BIN (SD側。OTA未適用か SSD 未マウント)"
    fi
fi

exec "$BIN" -config "$CONFIG"
