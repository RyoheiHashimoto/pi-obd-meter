#!/usr/bin/env python3
# IMU (GY-521 / MPU-6050) を IIO バッファ経由で 100Hz 記録する。
#
# 【sysfs の1回読みを使ってはいけない】
# in_accel_x_raw のような1回読みは、ドライバが読むたびにセンサーの電源を
# 入れ直すため、ジャイロが安定する前の値を拾う。2026-09-06 の実測では
# 3回の測定すべてで X 軸に -129 deg/s という同じ外れ値が現れた。
# 加速度計は安定が速いので影響を受けず、静止時 1G を正しく返していた。
# バッファ経由なら電源が入ったまま一定間隔で流れてくるので外れ値が出ない。
# 100Hz が出せるのもこちらだけ (1回読みは 10Hz が限界)。
#
# 記録先: /var/log/imu/imu-MMDD-HHMM.csv (→ /data/log/imu, SSD)
import os, signal, struct, sys, time

DEV     = "/sys/bus/iio/devices/iio:device0"
NODE    = "/dev/iio:device0"
OUT_DIR = "/var/log/imu"
RATE    = 100
TRIGGER = "mpu6050-dev0"
BUFLEN  = 128
# 起動直後に捨てるサンプル数。
# センサーを起こした直後のジャイロは過渡値を返す。2026-09-06 の実測では
# 1〜2 サンプル目が (-128.9, -93.2, -90.8) deg/s、6 サンプル目で ±3 deg/s に
# 収束した。静止しているのだから正しい値は 0 付近である。余裕を見て 1 秒捨てる。
WARMUP  = 100

# 1レコードの構造。scan_elements の index 順に詰まり、int64 の
# タイムスタンプは 8 バイト境界に揃うためパディングが入る。
#   0..13  accel x,y,z / temp / gyro x,y,z   (be:s16 が7個)
#   14..15 パディング
#   16..23 timestamp (le:s64, ナノ秒)
HEAD   = struct.Struct(">7h")
TS     = struct.Struct("<q")
RECLEN = 24

DEG = 57.29577951308232


def w(path, val):
    with open(os.path.join(DEV, path), "w") as f:
        f.write(str(val))


def r(path, default=None):
    try:
        with open(os.path.join(DEV, path)) as f:
            return f.read().strip()
    except OSError:
        return default


def setup():
    # 設定変更の前に必ずバッファを止める。動作中は書き込みを拒否される。
    try:
        w("buffer/enable", 0)
    except OSError:
        pass
    w("sampling_frequency", RATE)
    for ch in ("in_accel_x", "in_accel_y", "in_accel_z", "in_temp",
               "in_anglvel_x", "in_anglvel_y", "in_anglvel_z", "in_timestamp"):
        w("scan_elements/%s_en" % ch, 1)
    w("trigger/current_trigger", TRIGGER)
    w("buffer/length", BUFLEN)
    w("buffer/enable", 1)


def main():
    a_scale = float(r("in_accel_scale", "0.000598"))
    g_scale = float(r("in_anglvel_scale", "0.001064724")) * DEG
    t_scale = float(r("in_temp_scale", "2.941176"))
    t_off   = float(r("in_temp_offset", "12420"))

    setup()

    os.makedirs(OUT_DIR, exist_ok=True)
    # 既存ファイルは絶対に開かない。RTC が無く overlayfs で timesyncd の
    # 保存時刻も消えるため、毎回ほぼ同じ時刻から起動して同名になりうる。
    base = os.path.join(OUT_DIR, "imu-%s" % time.strftime("%m%d-%H%M"))
    path, n = base + ".csv", 0
    while os.path.exists(path):
        n += 1
        path = "%s-%d.csv" % (base, n)

    # 64KB も溜めると SIGTERM で殺されたときに丸ごと失う。
    # 8KB なら 100Hz で 1 秒強ごとに落ちる。
    f = open(path, "w", buffering=8192)
    # mono は起動からの秒数。壁時計が NTP や GPS で飛んでも影響を受けないので、
    # 経過時間の計算には必ずこちらを使うこと。
    f.write("# accel_scale=%g gyro_scale_dps=%g rate=%d\n" % (a_scale, g_scale, RATE))
    f.write("t,mono,ax,ay,az,gx,gy,gz,temp\n")
    print("記録先: %s" % path, flush=True)

    stop = []
    def on_term(_sig, _frm):
        stop.append(True)
    signal.signal(signal.SIGTERM, on_term)
    signal.signal(signal.SIGINT, on_term)

    t0_real = time.time()
    t0_mono = time.monotonic()
    dev = open(NODE, "rb", buffering=0)
    buf = b""
    skipped = 0
    last_flush = time.monotonic()
    try:
        while not stop:
            chunk = dev.read(RECLEN * 16)
            if not chunk:
                continue
            buf += chunk
            while len(buf) >= RECLEN:
                rec, buf = buf[:RECLEN], buf[RECLEN:]
                ax, ay, az, tp, gx, gy, gz = HEAD.unpack_from(rec, 0)
                ts, = TS.unpack_from(rec, 16)
                if skipped < WARMUP:
                    skipped += 1
                    continue
                t = ts / 1e9
                f.write("%.4f,%.4f,%.4f,%.4f,%.4f,%.3f,%.3f,%.3f,%.1f\n" % (
                    t, t0_mono + (t - t0_real),
                    ax * a_scale, ay * a_scale, az * a_scale,
                    gx * g_scale, gy * g_scale, gz * g_scale,
                    (tp + t_off) * t_scale / 1000.0))
            now = time.monotonic()
            if now - last_flush > 2.0:
                f.flush()
                last_flush = now
    except KeyboardInterrupt:
        pass
    finally:
        f.close()
        try:
            w("buffer/enable", 0)
        except OSError:
            pass


if __name__ == "__main__":
    main()
