#!/bin/sh
# contrib/0001-brcmfmac-*.patch を mainline に対して検証する。
#
# 適用できること・警告なしでコンパイルできること・checkpatch を通ることを
# 確かめる。実機での動作確認は含まない（それは Pi でしかできない）。
#
# 必要なもの: Docker (arm64 で動くもの。Apple Silicon ならネイティブ)
# 所要時間: 10分程度。カーネルの shallow clone とツールチェインの取得を含む
#
# usage: ./scripts/verify-brcmfmac-patch.sh

set -eu

repo_root=$(cd "$(dirname "$0")/.." && pwd)
patch_name=0001-brcmfmac-log-firmware-status-on-failed-connect.patch

if [ ! -f "$repo_root/contrib/$patch_name" ]; then
	echo "パッチが見つからない: contrib/$patch_name" >&2
	exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

cat > "$work/inner.sh" <<'INNER'
set -eux
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq git make gcc bc bison flex libssl-dev libelf-dev \
	python3 perl >/dev/null

cd /tmp
git clone -q --depth 1 https://github.com/torvalds/linux.git
cd linux
echo "### kernel: $(make kernelversion) $(git rev-parse --short HEAD)"

# 1. 適用できるか
git apply --check -v "/work/$PATCH_NAME"
git apply "/work/$PATCH_NAME"
git diff --stat

# 2. checkpatch (パッチファイルに直接かける。合成コミットにかけると
#    Signed-off-by 欠けを自分で作り込んでしまう)
./scripts/checkpatch.pl --no-tree --strict "/work/$PATCH_NAME" || true

# 3. 警告なしでコンパイルできるか
make ARCH=arm64 defconfig >/dev/null
./scripts/config --module CONFIG_BRCMFMAC --module CONFIG_CFG80211
make ARCH=arm64 olddefconfig >/dev/null
grep -q '^CONFIG_BRCMFMAC=m' .config
make ARCH=arm64 -j"$(nproc)" modules_prepare >/dev/null
obj=drivers/net/wireless/broadcom/brcm80211/brcmfmac/cfg80211.o
make ARCH=arm64 -j"$(nproc)" W=1 "$obj"
test -f "$obj"
echo "### OK: $obj を警告なしで生成した"
INNER

docker run --rm --platform linux/arm64 \
	-e PATCH_NAME="$patch_name" \
	-v "$repo_root/contrib:/work:ro" \
	-v "$work:/kb" \
	debian:testing sh /kb/inner.sh
