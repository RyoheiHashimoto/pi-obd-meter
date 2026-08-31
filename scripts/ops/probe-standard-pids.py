#!/usr/bin/env python3
# 標準PID (Mode 01) の対応状況を調べる。
#
#   sudo python3 probe-standard-pids.py
#
# エンジンをかけた状態で1回走らせるだけ。30秒ほどで終わる。
#
# 【2026-08-31 に実行済み。結果は scripts/ops/README.md を見ること】
#
# 大気圧 0x33、外気温 0x46、エンジン油温 0x5C はいずれも非対応だった。
# 絶対負荷 0x43 と触媒温度 0x3C は対応していた。
#
# 大気圧が非対応でも実害は無い。calcFuelEconomy は MAF を最優先で使い、
# 実機でも MAF 経路が動いている (maf_airflow から計算した値が
# fuel_rate_lh と完全一致することを確認済み)。MAF は空気の質量を直接
# 測るので標高の影響を自動で織り込む。atmosphericKPa のハードコードは
# MAF を持たない車で Speed-Density に落ちたときだけ効く死にコードである。
#
# 車種を変えたときや、PCM を交換したときに再実行する。
import subprocess, sys, threading, time

NAMES = {
    0x04: "エンジン負荷", 0x05: "水温", 0x06: "短期燃料トリム", 0x07: "長期燃料トリム",
    0x0B: "インマニ絶対圧", 0x0C: "RPM", 0x0D: "車速", 0x0E: "点火時期",
    0x0F: "吸気温", 0x10: "MAF", 0x11: "スロットル", 0x1F: "始動後経過時間",
    0x21: "MIL点灯後走行距離", 0x2F: "燃料レベル", 0x30: "DTCクリア後暖機回数",
    0x31: "DTCクリア後走行距離", 0x33: "★大気圧", 0x42: "電圧",
    0x43: "絶対負荷", 0x44: "当量比", 0x45: "相対スロットル",
    0x46: "★外気温", 0x47: "スロットルB", 0x49: "アクセルペダルD",
    0x4A: "アクセルペダルE", 0x4C: "指令スロットル", 0x5C: "エンジン油温",
}

resp = {}
lock = threading.Lock()

dump = subprocess.Popen(["candump", "can0,7E8:7FF"], stdout=subprocess.PIPE, text=True, bufsize=1)


def reader():
    for line in dump.stdout:
        f = line.split()
        di = next((i for i, t in enumerate(f) if t.startswith("[") and t.endswith("]")), -1)
        if di < 1:
            continue
        d = f[di + 1:]
        if len(d) < 3:
            continue
        try:
            if d[1] == "41":
                with lock:
                    resp[int(d[2], 16)] = [int(x, 16) for x in d[3:]]
            elif d[1] == "7F":
                with lock:
                    resp.setdefault(-1, []).append(d[2] if len(d) > 2 else "?")
        except ValueError:
            continue


threading.Thread(target=reader, daemon=True).start()


def ask(pid):
    subprocess.run(["cansend", "can0", "7DF#0201%02X0000000000" % pid], stderr=subprocess.DEVNULL)
    time.sleep(0.15)


# --- 1. 対応PIDビットマップ ---
print("=== 対応PIDビットマップ ===")
supported = set()
for base in (0x00, 0x20, 0x40, 0x60):
    with lock:
        resp.pop(base, None)
    ask(base)
    time.sleep(0.3)
    with lock:
        b = resp.get(base)
    if not b or len(b) < 4:
        print("  0x%02X: 応答なし" % base)
        continue
    val = (b[0] << 24) | (b[1] << 16) | (b[2] << 8) | b[3]
    got = [base + 1 + i for i in range(32) if val & (1 << (31 - i))]
    supported.update(got)
    print("  0x%02X: %s → %d個" % (base, " ".join("%02X" % x for x in b), len(got)))

if supported:
    print("\n  対応PID一覧:")
    for p in sorted(supported):
        star = " ←" if p in (0x33, 0x46, 0x5C, 0x43) else ""
        print("    %02X  %s%s" % (p, NAMES.get(p, ""), star))
    for p, label in ((0x33, "大気圧"), (0x46, "外気温"), (0x5C, "エンジン油温"), (0x43, "絶対負荷")):
        print("  %-12s %s" % (label, "★対応" if p in supported else "非対応"))

# --- 2. 実際に値を取る ---
print("\n=== 実値の取得 ===")
targets = sorted(supported) if supported else sorted(NAMES)
for p in targets:
    with lock:
        resp.pop(p, None)
    ask(p)
    with lock:
        b = resp.get(p)
    if not b:
        continue
    raw = " ".join("%02X" % x for x in b[:4])
    val = ""
    if p == 0x33:
        val = "= %d kPa  (標準大気圧 101.3、標高0m相当)" % b[0]
    elif p == 0x46:
        val = "= %d ℃" % (b[0] - 40)
    elif p == 0x5C:
        val = "= %d ℃" % (b[0] - 40)
    elif p == 0x05:
        val = "= %d ℃" % (b[0] - 40)
    elif p == 0x0F:
        val = "= %d ℃" % (b[0] - 40)
    elif p == 0x0B:
        val = "= %d kPa" % b[0]
    elif p == 0x10 and len(b) >= 2:
        val = "= %.2f g/s" % ((b[0] * 256 + b[1]) / 100.0)
    elif p == 0x43 and len(b) >= 2:
        val = "= %.1f %%" % ((b[0] * 256 + b[1]) * 100 / 255.0)
    print("  %02X %-14s %s  %s" % (p, NAMES.get(p, ""), raw, val))

dump.terminate()
print("\n完了。0x33 が対応なら configs/config.json ではなく fuel.go の")
print("atmosphericKPa を実測値に置き換える改修に進める。")
