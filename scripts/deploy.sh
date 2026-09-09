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
  # version 文字列を一意化: dev-<shortSHA>[-dirty]-<HHMMSS>
  # ブラウザの startVersionCheck がデプロイ毎にバージョン変化を検知して自動リロードする
  local SHORT_SHA DIRTY BUILD_TS VERSION
  SHORT_SHA=$(git rev-parse --short HEAD 2>/dev/null || echo "nogit")
  DIRTY=""
  if [ -n "$(git status --porcelain 2>/dev/null)" ]; then DIRTY="-dirty"; fi
  BUILD_TS=$(date +%H%M%S)
  VERSION="dev-${SHORT_SHA}${DIRTY}-${BUILD_TS}"
  echo "  version: ${VERSION}"
  GOOS=linux GOARCH=arm64 go build -ldflags "-X main.version=${VERSION}" -o bin/pi-obd-meter ./cmd/pi-obd-meter
  GOOS=linux GOARCH=arm64 go build -ldflags "-X main.version=${VERSION}" -o bin/pi-obd-scanner ./cmd/pi-obd-scanner
  echo "✓ bin/pi-obd-meter, bin/pi-obd-scanner"
}

cmd_deploy() {
  cmd_build
  echo "Deploying to ${PI}:${DEST}..."
  rsync -avz "$ROOT/bin/" "${PI}:${DEST}/"
  rsync -avz "$ROOT/configs/" "${PI}:${DEST}/configs/"
  rsync -avz "$ROOT/web/static/" "${PI}:${DEST}/web/static/"
  ssh "$PI" "mkdir -p ${DEST}/scripts"
  rsync -avz "$ROOT/scripts/auto-update.sh" "${PI}:${DEST}/scripts/auto-update.sh"
  # kiosk (cog) は systemd 配下ではなく labwc autostart 経由なので再起動不要。
  # ブラウザは startVersionCheck がバージョン変化を検知して 30 秒以内に自動リロードする。
  ssh "$PI" "sudo systemctl restart ${SERVICE}"
  cmd_persist
  echo "✓ デプロイ完了"
}

cmd_persist() {
  # overlayfs 有効時、/opt への書き込みは RAM 上のオーバーレイに載るため
  # 再起動で消える。下層の実ルートへ複製して初めて永続化される。
  # これを忘れると「デプロイしたのにエンジンを切ったら元に戻った」が起きる。
  echo "Persisting to lower layer..."
  ssh "$PI" "set -e
    if ! mount | grep -q 'on / type overlay'; then
      echo '  overlayfs 無効。永続化は不要'
      exit 0
    fi
    sudo mount -o remount,rw /media/root-ro
    sudo rsync -a ${DEST}/ /media/root-ro${DEST}/

    # systemd ユニットも下層へ書く。
    #
    # /etc/systemd/system は overlay の上層 (tmpfs) なので、ここに置いた
    # ユニットは再起動で消え、SD に焼かれた古い版に戻る。auto-update から
    # 永続化するのは overlayroot-chroot を自動で叩くことになり危険なので、
    # deploy でだけ行う (#191)。
    for u in ${DEST}/scripts/ops/systemd/*.service ${DEST}/scripts/ops/systemd/*.timer \
             ${DEST}/configs/pi-obd-meter.service; do
      [ -f "$u" ] || continue
      n=$(basename "$u")
      sudo cp "$u" "/etc/systemd/system/$n"
      sudo cp "$u" "/media/root-ro/etc/systemd/system/$n"
    done
    sudo systemctl daemon-reload
    sync
    # 稼働中の remount,ro は overlayfs が下層を掴んでいるため EBUSY で失敗する。
    # overlayroot-chroot は終了時に確実に ro へ戻すので、その後始末を借りる。
    sudo overlayroot-chroot true >/dev/null 2>&1 || true
    if mount | grep -q '/media/root-ro .*(ro,'; then
      echo '  ✓ 永続化完了 (下層 ro に復帰)'
    else
      echo '  ★ /media/root-ro が rw のままです。再起動して戻してください' >&2
      exit 1
    fi"
}

cmd_setup() {
  echo "Setting up Raspberry Pi..."
  local REMOTE_USER="${PI%%@*}"
  ssh "$PI" "sudo mkdir -p ${DEST}/configs /var/lib/pi-obd-meter && sudo chown -R ${REMOTE_USER}:${REMOTE_USER} ${DEST} /var/lib/pi-obd-meter"
  # swap無効化（SD書き込み削減）
  echo "Disabling swap..."
  ssh "$PI" "sudo dphys-swapfile swapoff 2>/dev/null; sudo systemctl disable dphys-swapfile 2>/dev/null || true"
  # systemd登録を先に行う（deploy 内の restart が成功するように）
  rsync -avz "$ROOT/configs/pi-obd-meter.service" "${PI}:/tmp/pi-obd-meter.service"
  ssh "$PI" "sudo cp /tmp/pi-obd-meter.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable ${SERVICE}"
  # キオスクサービス登録
  echo "Installing kiosk service..."
  rsync -avz "$ROOT/configs/kiosk.service" "${PI}:/tmp/kiosk.service"
  ssh "$PI" "sudo cp /tmp/kiosk.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable kiosk"
  # 自動更新タイマー登録
  echo "Installing auto-update timer..."
  rsync -avz "$ROOT/scripts/auto-update.sh" "${PI}:${DEST}/scripts/auto-update.sh"
  ssh "$PI" "chmod +x ${DEST}/scripts/auto-update.sh"
  rsync -avz "$ROOT/configs/auto-update.service" "${PI}:/tmp/auto-update.service"
  rsync -avz "$ROOT/configs/auto-update.timer" "${PI}:/tmp/auto-update.timer"
  ssh "$PI" "sudo cp /tmp/auto-update.service /tmp/auto-update.timer /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable --now auto-update.timer"
  cmd_deploy
  echo "✓ 初期セットアップ完了"
}

cmd_ssh()           { ssh "$PI"; }
cmd_logs()          { ssh "$PI" "journalctl -u ${SERVICE} -f"; }
cmd_status()        { ssh "$PI" "systemctl status ${SERVICE}"; }
cmd_restart()       { ssh "$PI" "sudo systemctl restart ${SERVICE}"; }
cmd_kiosk_logs()    { ssh "$PI" "journalctl -u kiosk -f"; }
cmd_kiosk_restart() { ssh "$PI" "sudo systemctl restart kiosk"; }
cmd_shutdown()      { ssh "$PI" "sudo shutdown -h now"; echo "✓ シャットダウン送信済み。LEDが消えたら電源を抜いてOK"; }
cmd_reboot()        { ssh "$PI" "sudo reboot"; echo "✓ 再起動中...30秒ほどお待ちください"; }

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
  deploy           ビルド + rsync転送 + サービス再起動 + 下層への永続化
  persist          下層(実ルート)へ複製するだけ。overlayfs 有効時に必要

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
  persist)         cmd_persist ;;
  setup)           cmd_setup ;;
  ssh)             cmd_ssh ;;
  logs)            cmd_logs ;;
  status)          cmd_status ;;
  restart)         cmd_restart ;;
  kiosk-logs)      cmd_kiosk_logs ;;
  kiosk-restart)   cmd_kiosk_restart ;;
  shutdown)        cmd_shutdown ;;
  reboot)          cmd_reboot ;;
  release-install) shift; cmd_release_install "$@" ;;
  help|--help|-h)  cmd_help ;;
  *)
    echo "Unknown command: $1"
    cmd_help
    exit 1
    ;;
esac
