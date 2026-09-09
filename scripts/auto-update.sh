#!/bin/bash
# Pi OBD Meter 自動更新スクリプト
# systemd timer (auto-update.timer) から2分間隔で実行される
# 1. Stable release (GitHub Releases latest) をチェック
# 2. Dev build (dev-latest pre-release) をチェック

set -euo pipefail

REPO="RyoheiHashimoto/pi-obd-meter"
# 更新の設置先は SSD 側にする。
#
# 【なぜ /opt ではないか】
# / は overlayfs で、上層は tmpfs (/media/root-rw)。/opt への書き込みは RAM に
# 載るだけで再起動で消える。一方バージョン記録は /var/lib/pi-obd-meter
# (= /data へのリンク) にあり永続する。この食い違いのため、
#   更新は適用される → 再起動で消える → 記録だけ残るので再取得もされない
# という状態になっていた。overlayfs を入れた 2026-09-05 以降、OTA は一度も
# 生き残っていない (2026-09-09 に実機で確認)。
#
# 【なぜ /opt へのリンクにしないか】
# メーターの実行を SSD のマウントに依存させたくない。ドングルの件で /data は
# 何度も落ちており、そのときメーターが動き続けたのはバイナリが SD にあった
# ため。リンクにすると SSD が落ちた瞬間にメーターごと止まる。
#
# よって「SSD に置き、無ければ SD のものを使う」。切り替えは run.sh が行う。
DEST="/opt/pi-obd-meter"        # SD 側の基準 (make deploy が overlayroot-chroot 経由で書く)
APP_DIR="/data/pi-obd-meter/app"  # OTA の設置先 (永続)
# バージョン記録はバイナリと同じ層に置く。
#
# 別の層に置くと「記録はあるがバイナリは無い」状態が作れてしまい、
# 再取得もされなくなる。実際それが起きていた。APP_DIR と一緒に消え、
# 一緒に残る場所に置くこと。
STATE_DIR="/data/pi-obd-meter/app"
LOCKFILE="/tmp/pi-obd-meter-update.lock"
SERVICE="pi-obd-meter"

LOG_TAG="auto-update"

log() { echo "[$LOG_TAG] $*" | systemd-cat -t "$LOG_TAG" -p info; }
log_warn() { echo "[$LOG_TAG] $*" | systemd-cat -t "$LOG_TAG" -p warning; }

# --- ロック（多重実行防止） ---
exec 9>"$LOCKFILE"
if ! flock -n 9; then
    exit 0
fi

# --- ネットワーク確認 ---
#
# 起動直後は network-online.target に到達していてもインターネットに出られない。
# 実測 (2026-08-31):
#
#   00:06:16  Reached target network-online.target - Network is Online.
#   00:06:18  New interface create wlan1
#
# systemd が「オンライン」と宣言した2秒後に、ようやく WiFi インターフェースが
# 生成されている。そこから wpa_supplicant の認証と DHCP が走るので、
# OnBootSec=10sec で起動するこのスクリプトが動く時点では、まだ名前解決すら
# できないことがある。
#
# 従来はここで即 exit していた。周期実行は廃止済み (起動時1回のみ) なので、
# この1回を落とすと次の起動まで更新の機会が無い。実際に #153〜#157 の
# デプロイが2回連続で取りこぼされ、手動実行でしか反映できなかった。
#
# 最大 NET_WAIT_TRIES 回、NET_WAIT_SLEEP 秒おきに待つ。既定で最大2分。
# 到達できればすぐ抜けるので、通常の起動でコストは増えない。
NET_WAIT_TRIES="${NET_WAIT_TRIES:-24}"
NET_WAIT_SLEEP="${NET_WAIT_SLEEP:-5}"

net_ready=0
for i in $(seq 1 "$NET_WAIT_TRIES"); do
    if curl -sf --max-time 5 "https://api.github.com/zen" > /dev/null 2>&1; then
        net_ready=1
        if [ "$i" -gt 1 ]; then
            log "ネットワーク到達まで $(( (i - 1) * NET_WAIT_SLEEP ))秒待機した"
        fi
        break
    fi
    # 最後の試行のあとは待たない。待っても次が無い。
    #
    # set -e 下で `[ cond ] && cmd` を文末に置くと、条件が偽のときの
    # 終了コードが 1 になる。errexit の例外規則に救われる書き方だが、
    # 無人で起動するスクリプトで微妙な規則に頼らない。
    if [ "$i" -lt "$NET_WAIT_TRIES" ]; then
        sleep "$NET_WAIT_SLEEP"
    fi
done

if [ "$net_ready" -ne 1 ]; then
    log_warn "ネットワークに到達できないため更新を見送る ($(( NET_WAIT_TRIES * NET_WAIT_SLEEP ))秒待機)"
    exit 0
fi

mkdir -p "$STATE_DIR"

# --- scripts/ の更新 ---
#
# 実行中のシェルスクリプト自身を上書きすると、bash が続きを読み込む際に
# 壊れた内容を読む恐れがある。同一ファイルシステム上の一時ファイルへ書いて
# mv で差し替えれば、ディレクトリエントリだけが入れ替わり、実行中のプロセス
# は元の inode を読み続けるので安全。
install_scripts() {
    local src="$1/scripts"
    [ -d "$src" ] || return 0

    mkdir -p "${DEST}/scripts"
    local f rel dst
    while IFS= read -r f; do
        rel="${f#"$src"/}"
        dst="${DEST}/scripts/${rel}"
        mkdir -p "$(dirname "$dst")"
        if ! cmp -s "$f" "$dst"; then
            if cp "$f" "${dst}.new" && chmod +x "${dst}.new" && mv -f "${dst}.new" "$dst"; then
                log "scripts 更新: $rel"
            else
                rm -f "${dst}.new"
                log_warn "scripts 更新失敗: $rel"
            fi
        fi
    done < <(find "$src" -type f)

    # systemd ユニットを配る。scripts/ops/systemd/ に置いたものを入れる。
    # 新しいタイマーを足しても、次の更新で勝手に有効になる。
    local u name
    for u in "${DEST}/scripts/ops/systemd/"*.service "${DEST}/scripts/ops/systemd/"*.timer; do
        [ -f "$u" ] || continue
        name=$(basename "$u")
        if ! cmp -s "$u" "/etc/systemd/system/$name"; then
            if cp "$u" "/etc/systemd/system/${name}.new" && mv -f "/etc/systemd/system/${name}.new" "/etc/systemd/system/$name"; then
                log "ユニット更新: $name"
                systemctl daemon-reload
                case "$name" in *.timer) systemctl enable --now "$name" 2>/dev/null || true;; esac
            else
                rm -f "/etc/systemd/system/${name}.new"
                log_warn "ユニット更新失敗: $name"
            fi
        fi
    done

    # スクリプトの配布先は systemd ユニットの ExecStart から導く。
    #
    # 以前は "ops/drive-verify.py:drive-verify" のような対応表をここに
    # 書いていた。ユニットを足しても表に足し忘れると、そのスクリプトだけ
    # 永久に更新されない。2026-09-09 に実際そうなっており、can-verify.sh /
    # imu-log.py / wifi-watchdog.sh / log-retention.sh の4本が対象外だった。
    # 直した wifi-watchdog.sh が Pi に届かないことで発覚した (#191)。
    #
    # ExecStart を読めば「そのユニットがどこの何を起動するか」が分かる。
    # 表を二重に持たない。
    local unit_file uname_ execpath dst src
    for unit_file in "${DEST}/scripts/ops/systemd/"*.service; do
        [ -f "$unit_file" ] || continue
        uname_=$(basename "$unit_file" .service)
        # ExecStart=/usr/bin/python3 /usr/local/bin/foo.py のように
        # インタプリタが前置される形もあるので、/usr/local 配下の引数を拾う。
        execpath=$(awk -F= '/^ExecStart=/{print $2}' "$unit_file" \
                   | tr ' ' '\n' | grep '^/usr/local/' | head -1)
        [ -n "$execpath" ] || continue
        src="${DEST}/scripts/ops/$(basename "$execpath")"
        [ -f "$src" ] || continue
        cmp -s "$src" "$execpath" && continue
        mkdir -p "$(dirname "$execpath")"
        if cp "$src" "${execpath}.new" && chmod +x "${execpath}.new" \
           && mv -f "${execpath}.new" "$execpath"; then
            log "スクリプト更新: $execpath ($uname_)"
            # 失敗したまま再起動すると古いコードで止まるだけ損をするので、
            # 入れ替えが成功したときだけ再起動する。
            if systemctl is-enabled --quiet "$uname_" 2>/dev/null \
               && systemctl is-active --quiet "$uname_" 2>/dev/null; then
                systemctl restart "$uname_" 2>/dev/null || log_warn "$uname_ の再起動に失敗"
            fi
        else
            rm -f "${execpath}.new"
            log_warn "スクリプト更新失敗: $execpath"
        fi
    done

    # poll22 だけはユニットがリポジトリに無く Pi 側にしかないので個別に扱う。
    # ユニットを scripts/ops/systemd/ に移せばこの分岐は不要になる。
    src="${DEST}/scripts/ops/poll22-lean.py"
    dst=/usr/local/bin/poll22-lean.py
    if [ -f "$src" ] && ! cmp -s "$src" "$dst"; then
        if cp "$src" "${dst}.new" && chmod +x "${dst}.new" && mv -f "${dst}.new" "$dst"; then
            log "スクリプト更新: $dst (poll22)"
            systemctl is-active --quiet poll22 2>/dev/null && { systemctl restart poll22 2>/dev/null || true; }
        else
            rm -f "${dst}.new"
        fi
    fi
}

# --- Stable release チェック ---
check_stable() {
    local latest_json
    latest_json=$(curl -sf --max-time 10 "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null) || return 1

    local tag
    tag=$(echo "$latest_json" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
    [ -z "$tag" ] && return 1

    local current
    current=$(cat "$STATE_DIR/release-version" 2>/dev/null || echo "")

    if [ "$tag" = "$current" ]; then
        return 1
    fi

    log "新しいリリース検出: $tag (現在: ${current:-なし})"

    # ダウンロード URL（selfupdate 用アセット）
    local url="https://github.com/${REPO}/releases/download/${tag}/pi-obd-meter_linux_arm64.tar.gz"
    local tmpdir
    tmpdir=$(mktemp -d)

    if ! curl -fsSL --max-time 120 "$url" -o "${tmpdir}/release.tar.gz"; then
        log_warn "リリースダウンロード失敗: $url"
        rm -rf "$tmpdir"
        return 1
    fi

    tar xzf "${tmpdir}/release.tar.gz" -C "$tmpdir"

    # バックアップ（ロールバック用）
    mkdir -p "$APP_DIR"
    cp "${APP_DIR}/pi-obd-meter" "${APP_DIR}/pi-obd-meter.bak" 2>/dev/null || true

    # インストール
    systemctl stop "$SERVICE" 2>/dev/null || true
    cp "${tmpdir}/pi-obd-meter" "${APP_DIR}/pi-obd-meter"
    chmod +x "${APP_DIR}/pi-obd-meter"
    if [ -f "${tmpdir}/pi-obd-scanner" ]; then
        cp "${tmpdir}/pi-obd-scanner" "${APP_DIR}/pi-obd-scanner"
        chmod +x "${APP_DIR}/pi-obd-scanner"
    fi
    # web/static を更新 (stable release でも UI 差し替え)
    if [ -d "${tmpdir}/web/static" ]; then
        mkdir -p "${DEST}/web/static"
        cp -r "${tmpdir}/web/static/"* "${DEST}/web/static/"
    fi
    # scripts/ と systemd ユニットも更新する。
    #
    # 以前は check_dev だけが install_scripts を呼んでいた。stable release を
    # 入れると ops スクリプトが古いまま取り残される。v1.4.0 で実際に起きた。
    install_scripts "$tmpdir"
    systemctl start "$SERVICE"

    # ヘルスチェック（10秒以内にプロセスが生存しているか）
    sleep 10
    if ! systemctl is-active --quiet "$SERVICE"; then
        log_warn "リリース $tag 起動失敗、ロールバック"
        cp "${APP_DIR}/pi-obd-meter.bak" "${APP_DIR}/pi-obd-meter"
        systemctl start "$SERVICE"
        rm -rf "$tmpdir"
        return 1
    fi

    systemctl restart kiosk 2>/dev/null || true

    rm -rf "$tmpdir"
    echo "$tag" > "$STATE_DIR/release-version"
    log "リリース $tag インストール完了"
    return 0
}

# --- Dev build チェック ---
check_dev() {
    local dev_json
    dev_json=$(curl -sf --max-time 10 "https://api.github.com/repos/${REPO}/releases/tags/dev-latest" 2>/dev/null) || return 1

    local published
    published=$(echo "$dev_json" | grep '"published_at"' | head -1 | cut -d'"' -f4)
    [ -z "$published" ] && return 1

    local stored
    stored=$(cat "$STATE_DIR/dev-version" 2>/dev/null || echo "")

    if [ "$published" = "$stored" ]; then
        return 1
    fi

    log "新しい dev ビルド検出: $published (前回: ${stored:-なし})"

    # ダウンロード URL
    local asset_url
    asset_url=$(echo "$dev_json" | grep '"browser_download_url"' | head -1 | cut -d'"' -f4)
    [ -z "$asset_url" ] && return 1

    local tmpdir
    tmpdir=$(mktemp -d)

    if ! curl -fsSL --max-time 120 "$asset_url" -o "${tmpdir}/dev.tar.gz"; then
        log_warn "dev ビルドダウンロード失敗"
        rm -rf "$tmpdir"
        return 1
    fi

    tar xzf "${tmpdir}/dev.tar.gz" -C "$tmpdir"

    # バックアップ（ロールバック用）
    mkdir -p "$APP_DIR"
    cp "${APP_DIR}/pi-obd-meter" "${APP_DIR}/pi-obd-meter.bak" 2>/dev/null || true

    # インストール
    systemctl stop "$SERVICE" 2>/dev/null || true
    cp "${tmpdir}/pi-obd-meter" "${APP_DIR}/pi-obd-meter"
    chmod +x "${APP_DIR}/pi-obd-meter"
    if [ -f "${tmpdir}/pi-obd-scanner" ]; then
        cp "${tmpdir}/pi-obd-scanner" "${APP_DIR}/pi-obd-scanner"
        chmod +x "${APP_DIR}/pi-obd-scanner"
    fi
    # scripts/ を更新する。
    #
    # バイナリだけが自動更新され scripts/ が手動だと、リポジトリを直しても
    # Pi 上は古いまま動き続ける。実際 drive-verify.py は修正後も手で配る
    # まで古いままだった。auto-update.sh 自身もここで更新される。
    install_scripts "$tmpdir"

    # web/static を更新（開発用ファイルシステム配信）
    if [ -d "${tmpdir}/web/static" ]; then
        mkdir -p "${DEST}/web/static"
        cp -r "${tmpdir}/web/static/"* "${DEST}/web/static/"
    fi
    systemctl start "$SERVICE"

    # ヘルスチェック（10秒以内にプロセスが生存しているか）
    sleep 10
    if ! systemctl is-active --quiet "$SERVICE"; then
        log_warn "dev ビルド起動失敗、ロールバック: $published"
        cp "${APP_DIR}/pi-obd-meter.bak" "${APP_DIR}/pi-obd-meter"
        systemctl start "$SERVICE"
        rm -rf "$tmpdir"

        # 失敗したバージョンも記録する。
        #
        # 記録しないと dev-version が古いままになり、2分後のタイマーで
        # 同じビルドを「新しい」と判定して再度ダウンロードし、また失敗する。
        # 12MB のダウンロードとメーターの10秒停止が延々と繰り返され、
        # 走行記録に穴が空き、SD への書き込みも増え続ける。
        #
        # 実際 2026-08-28 に GET /api/health の二重登録で起動できない
        # ビルドを出してしまい、この筋を踏みかけた。
        #
        # 同じビルドは二度と試さない。次の新しいビルドが出れば自動で試す。
        echo "$published" > "$STATE_DIR/dev-version"
        log_warn "このビルドは再試行しない。次のビルドを待つ"
        return 1
    fi

    systemctl restart kiosk 2>/dev/null || true

    rm -rf "$tmpdir"
    echo "$published" > "$STATE_DIR/dev-version"
    log "dev ビルドインストール完了"
    return 0
}

# --- メイン ---
# Stable release が優先（新しいリリースがあればそちらをインストール）
if check_stable; then
    exit 0
fi

# Stable に更新がなければ dev をチェック
check_dev || true
