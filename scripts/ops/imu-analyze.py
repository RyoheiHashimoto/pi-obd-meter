#!/usr/bin/env python3
# IMU の取付角を較正し、車両座標系での勾配・横G・前後Gを出す。
#
#   sudo ./imu-analyze.py /data/log/imu/imu-MMDD-HHMM.csv \
#                         /data/log/drive-verify/drive-MMDD-HHMM.csv
#
# 同じ走行の IMU ログと走行ログを渡す。ファイル名の時刻は揃っている。
# IMU の取付角を停車中の重力から求め、車両座標系での前後加速度・横Gを出す。
#
# 手順 (#165 の実装項目):
#   1. 停車中 (車速0) の平均加速度 = 重力ベクトル → 「下」が決まる
#   2. 重力に直交する平面へ射影し、車速の微分と最も相関する向きを「前」とする
#   3. 前後加速度から車速微分を引いた残差が g·sinθ → 勾配
#   4. 「前」と「下」の外積が「横」→ 横G
import csv, math, sys

imu_path, drv_path = sys.argv[1], sys.argv[2]
G = 9.80665

# --- 車速を読む (5Hz) ---
def load_speed(p):
    """走行ログから (時刻, 車速) を読む。

    列番号ではなくヘッダ名で引く。drive-verify.py の列は増えることがあり
    (14 -> 27 -> 28)、位置で読むと足された日に静かに壊れる。2026-09-09 に
    tm 列を足したとき、index 1 に入れていたら車速が単調時刻として読まれ、
    時速 1000km 相当の値になるところだった。
    電断で末尾が NUL になっているファイルがあるので、それも落とす。
    """
    out = []
    with open(p, errors="replace") as fh:
        r = csv.reader(line.replace("\0", "") for line in fh)
        try:
            hdr = next(r)
        except StopIteration:
            return out
        if "t" not in hdr or "speed" not in hdr:
            return out
        it, isp = hdr.index("t"), hdr.index("speed")
        for row in r:
            if len(row) != len(hdr):
                continue
            try:
                out.append((float(row[it]), float(row[isp])))
            except ValueError:
                pass
    out.sort()
    return out


speed = load_speed(drv_path)          # (t, kmh)
if len(speed) < 100:
    print("  車速データ不足"); sys.exit(1)

def speed_at(t):
    lo, hi = 0, len(speed)-1
    if t <= speed[0][0] or t >= speed[-1][0]:
        return None
    while hi - lo > 1:
        mid = (lo+hi)//2
        if speed[mid][0] <= t: lo = mid
        else: hi = mid
    t0,v0 = speed[lo]; t1,v1 = speed[hi]
    if t1 == t0: return v0
    if t1 - t0 > 2.0: return None
    return v0 + (v1-v0)*(t-t0)/(t1-t0)

# --- IMU を読む (100Hz) ---
imu = []
for line in open(imu_path, errors='replace'):
    if line.startswith('#') or line.startswith('t,'):
        continue
    p = line.rstrip('\n').split(',')
    if len(p) < 9: continue
    try:
        imu.append((float(p[0]), float(p[2]), float(p[3]), float(p[4]),
                    float(p[5]), float(p[6]), float(p[7]), float(p[8])))
    except ValueError:
        pass
if len(imu) < 1000:
    print("  IMUデータ不足"); sys.exit(1)

print("  IMU %d サンプル / 車速 %d サンプル" % (len(imu), len(speed)))
print("  期間 %.1f 分" % ((imu[-1][0]-imu[0][0])/60))

# --- 1. 停車中の重力ベクトル ---
still = [(a,b,c) for (t,a,b,c,_,_,_,_) in imu
         if (lambda v: v is not None and v < 0.5)(speed_at(t))]
if len(still) < 500:
    print("  停車中のサンプルが足りない (%d)" % len(still)); sys.exit(1)
gx = sum(s[0] for s in still)/len(still)
gy = sum(s[1] for s in still)/len(still)
gz = sum(s[2] for s in still)/len(still)
gmag = math.sqrt(gx*gx+gy*gy+gz*gz)
print()
print("  ── 取付角の較正 (停車 %d サンプル) ──" % len(still))
print("    重力ベクトル  X %+7.4f  Y %+7.4f  Z %+7.4f  m/s^2" % (gx,gy,gz))
print("    大きさ        %.4f m/s^2 = %.4f G" % (gmag, gmag/G))
down = (gx/gmag, gy/gmag, gz/gmag)

# --- 2. 前方向を車速微分との相関で決める ---
# 重力成分を除いた水平面の加速度と、車速の微分を突き合わせる。
dot = lambda a,b: a[0]*b[0]+a[1]*b[1]+a[2]*b[2]
samples = []
HALF = 0.5   # 車速微分の窓の半幅 (秒)。車速は5Hzなので短いと補間誤差を拾う。
for i in range(len(imu)):
    t = imu[i][0]
    v0, v1 = speed_at(t-HALF), speed_at(t+HALF)
    if v0 is None or v1 is None: continue
    dv = (v1-v0)/3.6/(2*HALF)                # m/s^2
    if abs(dv) < 0.3: continue               # 加減速している場面だけ使う
    a = (imu[i][1], imu[i][2], imu[i][3])
    h = tuple(a[k] - down[k]*dot(a,down) for k in range(3))   # 水平成分
    samples.append((dv, h))
if len(samples) < 200:
    print("  加減速サンプルが足りない (%d)" % len(samples)); sys.exit(1)

fwd = [sum(s[0]*s[1][k] for s in samples) for k in range(3)]
fmag = math.sqrt(sum(v*v for v in fwd))
fwd = tuple(v/fmag for v in fwd)
left = (down[1]*fwd[2]-down[2]*fwd[1], down[2]*fwd[0]-down[0]*fwd[2], down[0]*fwd[1]-down[1]*fwd[0])

print()
print("  ── 車両座標系 (%d の加減速サンプルから決定) ──" % len(samples))
print("    前   %+.3f %+.3f %+.3f" % fwd)
print("    横   %+.3f %+.3f %+.3f" % left)
print("    下   %+.3f %+.3f %+.3f" % down)

# --- 3. 勾配と横G ---
raw=[]
for i in range(len(imu)):
    t=imu[i][0]
    v0,v1 = speed_at(t-HALF), speed_at(t+HALF)
    v = speed_at(t)
    if v0 is None or v1 is None or v is None: continue
    dv=(v1-v0)/3.6/(2*HALF)
    a=(imu[i][1],imu[i][2],imu[i][3])
    raw.append((t, dot(a,fwd)-dv, dot(a,left), dv, v))

# 勾配は路面の凹凸より遥かにゆっくり変化する。2秒の移動平均で振動を落とす。
WIN = 200   # 100Hz × 2秒
grades=[]; lats=[]; longs=[]
acc=0.0
for i,(t,resid,a_l,dv,v) in enumerate(raw):
    lats.append(abs(a_l)/G); longs.append(dv/G)
    lo=max(0,i-WIN//2); hi=min(len(raw), i+WIN//2)
    m=sum(r[1] for r in raw[lo:hi])/(hi-lo)
    s=max(-1.0,min(1.0,m/G))
    grades.append((t, math.degrees(math.asin(s)), math.tan(math.asin(s))*100, v))

if not grades:
    print("  勾配を計算できるサンプルが無い"); sys.exit(1)
gp=[x[2] for x in grades]
gp_sorted=sorted(gp)
q=lambda p: gp_sorted[int(len(gp_sorted)*p)]
print()
print("  ── 勾配 (%d サンプル / 2秒移動平均) ──" % len(gp))
print("    中央値 %+.2f %%   5%%点 %+.2f %%   95%%点 %+.2f %%" % (q(0.5), q(0.05), q(0.95)))
print("    最小 %+.2f %%   最大 %+.2f %%" % (gp_sorted[0], gp_sorted[-1]))
ls=sorted(lats)
print()
print("  ── 横G (%d サンプル) ──" % len(ls))
print("    中央値 %.3f G   95%%点 %.3f G   最大 %.3f G" % (ls[len(ls)//2], ls[int(len(ls)*0.95)], ls[-1]))
lo=sorted(longs)
print()
print("  ── 前後G (車速微分) ──")
print("    最大加速 %+.3f G   最大減速 %+.3f G" % (lo[-1], lo[0]))
