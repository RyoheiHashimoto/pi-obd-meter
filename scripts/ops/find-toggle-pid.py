#!/usr/bin/env python3
# probe-toggle.py のラベルと poll22 のログを突き合わせ、
# 操作に連動した PID を探す。
#
#   python3 find-toggle-pid.py toggle_*.labels poll22_*.csv [poll22_*.csv ...]
#
# Mac 上で走らせる。
#
# 【判定】
# 各項目について ON区間 と OFF区間 の値を比べる。停車・アイドルで他が
# 動いていないので、分布が完全に分離すれば、それがその装備の信号である。
#
# 2026-09-01 にこの方式で以下を同定した。
#   1101 bit1 ブレーキ / 1101 bit0 ファン / 1103 bit2 エアコン
import collections, csv, io, statistics as st, sys

if len(sys.argv) < 3:
    sys.exit(__doc__)

labels = []
with open(sys.argv[1], errors="replace") as f:
    next(f, None)
    for line in f:
        p = line.rstrip("\n").split(",", 1)
        if len(p) == 2:
            try:
                labels.append((float(p[0]), p[1]))
            except ValueError:
                pass
if not labels:
    sys.exit("ラベルが読めない")

rows = []
for path in sys.argv[2:]:
    raw = open(path, "rb").read().replace(b"\x00", b"")
    for r in csv.DictReader(io.StringIO(raw.decode("utf-8", "replace"))):
        try:
            rows.append((float(r["t"]), r["pid"], int(r["b1"]), int(r["b2"])))
        except (ValueError, KeyError, TypeError):
            continue
rows.sort()
print("ラベル %d 件  PID サンプル %d 件" % (len(labels), len(rows)))

# 項目ごとに ON/OFF の時間窓を作る
items = collections.defaultdict(lambda: {"ON": [], "OFF": []})
for i, (t, name) in enumerate(labels):
    parts = name.rsplit(" ", 1)
    if len(parts) != 2:
        continue
    item, phase = parts
    if phase.startswith("ON"):
        ph = "ON"
    elif phase == "OFF":
        ph = "OFF"
    else:
        continue
    end = labels[i + 1][0] if i + 1 < len(labels) else t + 20
    # 操作直後の2秒は過渡なので捨てる
    items[item][ph].append((t + 2, end))

def in_windows(t, ws):
    return any(a <= t <= b for a, b in ws)

for item, ph in items.items():
    on_w, off_w = ph["ON"], ph["OFF"]
    if not on_w or not off_w:
        continue
    per = collections.defaultdict(lambda: ([], []))
    for t, pid, b1, b2 in rows:
        if in_windows(t, on_w):
            per[pid][0].append((b1, b2))
        elif in_windows(t, off_w):
            per[pid][1].append((b1, b2))
    print("\n=== %s ===" % item)
    hits = []
    for pid, (on, off) in per.items():
        if len(on) < 5 or len(off) < 5:
            continue
        for bi, bn in ((0, "b1"), (1, "b2")):
            a = [x[bi] for x in on]
            b = [x[bi] for x in off]
            if len(set(a)) == 1 and len(set(b)) == 1 and a[0] != b[0]:
                hits.append((pid, bn, a[0], b[0], len(a), len(b), "完全"))
                continue
            sa, sb = sorted(a), sorted(b)
            if sa[0] > sb[-1] or sb[0] > sa[-1]:
                hits.append((pid, bn, st.median(a), st.median(b), len(a), len(b), "分離"))
    if not hits:
        print("  該当なし")
        continue
    hits.sort(key=lambda x: (x[6] != "完全", x[0]))
    print("  %-6s %-4s %9s %9s %6s %6s  %s" % ("PID", "byte", "ON", "OFF", "n(ON)", "n(OFF)", "判定"))
    for pid, bn, a, b, na, nb, kind in hits[:12]:
        print("  %-6s %-4s %9.0f %9.0f %6d %6d  %s" % (pid, bn, a, b, na, nb, kind))

print("\n【注意】停車・アイドルで他を触っていないことが前提。")
print("窓を開けた、シフトを動かした、といった操作が混ざると偽陽性が出る。")
