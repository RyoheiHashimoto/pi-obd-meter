#!/usr/bin/env python3
"""DBC (cantools) と Go デコーダ (internal/can) の出力を突き合わせる。

DBC 内部の整合だけを見ても「DBC を DBC で検証」しているだけになる。独立に
書かれた実装と数値が一致することを確かめる。

    go run ./tools/xcheck-dbc <candump.log> > /tmp/go.csv
    scripts/ops/xcheck-dbc.py <candump.log> /tmp/go.csv
"""
import os
import sys
from collections import defaultdict

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
DBC = os.path.join(REPO, "contrib", "mazda_demio_dy.dbc")
# Go 側と同じ順序・同じ 200000 フレームを見る
LIMIT = 200000
# Go 側が出している信号だけを比べる
FIELDS = {0x201: ["RPM", "SPEED", "ENGINE_LOAD"],
          0x430: ["FUEL_LEVEL", "UNKNOWN_B1", "ODOMETER"],
          0x420: ["COOLANT_TEMP", "DISTANCE_PULSE"],
          0x4B0: ["WHEEL_MEAN"]}

# Go の DecodeWheelSpeed は FL ではなく4輪平均を返し、負値を 0 にクランプする。
# 信号名をそのまま突き合わせると「DBC が間違っている」ように見えるので、
# ここで同じ計算をしてから比べる。
WHEELS = ["WHEEL_SPEED_FL", "WHEEL_SPEED_FR", "WHEEL_SPEED_RL", "WHEEL_SPEED_RR"]


def main():
    import cantools
    log, gocsv = sys.argv[1], sys.argv[2]
    db = cantools.database.load_file(DBC)
    raw = lambda v: v.value if hasattr(v, "value") else v  # noqa: E731

    mine = {}
    n = 0
    with open(log, errors="ignore") as fh:
        for line in fh:
            q = line.split()
            if len(q) < 5 or not q[3].startswith("["):
                continue
            try:
                ln = int(q[3].strip("[]"))
                if len(q) < 4 + ln:
                    continue
                fid = int(q[2], 16)
                data = bytes.fromhex("".join(q[4:4 + ln]))
            except ValueError:
                continue
            n += 1
            if n > LIMIT:
                break
            if fid not in FIELDS:
                continue
            m = db.decode_message(fid, data)
            for k in FIELDS[fid]:
                if k == "WHEEL_MEAN":
                    v = sum(max(0.0, float(raw(m[w]))) for w in WHEELS) / 4
                else:
                    v = float(raw(m[k]))
                mine[(n, f"{fid:03X}", k)] = v

    diffs = defaultdict(list)
    compared = missing = 0
    with open(gocsv) as fh:
        for line in fh:
            i, fid, key, val = line.rstrip("\n").split(",")
            k = (int(i), fid, key)
            if k not in mine:
                missing += 1
                continue
            compared += 1
            d = abs(mine[k] - float(val))
            if d > 1e-6:
                diffs[f"{fid}.{key}"].append(d)

    print(f"突合 {compared:,} 点 / Go 側にあって DBC 側に無い {missing:,} 点")
    if not diffs:
        print("  全一致 (差 < 1e-6)")
        return 0
    print("  不一致:")
    for k, v in sorted(diffs.items(), key=lambda kv: -len(kv[1])):
        print(f"    {k:26} {len(v):,}点  最大差 {max(v):.6g}")
    return 1


if __name__ == "__main__":
    sys.exit(main())
