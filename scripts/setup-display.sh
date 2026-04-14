#!/bin/bash
# setup-display.sh
# Pi のブート設定を SDL + canvas メーター用に切り替える
# - /boot/firmware/cmdline.txt: カーネルメッセージ抑制、カーソル非表示
# - /boot/firmware/config.txt: ラズパイロゴ（レインボー）無効
# - kiosk.service / pi-obd-meter.service 無効化
# - pi-obd-meter-sdl.service 有効化
#
# Pi 上で 1 回だけ実行:
#   sudo bash /opt/pi-obd-meter/scripts/setup-display.sh
#
# その後再起動すると:
#   1. 真っ暗な画面 (1-2 秒)
#   2. メーター起動アニメ (2.8 秒)
#   3. 通常動作

set -e

if [[ "$EUID" -ne 0 ]]; then
    echo "エラー: root で実行してください (sudo bash $0)"
    exit 1
fi

CMDLINE=/boot/firmware/cmdline.txt
CONFIG=/boot/firmware/config.txt

echo "=== SDL 版メーター用ブート設定 ==="

# --- /boot/firmware/cmdline.txt ---
if [[ -f "$CMDLINE" ]]; then
    # バックアップ
    cp -n "$CMDLINE" "${CMDLINE}.bak.$(date +%s)" 2>/dev/null || true

    # 既存の設定を保持しつつ、必要なオプションを追加
    # cmdline.txt は 1 行構成
    LINE=$(cat "$CMDLINE")
    CHANGED=0

    add_opt() {
        local opt="$1"
        if ! echo "$LINE" | grep -qw "$opt"; then
            LINE="$LINE $opt"
            CHANGED=1
        fi
    }

    add_opt "quiet"
    add_opt "loglevel=3"
    add_opt "logo.nologo"
    add_opt "vt.global_cursor_default=0"
    add_opt "splash"
    add_opt "plymouth.ignore-serial-consoles"

    if [[ $CHANGED -eq 1 ]]; then
        echo "$LINE" > "$CMDLINE"
        echo "✓ $CMDLINE を更新"
    else
        echo "✓ $CMDLINE は既に設定済み"
    fi
else
    echo "⚠ $CMDLINE が見つかりません (Pi 以外の環境？)"
fi

# --- /boot/firmware/config.txt ---
if [[ -f "$CONFIG" ]]; then
    cp -n "$CONFIG" "${CONFIG}.bak.$(date +%s)" 2>/dev/null || true

    if ! grep -q "^disable_splash=1" "$CONFIG"; then
        echo "" >> "$CONFIG"
        echo "# SDL meter: disable rainbow splash" >> "$CONFIG"
        echo "disable_splash=1" >> "$CONFIG"
        echo "✓ $CONFIG に disable_splash=1 を追加"
    else
        echo "✓ $CONFIG は既に設定済み"
    fi
else
    echo "⚠ $CONFIG が見つかりません"
fi

# --- systemd サービス切り替え ---
echo ""
echo "=== systemd サービス切り替え ==="

# 既存サービスを停止・無効化
for svc in kiosk.service pi-obd-meter.service; do
    if systemctl is-enabled --quiet "$svc" 2>/dev/null; then
        systemctl disable "$svc"
        echo "✓ $svc 無効化"
    fi
    if systemctl is-active --quiet "$svc" 2>/dev/null; then
        systemctl stop "$svc"
        echo "✓ $svc 停止"
    fi
done

# 新サービスをコピー・有効化
SVC_SRC=/opt/pi-obd-meter/configs/pi-obd-meter-sdl.service
SVC_DST=/etc/systemd/system/pi-obd-meter-sdl.service

if [[ -f "$SVC_SRC" ]]; then
    cp "$SVC_SRC" "$SVC_DST"
    systemctl daemon-reload
    systemctl enable pi-obd-meter-sdl.service
    echo "✓ pi-obd-meter-sdl.service 有効化"
else
    echo "⚠ $SVC_SRC が見つかりません"
    exit 1
fi

echo ""
echo "=== 完了 ==="
echo "再起動して SDL メーターを起動:"
echo "  sudo reboot"
