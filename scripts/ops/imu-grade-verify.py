#!/usr/bin/env python3
# IMU から出した勾配を GPS の標高変化で独立に検証する。
#
#   sudo ./imu-grade-verify.py imu-*.csv drive-*.csv gps-*.csv
#
# 2026-09-06 の 20km 走行で 20区間を比較し、相関 0.835 / 差の平均 -0.69% /
# ばらつき 0.89% を得た。差の平均は「停車中＝水平」という較正の前提から来る
# (停めた場所が傾いていればそのぶんずれる)。
# IMU から出した勾配を、GPS の標高変化で独立に検証する。
#
# GPS の標高は誤差が数メートルあるため1点ずつでは使えない。
# 60秒の窓で「標高 対 走行距離」を最小二乗で直線近似し、その傾きを勾配とする。
# 同じ窓の IMU 勾配 (2秒移動平均済み) と突き合わせる。
import csv, math, sys

imu_path, drv_path, gps_path = sys.argv[1], sys.argv[2], sys.argv[3]
G = 9.80665
WINDOW = 60.0      # 秒
HALF   = 0.5       # 車速微分の半幅

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
speed = load_speed(drv_path)

def speed_at(t):
    lo,hi=0,len(speed)-1
    if t<=speed[0][0] or t>=speed[-1][0]: return None
    while hi-lo>1:
        m=(lo+hi)//2
        if speed[m][0]<=t: lo=m
        else: hi=m
    t0,v0=speed[lo]; t1,v1=speed[hi]
    if t1-t0>2.0: return None
    return v0 if t1==t0 else v0+(v1-v0)*(t-t0)/(t1-t0)

imu=[]
for line in open(imu_path, errors='replace'):
    if line[0]=='#' or line[0]=='t': continue
    p=line.rstrip('\n').split(',')
    if len(p)<9: continue
    try: imu.append((float(p[0]),float(p[2]),float(p[3]),float(p[4])))
    except ValueError: pass

# 較正
dot=lambda a,b:a[0]*b[0]+a[1]*b[1]+a[2]*b[2]
still=[(a,b,c) for (t,a,b,c) in imu if (lambda v:v is not None and v<0.5)(speed_at(t))]
gx=sum(s[0] for s in still)/len(still); gy=sum(s[1] for s in still)/len(still); gz=sum(s[2] for s in still)/len(still)
gm=math.sqrt(gx*gx+gy*gy+gz*gz); down=(gx/gm,gy/gm,gz/gm)
sm=[]
for (t,ax,ay,az) in imu:
    v0,v1=speed_at(t-HALF),speed_at(t+HALF)
    if v0 is None or v1 is None: continue
    dv=(v1-v0)/3.6/(2*HALF)
    if abs(dv)<0.3: continue
    a=(ax,ay,az); h=tuple(a[k]-down[k]*dot(a,down) for k in range(3))
    sm.append((dv,h))
fwd=[sum(s[0]*s[1][k] for s in sm) for k in range(3)]
fn=math.sqrt(sum(v*v for v in fwd)); fwd=tuple(v/fn for v in fwd)

# IMU 勾配 (生の残差、時刻つき)
resid=[]
for (t,ax,ay,az) in imu:
    v0,v1=speed_at(t-HALF),speed_at(t+HALF)
    if v0 is None or v1 is None: continue
    dv=(v1-v0)/3.6/(2*HALF)
    resid.append((t, dot((ax,ay,az),fwd)-dv))

# GPS
gps=[]
for row in csv.reader(open(gps_path, errors='replace')):
    if len(row)<8 or not row[0].replace('.','',1).isdigit(): continue
    try:
        t=float(row[0]); fix=int(row[1]); hdop=float(row[3]) if row[3] else 99
        if fix<1 or hdop>5 or not row[6]: continue
        gps.append((t, float(row[6])))
    except ValueError: pass
print("  GPS 測位できたサンプル %d / IMU %d / 車速 %d" % (len(gps), len(imu), len(speed)))
if len(gps)<120:
    print("  GPS が足りない"); sys.exit(1)

# 走行距離の累積 (車速から)
def dist_between(t0,t1):
    d=0.0; t=t0
    while t<t1:
        v=speed_at(t)
        if v is None: return None
        step=min(1.0,t1-t); d+=v/3.6*step; t+=step
    return d

print()
print("  ── 60秒ごとの比較 ──")
print("    時刻       GPS勾配   IMU勾配    差     距離   標高変化")
rows=[]
t0=gps[0][0]
while t0+WINDOW <= gps[-1][0]:
    t1=t0+WINDOW
    seg=[g for g in gps if t0<=g[0]<=t1]
    if len(seg)<30: t0+=WINDOW; continue
    d=dist_between(t0,t1)
    if d is None or d<200: t0+=WINDOW; continue   # 200m未満は誤差が支配的
    dalt=seg[-1][1]-seg[0][1]
    gps_grade=dalt/d*100
    r=[x[1] for x in resid if t0<=x[0]<=t1]
    if len(r)<1000: t0+=WINDOW; continue
    m=sum(r)/len(r); s=max(-1.0,min(1.0,m/G))
    imu_grade=math.tan(math.asin(s))*100
    rows.append((t0,gps_grade,imu_grade,d,dalt))
    t0+=WINDOW

import time
for t,gg,ig,d,da in rows:
    print("    %s  %+6.2f%%  %+6.2f%%  %+6.2f  %5.0fm  %+6.1fm" % (
        time.strftime('%H:%M:%S',time.localtime(t)), gg, ig, ig-gg, d, da))
if len(rows)>=3:
    diffs=[r[2]-r[1] for r in rows]
    mean=sum(diffs)/len(diffs)
    sd=math.sqrt(sum((x-mean)**2 for x in diffs)/len(diffs))
    gs=[r[1] for r in rows]; is_=[r[2] for r in rows]
    mg=sum(gs)/len(gs); mi=sum(is_)/len(is_)
    cov=sum((gs[i]-mg)*(is_[i]-mi) for i in range(len(gs)))
    sg=math.sqrt(sum((x-mg)**2 for x in gs)); si=math.sqrt(sum((x-mi)**2 for x in is_))
    print()
    print("  ── まとめ (%d区間) ──" % len(rows))
    print("    差の平均 %+.2f %%   ばらつき %.2f %%" % (mean, sd))
    if sg>0 and si>0:
        print("    相関係数 %.3f" % (cov/(sg*si)))
