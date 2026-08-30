#!/usr/bin/env python3
# Mode22 拡張PIDを巡回ポーリングし、解析に必要な行だけをCSVで書く。
#
# 記録先: /var/log/can-verify/poll22_YYYYmmdd_HHMMSS.csv
# systemd の poll22.service から常時起動する。ATF油温の同定が済んだら
# systemctl disable poll22 で止めてよい。
#
# 【生の candump を保存しないこと】
# 全フレームを保存すると 115MB/時 になり、その大半は解析に使わない
# ブロードキャスト (0x200/0x201/0x4B0/...) だった。車載機は毎回エンジン
# 停止で電源を失うため、書き込み続けること自体が破損リスクになる。
# 書く量を減らすことがそのまま安全性になる。応答1件につき1行だけ書く。
#
# 水温と車速はその時点の最新値を横に付ける。同定に必要な「同じ水温で
# 停車中と走行中を比べる」が、このファイル1つで完結する。
import json, os, signal, subprocess, sys, threading, time

LOG_DIR = "/var/log/can-verify"

# 直近のスキャン結果から有効な Mode22 PID を読む
scans = sorted(
    (f for f in os.listdir(LOG_DIR) if f.startswith("scan22_full")),
    key=lambda f: os.path.getmtime(os.path.join(LOG_DIR, f)),
)
if not scans:
    sys.exit("スキャン結果が無い")
pids = set()
with open(os.path.join(LOG_DIR, scans[-1]), errors="ignore") as f:
    for line in f:
        p = line.split()
        if len(p) > 8 and p[5] == "62":
            pids.add(p[6] + p[7])
pids = sorted(pids)
if not pids:
    sys.exit("有効PIDが見つからない")

out_path = os.path.join(LOG_DIR, "poll22_%s.csv" % time.strftime("%Y%m%d_%H%M%S"))
out = open(out_path, "w", buffering=1)
out.write("t,pid,b1,b2,coolant,speed\n")
print("対象PID %d 個 → %s" % (len(pids), out_path), flush=True)

# 受信は1本の candump をパイプで読む。ディスクには書かない。
dump = subprocess.Popen(
    ["candump", "-t", "d", "can0,7E8:7FF,420:7FF,201:7FF"],
    stdout=subprocess.PIPE, text=True, bufsize=1,
)


def stop(*_):
    dump.terminate()
    out.close()
    sys.exit(0)


signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)


def sender():
    while True:
        for p in pids:
            subprocess.run(["cansend", "can0", "7E0#0322%s00000000" % p],
                           stderr=subprocess.DEVNULL)
            time.sleep(0.03)
        # 標準PID。05=水温 0F=吸気温 10=MAF 0B=MAP
        # 06/07=燃料トリム 0E=点火時期 42=電圧
        for p in ("05", "0F", "10", "0B", "06", "07", "0E", "42"):
            subprocess.run(["cansend", "can0", "7DF#0201%s0000000000" % p],
                           stderr=subprocess.DEVNULL)
            time.sleep(0.03)


threading.Thread(target=sender, daemon=True).start()

coolant = 0.0
speed = 0.0
for line in dump.stdout:
    f = line.split()
    # 書式はオプションで前後する (タイムスタンプの有無など) ので、
    # データ長を示す [N] の位置を探して、そこを基準に切り出す。
    di = -1
    for i, tok in enumerate(f):
        if tok.startswith("[") and tok.endswith("]"):
            di = i
            break
    if di < 1:
        continue
    cid = f[di - 1]
    d = f[di + 1:]
    try:
        if cid == "420" and len(d) >= 1:
            coolant = int(d[0], 16) - 40
        elif cid == "201" and len(d) >= 6:
            speed = max(0.0, (int(d[4], 16) * 256 + int(d[5], 16) - 10000) / 100.0)
        elif cid == "7E8" and len(d) >= 4 and d[1] == "41":
            # 標準PID (Mode 01) の応答。従来は要求だけ出して応答を捨てていた。
            # 燃料トリムと点火時期はエンジンの健康診断に要る。
            b2 = int(d[4], 16) if len(d) >= 5 else 0
            out.write("%.2f,01%s,%d,%d,%.0f,%.1f\n" %
                      (time.time(), d[2], int(d[3], 16), b2, coolant, speed))
        elif cid == "7E8" and len(d) >= 6 and d[1] == "62":
            out.write("%.2f,%s%s,%d,%d,%.0f,%.1f\n" %
                      (time.time(), d[2], d[3], int(d[4], 16), int(d[5], 16), coolant, speed))
    except (ValueError, IndexError):
        continue
