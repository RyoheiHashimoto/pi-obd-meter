#!/usr/bin/env python3
"""contrib/mazda_demio_dy.dbc を実車の candump ログでデコード検証する。

opendbc へ出す前の確認用。停車ログでは通っても走行中のフレームで落ちる
可能性があるため、車輪速から「そのログが本当に走行を含むか」も併せて出す。

usage:
    scripts/ops/verify-dbc.py logs-pi/can-verify/*.log
    scripts/ops/verify-dbc.py --fetch     # Pi から最新の raw_*.log を取る

Pi 側の出力先は /var/log/can-verify/raw_<stamp>.log (scripts/ops/can-verify.sh)。
"""
import argparse
import glob
import os
import subprocess
import sys
from collections import Counter

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
DBC = os.path.join(REPO, "contrib", "mazda_demio_dy.dbc")
PI = os.environ.get("PI_HOST", "laurel@pi-obd-meter.local")

# 4輪速 0x4B0 は各16bitで 10000 が 0 km/h。走行判定に使う。
WHEEL_ID = 0x4B0
WHEEL_ZERO = 10000


def fetch():
    """Pi から candump ログを回収する。"""
    dest = os.path.join(REPO, "logs-pi", "can-verify")
    os.makedirs(dest, exist_ok=True)
    src = f"{PI}:/var/log/can-verify/raw_*.log"
    print(f"回収: {src} -> {dest}", file=sys.stderr)
    subprocess.run(["scp", "-o", "ConnectTimeout=10", src, dest], check=True)
    return sorted(glob.glob(os.path.join(dest, "raw_*.log")))


def parse(path):
    """candump のログを (can_id, data) で返す。2つの形式に対応する。

    candump は起動オプションで出力形式が変わる。Pi の can-verify.sh は
    既定形式 (B) で書くが、手で取ったログには compact 形式 (A) が混ざる。
    片方しか読めない実装だと、もう片方を渡したときに「0フレーム」と出て
    ログが空なのかパーサが合っていないのか区別できない。

      A  (1788189998.242396) can0 201#0B107C9C27100064
      B   (1788865430.176578)  can0  201   [8]  0B 0F 7D 78 27 10 00 64
    """
    with open(path, errors="ignore") as fh:
        for line in fh:
            parts = line.split()
            if len(parts) < 3:
                continue
            try:
                if "#" in parts[2]:                       # A: compact
                    ident, _, payload = parts[2].partition("#")
                    yield int(ident, 16), bytes.fromhex(payload)
                elif len(parts) >= 4 and parts[3].startswith("["):
                    n = int(parts[3].strip("[]"))          # B: 既定
                    if len(parts) < 4 + n:
                        continue
                    yield int(parts[2], 16), bytes.fromhex("".join(parts[4:4 + n]))
            except ValueError:
                continue


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("logs", nargs="*")
    ap.add_argument("--fetch", action="store_true")
    args = ap.parse_args()

    try:
        import cantools
    except ImportError:
        sys.exit("cantools が必要: pip install cantools")

    db = cantools.database.load_file(DBC)
    known = {m.frame_id for m in db.messages}
    print(f"DBC: {os.path.relpath(DBC, REPO)}  メッセージ {len(db.messages)}件 "
          f"({', '.join(hex(i) for i in sorted(known))})")

    logs = fetch() if args.fetch else args.logs
    if not logs:
        sys.exit("ログを指定するか --fetch を使う")

    exit_code = 0
    for path in logs:
        total = decoded = 0
        errors = Counter()
        max_wheel = 0
        seen = Counter()
        for can_id, data in parse(path):
            total += 1
            if can_id == WHEEL_ID and len(data) >= 8:
                max_wheel = max(max_wheel, *(
                    int.from_bytes(data[k:k + 2], "big") for k in (0, 2, 4, 6)))
            if can_id not in known:
                continue
            seen[can_id] += 1
            try:
                db.decode_message(can_id, data)
                decoded += 1
            except Exception as exc:  # noqa: BLE001 - 種類を数えたい
                errors[f"{hex(can_id)}: {type(exc).__name__}: {exc}"] += 1

        kmh = (max_wheel - WHEEL_ZERO) / 100 if max_wheel else 0.0
        moving = kmh > 1.0
        print(f"\n--- {os.path.relpath(path, REPO)} ---")
        print(f"  全フレーム {total:,} / DBC対象 {sum(seen.values()):,} / "
              f"デコード成功 {decoded:,}")
        print(f"  最大車輪速 {kmh:.1f} km/h  "
              f"{'走行を含む' if moving else '★停車のみ（走行ログでの検証が未達）'}")
        for fid, n in sorted(seen.items()):
            print(f"    {hex(fid)} {n:,}")
        if errors:
            exit_code = 1
            print(f"  デコードエラー {sum(errors.values()):,}件:")
            for msg, n in errors.most_common(10):
                print(f"    {n:,}x {msg}")
        else:
            print("  デコードエラー 0件")
        if not moving:
            exit_code = max(exit_code, 2)

    if exit_code == 2:
        print("\n走行を含むログが無い。opendbc へ出す前に走行ログで再実行すること。")
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
