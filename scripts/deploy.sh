#!/bin/bash
# pi-obd-meter 開発・デプロイスクリプト
# Usage: ./scripts/deploy.sh <command> [args]

set -euo pipefail

# --- 設定 ---
PI="${PI_HOST:-laurel@pi-obd-meter.local}"
DEST="/opt/pi-obd-meter"
SERVICE="pi-obd-meter"
REPO="${GITHUB_REPO:-RyoheiHashimoto/pi-obd-meter}"

# プロジェクトルート
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# --- コマンド ---

cmd_build() {
  echo "Building for ARM64..."
  cd "$ROOT"
  GOOS=linux GOARCH=arm64 go build -o bin/pi-obd-meter ./cmd/pi-obd-meter
  GOOS=linux GOARCH=arm64 go build -o bin/pi-obd-scanner ./cmd/pi-obd-scanner
  echo "✓ bin/pi-obd-meter, bin/pi-obd-scanner"
}

cmd_deploy() {
  cmd_build
  echo "Deploying to ${PI}:${DEST}..."
  rsync -avz "$ROOT/bin/" "${PI}:${DEST}/"
  rsync -avz "$ROOT/configs/" "${PI}:${DEST}/configs/"
  ssh "$PI" "sudo systemctl restart ${SERVICE} && sudo systemctl restart kiosk"
  echo "✓ デプロイ完了"
}

cmd_setup() {
  echo "Setting up Raspberry Pi..."
  local REMOTE_USER="${PI%%@*}"
  ssh "$PI" "sudo mkdir -p ${DEST}/web/static ${DEST}/configs /var/lib/pi-obd-meter && sudo chown -R ${REMOTE_USER}:${REMOTE_USER} ${DEST} /var/lib/pi-obd-meter"
  # systemd登録を先に行う（deploy 内の restart が成功するように）
  rsync -avz "$ROOT/configs/pi-obd-meter.service" "${PI}:/tmp/pi-obd-meter.service"
  ssh "$PI" "sudo cp /tmp/pi-obd-meter.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable ${SERVICE}"
  # キオスクサービス登録
  echo "Installing kiosk service..."
  rsync -avz "$ROOT/configs/kiosk.service" "${PI}:/tmp/kiosk.service"
  ssh "$PI" "sudo cp /tmp/kiosk.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable kiosk"
  cmd_deploy
  echo "✓ 初期セットアップ完了"
}

cmd_ssh()           { ssh "$PI"; }
cmd_logs()          { ssh "$PI" "journalctl -u ${SERVICE} -f"; }
cmd_status()        { ssh "$PI" "systemctl status ${SERVICE}"; }
cmd_restart()       { ssh "$PI" "sudo systemctl restart ${SERVICE} && sudo systemctl restart kiosk"; }
cmd_kiosk_logs()    { ssh "$PI" "journalctl -u kiosk -f"; }
cmd_kiosk_restart() { ssh "$PI" "sudo systemctl restart kiosk"; }
cmd_shutdown()      { ssh "$PI" "sudo shutdown -h now"; echo "✓ シャットダウン送信済み。LEDが消えたら電源を抜いてOK"; }
cmd_reboot()        { ssh "$PI" "sudo reboot"; echo "✓ 再起動中...30秒ほどお待ちください"; }

cmd_overlay_on() {
  ssh "$PI" "sudo raspi-config nonint do_overlayfs 0"
  echo "⚠️  overlayFS有効化予約済み。再起動で有効になる"
  echo "    ssh ${PI} 'sudo reboot'"
}

cmd_overlay_off() {
  ssh "$PI" "sudo raspi-config nonint do_overlayfs 1"
  echo "⚠️  overlayFS無効化予約済み。再起動で無効になる"
  echo "    ssh ${PI} 'sudo reboot'"
}

cmd_release_install() {
  local VERSION="${1:-}"
  if [ -z "$VERSION" ]; then
    echo "Fetching latest release..."
    VERSION=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
    if [ -z "$VERSION" ]; then
      echo "Error: Could not determine latest version"
      exit 1
    fi
  fi

  echo "Installing ${VERSION} from GitHub Releases..."
  local URL="https://github.com/${REPO}/releases/download/${VERSION}/pi-obd-meter-${VERSION}-arm64.tar.gz"
  local TMPDIR
  TMPDIR=$(mktemp -d)

  echo "Downloading ${URL}..."
  curl -fsSL "$URL" -o "${TMPDIR}/release.tar.gz"

  echo "Extracting..."
  tar xzf "${TMPDIR}/release.tar.gz" -C "${TMPDIR}"

  echo "Stopping service..."
  sudo systemctl stop "${SERVICE}" 2>/dev/null || true

  echo "Installing to ${DEST}..."
  mkdir -p "${DEST}"
  cp "${TMPDIR}/pi-obd-meter/pi-obd-meter" "${DEST}/pi-obd-meter"
  cp "${TMPDIR}/pi-obd-meter/pi-obd-scanner" "${DEST}/pi-obd-scanner" 2>/dev/null || true
  chmod +x "${DEST}/pi-obd-meter"
  chmod +x "${DEST}/pi-obd-scanner" 2>/dev/null || true
  cp -r "${TMPDIR}/pi-obd-meter/web/" "${DEST}/web/"
  cp -r "${TMPDIR}/pi-obd-meter/configs/" "${DEST}/configs/" 2>/dev/null || true

  rm -rf "${TMPDIR}"

  echo "Starting service..."
  sudo systemctl start "${SERVICE}"
  echo "✓ ${VERSION} インストール完了"
}

cmd_help() {
  cat <<'HELP'
Usage: ./scripts/deploy.sh <command> [args]

開発 (Mac上で実行):
  build            クロスコンパイル (ARM64)
  deploy           ビルド + rsync転送 + サービス再起動

ラズパイ管理 (Mac上で実行):
  setup            初回セットアップ (ディレクトリ作成 + systemd登録)
  ssh              ラズパイにSSH接続
  logs             リアルタイムログ表示
  status           サービス状態確認
  restart          サービス再起動 (転送なし)
  kiosk-logs       キオスク (Chromium) ログ表示
  kiosk-restart    キオスク (Chromium) 再起動
  shutdown         ラズパイを安全にシャットダウン
  reboot           ラズパイを再起動

overlayFS:
  overlay-on       overlayFS有効化 (再起動後有効)
  overlay-off      overlayFS無効化 (再起動後無効)

リリース (ラズパイ上で実行):
  release-install [version]  GitHub Releasesからインストール

環境変数:
  PI_HOST          ラズパイのSSH先 (default: laurel@pi-obd-meter.local)
  GITHUB_REPO      GitHubリポジトリ (default: YOUR_USER/pi-obd-meter)
HELP
}

# --- エントリポイント ---

case "${1:-help}" in
  build)           cmd_build ;;
  deploy)          cmd_deploy ;;
  setup)           cmd_setup ;;
  ssh)             cmd_ssh ;;
  logs)            cmd_logs ;;
  status)          cmd_status ;;
  restart)         cmd_restart ;;
  kiosk-logs)      cmd_kiosk_logs ;;
  kiosk-restart)   cmd_kiosk_restart ;;
  shutdown)        cmd_shutdown ;;
  reboot)          cmd_reboot ;;
  overlay-on)      cmd_overlay_on ;;
  overlay-off)     cmd_overlay_off ;;
  release-install) shift; cmd_release_install "$@" ;;
  help|--help|-h)  cmd_help ;;
  *)
    echo "Unknown command: $1"
    cmd_help
    exit 1
    ;;
esac
