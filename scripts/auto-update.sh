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
    # web/static を更新（開発用ファイルシステム配信）
    if [ -d "${tmpdir}/web/static" ]; then
        mkdir -p "${DEST}/web/static"
        cp -r "${tmpdir}/web/static/"* "${DEST}/web/static/"
    fi
    systemctl start "$SERVICE"

    # ヘルスチェック（10秒以内にプロセスが生存しているか）
    sleep 10
    if ! systemctl is-active --quiet "$SERVICE"; then
        log_warn "dev ビルド起動失敗、ロールバック"
        cp "${DEST}/pi-obd-meter.bak" "${DEST}/pi-obd-meter"
        systemctl start "$SERVICE"
        rm -rf "$tmpdir"
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
