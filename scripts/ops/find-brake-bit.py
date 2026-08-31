#!/usr/bin/env python3
# capture-brake.py が録ったログから、ブレーキ操作に連動するビットを探す。
#
#   python3 find-brake-bit.py brake_YYYYmmdd_HHMMSS.log brake_YYYYmmdd_HHMMSS.labels
#
# Mac 上で走らせる。Pi は不要。
#
# 【判定の考え方】
# 「踏んだ区間で1、離した区間で0」になるビットを探すだけでは足りない。
# 減速中に立つだけのビット (車速依存、ブレーキ以外の何か) が混ざるため。
#
# そこで「★アクセルオフのみで減速」の区間を対照群に使う。ブレーキ信号なら
# その区間では立たないはずで、単に減速と相関するビットはそこでも立つ。
import re, sys, collections

if len(sys.argv) < 3:
    sys.exit(__doc__)

pat = re.compile(r"\((\d+\.\d+)\)\s+\S+\s+([0-9A-Fa-f]{3,8})#([0-9A-Fa-f]*)")
frames = []
with open(sys.argv[1], errors="replace") as f:
    for line in f:
        m = pat.search(line)
        if not m:
            continue
        data = m.group(3)
        by = [int(data[i:i + 2], 16) for i in range(0, len(data) - 1, 2)]
        frames.append((float(m.group(1)), m.group(2).upper(), by))

labels = []
with open(sys.argv[2], errors="replace") as f:
    next(f, None)
    for line in f:
        p = line.rstrip("\n").split(",", 1)
        if len(p) == 2:
            labels.append((float(p[0]), p[1]))

print("フレーム %d 件  ラベル %d 件" % (len(frames), len(labels)))
if not frames or not labels:
    sys.exit("データが足りない")

ids = collections.Counter(f[1] for f in frames)
print("CAN ID: " + "  ".join("%s:%d" % (k, v) for k, v in ids.most_common(12)))


def window(i):
    """ラベル i の開始から次のラベルまで (最後は +6秒)"""
    t0 = labels[i][0]
    t1 = labels[i + 1][0] if i + 1 < len(labels) else t0 + 6.0
    return [f for f in frames if t0 <= f[0] < t1]


ON = [i for i, (_, n) in enumerate(labels) if "踏む" in n]
OFF = [i for i, (_, n) in enumerate(labels) if "離す" in n]
COAST = [i for i, (_, n) in enumerate(labels) if "アクセルオフのみ" in n]
BRAKE_MOVING = [i for i, (_, n) in enumerate(labels) if "フットブレーキ" in n]

print("\n踏む %d区間 / 離す %d区間 / 惰行 %d区間 / 走行中制動 %d区間"
      % (len(ON), len(OFF), len(COAST), len(BRAKE_MOVING)))
if not ON or not OFF:
    sys.exit("踏む/離す のラベルが無い")


def bitstats(idxs, cid, byte, bit):
    """該当区間で、そのビットが立っていた割合"""
    hi = tot = 0
    for i in idxs:
        for _, c, by in window(i):
            if c != cid or len(by) <= byte:
                continue
            tot += 1
            if by[byte] >> bit & 1:
                hi += 1
    return (hi / tot if tot else None), tot


print("\n=== ブレーキ操作に連動するビット ===")
print("  条件: 踏む区間で90%以上立ち、離す区間で10%以下")
hits = []
for cid in sorted(ids):
    maxlen = max((len(by) for _, c, by in frames if c == cid), default=0)
    for byte in range(maxlen):
        for bit in range(8):
            on, n_on = bitstats(ON, cid, byte, bit)
            off, n_off = bitstats(OFF, cid, byte, bit)
            if on is None or off is None or n_on < 20 or n_off < 20:
                continue
            if on >= 0.9 and off <= 0.1:
                hits.append((cid, byte, bit, on, off, False))
            elif on <= 0.1 and off >= 0.9:
                hits.append((cid, byte, bit, on, off, True))

if not hits:
    print("  該当なし。ブレーキは CAN に出ていないか、別のバスにある。")
else:
    print("  %-5s %-5s %-4s %8s %8s %s" % ("ID", "byte", "bit", "踏む", "離す", "極性"))
    for cid, byte, bit, on, off, inv in hits:
        print("  %-5s B%-4d %-4d %7.0f%% %7.0f%%  %s"
              % (cid, byte, bit, on * 100, off * 100, "反転(0=踏む)" if inv else "1=踏む"))

    # --- 対照群でふるいにかける ---
    if COAST and BRAKE_MOVING:
        print("\n=== 走行中の対照検証 ===")
        print("  ブレーキ信号なら「惰行」で立たず「制動」で立つ。")
        print("  両方で立つなら、それは単に減速と相関しているだけ。")
        print("  %-5s %-5s %-4s %10s %10s  判定" % ("ID", "byte", "bit", "惰行", "制動"))
        for cid, byte, bit, on, off, inv in hits:
            c, nc = bitstats(COAST, cid, byte, bit)
            b, nb = bitstats(BRAKE_MOVING, cid, byte, bit)
            if c is None or b is None or nc < 20 or nb < 20:
                print("  %-5s B%-4d %-4d %10s %10s  サンプル不足" % (cid, byte, bit, "-", "-"))
                continue
            if inv:
                c, b = 1 - c, 1 - b
            ok = c <= 0.2 and b >= 0.6
            print("  %-5s B%-4d %-4d %9.0f%% %9.0f%%  %s"
                  % (cid, byte, bit, c * 100, b * 100,
                     "★ブレーキ信号" if ok else "減速と相関しているだけ"))
    else:
        print("\n  対照区間 (惰行/制動) が無いため、減速との区別ができない。")
        print("  capture-brake.py を最後まで実行すること。")
