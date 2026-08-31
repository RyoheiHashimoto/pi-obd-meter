#!/usr/bin/env python3
# 未同定の Mode22 PID から「勾配・路面負荷」らしいものを探す。
#
#   python3 find-grade-pid.py poll22_*.csv
#
# Mac 上で走らせる。poll22-lean.py が録った CSV をそのまま渡す。
#
# 【なぜ在るはずか】
# FN4A-EL は road load / grade logic で変速線を動かす。登坂で不要な
# シフトアップを避け、降坂でエンジンブレーキを残すには、PCM が勾配相当の
# 量を持っていなければ制御できない。#150 の未同定 PID 17個のどれかが
# それである可能性が高い。
#
# 【見分け方】
# 勾配相当値は「登坂で変わる」が「車速・RPM・スロットルのどれとも
# 相関しない」。負荷やトルクの写しなら、スロットルと強く相関する。
# その差で篩う。
import csv, math, sys, collections, statistics as st

UNKNOWN = {"1782", "17A0", "097C", "1410", "1103", "1104", "16E8", "16CD",
           "17BB", "17BC", "17C1", "3201", "096E", "098E", "16E9", "1746", "1101"}

rows = []
for path in sys.argv[1:]:
    with open(path, newline="", errors="replace") as f:
        raw = f.read().replace("\x00", "")
    for r in csv.DictReader(raw.splitlines()):
        try:
            rows.append((float(r["t"]), r["pid"], int(r["b1"]), int(r["b2"]),
                         float(r["coolant"]), float(r["speed"])))
        except (ValueError, KeyError, TypeError):
            continue
if not rows:
    sys.exit("データが読めない")
print("サンプル %d 件  PID %d 種" % (len(rows), len({r[1] for r in rows})))

# 各PIDの時系列を作り、その時点の車速を添える
series = collections.defaultdict(list)
for t, pid, b1, b2, _, sp in rows:
    series[pid].append((t, b1 * 256 + b2, b1, sp))


def corr(x, y):
    if len(x) < 30:
        return 0.0
    mx, my = st.mean(x), st.mean(y)
    n = sum((a - mx) * (b - my) for a, b in zip(x, y))
    d = math.sqrt(sum((a - mx) ** 2 for a in x) * sum((b - my) ** 2 for b in y))
    return n / d if d else 0.0


print("\n=== 未同定PIDの変動と車速相関 ===")
print("%-6s %6s %8s %8s %9s %10s  %s" % ("PID", "n", "最小", "最大", "変動幅", "車速相関", "所見"))
cands = []
for pid in sorted(series):
    if pid not in UNKNOWN:
        continue
    v = series[pid]
    if len(v) < 50:
        continue
    v16 = [x[1] for x in v]
    v8 = [x[2] for x in v]
    sp = [x[3] for x in v]
    # 8bit と 16bit のどちらで動いているか、変動の大きいほうを見る
    use16 = (max(v16) - min(v16)) > (max(v8) - min(v8)) * 1.5
    val = v16 if use16 else v8
    span = max(val) - min(val)
    if span == 0:
        continue
    c = corr(val, sp)
    # 停車中に固定なら、走行に関わる量である
    mv = [(x[1] if use16 else x[2]) for x in v if x[3] > 10]
    sp0 = [(x[1] if use16 else x[2]) for x in v if x[3] < 1]
    fixed_stopped = bool(sp0) and (max(sp0) - min(sp0)) <= max(1, span * 0.1)
    moves_when_driving = bool(mv) and (max(mv) - min(mv)) > span * 0.3

    note = ""
    if abs(c) > 0.6:
        note = "車速の写し"
    elif abs(c) < 0.25 and span > 8:
        if fixed_stopped and moves_when_driving:
            note = "★★ 停車中は固定・走行中に動く・車速と無相関"
            cands.append(pid)
        else:
            note = "・車速とは無相関だが停車中も動く"
    print("%-6s %6d %8d %8d %9d %+10.3f  %s"
          % (pid + ("(16)" if use16 else "(8)"), len(v), min(val), max(val), span, c, note))

if cands:
    print("\n=== 候補の詳細 ===")
    print("走行中 (10km/h超) と停車中で値が分かれるか、")
    print("同じ車速でも値が違う場面があるかを見る。")
    for pid in cands:
        v = series[pid]
        v16 = [x[1] for x in v]
        v8 = [x[2] for x in v]
        use16 = (max(v16) - min(v16)) > (max(v8) - min(v8)) * 1.5
        moving = [(x[1] if use16 else x[2]) for x in v if x[3] > 10]
        stopped = [(x[1] if use16 else x[2]) for x in v if x[3] < 1]
        print("\n  %s" % pid)
        if moving:
            print("    走行中 n=%d  中央 %.0f  範囲 %d〜%d" % (len(moving), st.median(moving), min(moving), max(moving)))
        if stopped:
            print("    停車中 n=%d  中央 %.0f  範囲 %d〜%d" % (len(stopped), st.median(stopped), min(stopped), max(stopped)))
        # 同一車速帯での散らばり = 車速以外の何かで動いている証拠
        for lo, hi in ((20, 40), (40, 60), (60, 80)):
            g = [(x[1] if use16 else x[2]) for x in v if lo <= x[3] < hi]
            if len(g) >= 30:
                print("    %d-%dkm/h n=%d  中央 %.0f  ばらつき σ=%.1f" % (lo, hi, len(g), st.median(g), st.pstdev(g)))
else:
    print("\n候補なし。登坂を含むログでないと差が出ない。")
    print("比叡山のような連続した登りで poll22 を回したデータが要る。")

print("\n【注意】このスクリプトは候補を絞るだけで、同定はしない。")
print("勾配と断定するには、GPS の標高 (#164) か IMU (#165) との照合が要る。")
