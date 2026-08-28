#!/bin/bash
# Pi OBD Meter 自動更新スクリプト
# systemd timer (auto-update.timer) から2分間隔で実行される
# 1. Stable release (GitHub Releases latest) をチェック
# 2. Dev build (dev-latest pre-release) をチェック

set -euo pipefail

REPO="RyoheiHashimoto/pi-obd-meter"
DEST="/opt/pi-obd-meter"
STATE_DIR="/var/lib/pi-obd-meter"
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
if ! curl -sf --max-time 5 "https://api.github.com/zen" > /dev/null 2>&1; then
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

    # ロガーは /usr/local/bin から起動しているので、変わっていれば入れ替えて
    # サービスを再起動する。再起動しないと古いコードのまま動き続ける。
    local pair name unit
    for pair in "ops/drive-verify.py:drive-verify" "ops/poll22-lean.py:poll22"; do
        name="${pair%%:*}"; unit="${pair##*:}"
        [ -f "${DEST}/scripts/${name}" ] || continue
        dst="/usr/local/bin/$(basename "$name")"
        if ! cmp -s "${DEST}/scripts/${name}" "$dst"; then
            # 失敗したまま再起動すると、古いコードのまま止まるだけ損をする。
            # 入れ替えが成功したときだけ再起動する。
            if cp "${DEST}/scripts/${name}" "${dst}.new" && chmod +x "${dst}.new" && mv -f "${dst}.new" "$dst"; then
                log "ロガー更新: $(basename "$name")"
                if systemctl is-enabled --quiet "$unit" 2>/dev/null; then
                    systemctl restart "$unit" 2>/dev/null || log_warn "$unit の再起動に失敗"
                fi
            else
                rm -f "${dst}.new"
                log_warn "ロガー更新失敗: $(basename "$name")"
            fi
        fi
    done
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
    cp "${DEST}/pi-obd-meter" "${DEST}/pi-obd-meter.bak" 2>/dev/null || true

    # インストール
    systemctl stop "$SERVICE" 2>/dev/null || true
    cp "${tmpdir}/pi-obd-meter" "${DEST}/pi-obd-meter"
    chmod +x "${DEST}/pi-obd-meter"
    if [ -f "${tmpdir}/pi-obd-scanner" ]; then
        cp "${tmpdir}/pi-obd-scanner" "${DEST}/pi-obd-scanner"
        chmod +x "${DEST}/pi-obd-scanner"
    fi
    # web/static を更新 (stable release でも UI 差し替え)
    if [ -d "${tmpdir}/web/static" ]; then
        mkdir -p "${DEST}/web/static"
        cp -r "${tmpdir}/web/static/"* "${DEST}/web/static/"
    fi
    systemctl start "$SERVICE"

    # ヘルスチェック（10秒以内にプロセスが生存しているか）
    sleep 10
    if ! systemctl is-active --quiet "$SERVICE"; then
        log_warn "リリース $tag 起動失敗、ロールバック"
        cp "${DEST}/pi-obd-meter.bak" "${DEST}/pi-obd-meter"
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
    cp "${DEST}/pi-obd-meter" "${DEST}/pi-obd-meter.bak" 2>/dev/null || true

    # インストール
    systemctl stop "$SERVICE" 2>/dev/null || true
    cp "${tmpdir}/pi-obd-meter" "${DEST}/pi-obd-meter"
    chmod +x "${DEST}/pi-obd-meter"
    if [ -f "${tmpdir}/pi-obd-scanner" ]; then
        cp "${tmpdir}/pi-obd-scanner" "${DEST}/pi-obd-scanner"
        chmod +x "${DEST}/pi-obd-scanner"
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
        cp "${DEST}/pi-obd-meter.bak" "${DEST}/pi-obd-meter"
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
