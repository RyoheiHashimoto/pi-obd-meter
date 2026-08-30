#!/usr/bin/env python3
# GPS (u-blox 7 / NMEA) を記録する。
#
# 記録先: /var/log/gps/gps-MMDD-HHMM.csv
# systemd の gps-log.service から常時起動する。
#
# 【なぜ要るか】
# 加速度からトルクを逆算するとき、勾配と「最高速付近で加速しない状態」を
# 区別できない。実際 2026-08-29 の解析では、逆算した勾配が中央値 +5.8%、
# 最大 +15.9% となり成立しなかった (高速道路の勾配は通常 2〜3%)。
# 標高が取れれば勾配が直接求まり、トルク推定から重力成分を分離できる。
#
# 【書き込み量】
# NMEA は毎秒出るが、記録は 1行/秒 で約 70バイト = 0.25 MB/時。
# 走行ログ (1.5 MB/時) や poll22 (3 MB/時) に比べれば無視できる。
import csv
import os
import signal
import sys
import time

DEV = os.environ.get("GPS_DEV", "/dev/ttyACM0")
OUT_DIR = "/var/log/gps"


def nmea_deg(v, hemi):
    """NMEA の ddmm.mmmm を10進度に直す。空なら None。"""
    if not v or not hemi:
        return None
    try:
        dot = v.index(".")
    except ValueError:
        return None
    deg = float(v[:dot - 2])
    minutes = float(v[dot - 2:])
    d = deg + minutes / 60.0
    return -d if hemi in ("S", "W") else d


def checksum_ok(line):
    """NMEA のチェックサムを検証する。壊れた行を弾く。"""
    if "*" not in line:
        return False
    body, _, cs = line[1:].partition("*")
    try:
        want = int(cs[:2], 16)
    except ValueError:
        return False
    got = 0
    for ch in body:
        got ^= ord(ch)
    return got == want


def main():
    os.makedirs(OUT_DIR, exist_ok=True)
    path = os.path.join(OUT_DIR, "gps-%s.csv" % time.strftime("%m%d-%H%M"))
    f = open(path, "w", buffering=1)
    w = csv.writer(f)
    w.writerow(["t", "fix", "sats", "hdop", "lat", "lon", "alt_m", "speed_kmh", "course"])
    print("記録先: %s" % path, flush=True)

    def stop(*_):
        f.close()
        sys.exit(0)

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)

    # 最新の値を持ち回り、GGA を受けた時点で1行書く。
    # GGA に速度が無く、VTG に標高が無いため、両方を合わせる必要がある。
    speed = course = 0.0
    last_write = 0.0

    while True:
        try:
            with open(DEV, "r", errors="ignore") as dev:
                for line in dev:
                    line = line.strip()
                    if not line.startswith("$") or not checksum_ok(line):
                        continue
                    p = line.split(",")
                    if p[0].endswith("VTG") and len(p) > 7:
                        try:
                            course = float(p[1]) if p[1] else course
                            speed = float(p[7]) if p[7] else 0.0
                        except ValueError:
                            pass
                    elif p[0].endswith("GGA") and len(p) > 9:
                        now = time.time()
                        # GGA は毎秒来る。1秒に1行で十分。
                        if now - last_write < 0.9:
                            continue
                        last_write = now
                        fix = p[6] or "0"
                        sats = p[7] or "0"
                        hdop = p[8] or ""
                        lat = nmea_deg(p[2], p[3])
                        lon = nmea_deg(p[4], p[5])
                        alt = p[9] or ""
                        w.writerow([
                            "%.2f" % now, fix, sats, hdop,
                            "%.6f" % lat if lat is not None else "",
                            "%.6f" % lon if lon is not None else "",
                            alt, "%.1f" % speed, "%.1f" % course,
                        ])
        except (OSError, IOError):
            # USB が抜けた・再認識された。開き直す。
            time.sleep(2)


if __name__ == "__main__":
    main()
