#!/usr/bin/env python3
# 走行データを1行/0.2秒でCSVに記録する。
#
# 記録先: /var/log/drive-verify/drive-MMDD-HHMM.csv
# systemd の drive-verify.service から常時起動する。
#
# 【hold と range を必ず記録すること】
# 当初はギアと車速しか記録していなかったため、「負荷が上がったときに3速へ
# 落ちていた」という観測が、AT の自動変速なのか運転者の手動操作なのかを
# 区別できなかった。DY デミオは HOLD スイッチとレンジ操作で任意にギアを
# 固定できるため、ギア段だけを見て AT の制御を語ることはできない。
# 実際この走行の2〜3速は、多くがキックダウンを嫌っての手動操作だった。
import json, os, time, urllib.request

MECH = {1: 2.816, 2: 1.498, 3: 1.000, 4: 0.726}
OUT_DIR = "/var/log/drive-verify"
API = "http://localhost:9090/api/realtime"

os.makedirs(OUT_DIR, exist_ok=True)
path = os.path.join(OUT_DIR, "drive-%s.csv" % time.strftime("%m%d-%H%M"))
f = open(path, "w", buffering=1)
f.write("t,speed,rpm,gear,engaged,ratio,mech,slip,tcc,locked,hold,range,shifting,"
        "atf,volt,odo,trip_km,fuel_pt,rate_lh,eco,coolant,map,load,"
        "brake,fan,ac,grade\n")
print("記録先: %s" % path, flush=True)

while True:
    try:
        d = json.load(urllib.request.urlopen(API, timeout=2))
    except Exception:
        time.sleep(0.5)
        continue
    g = d.get("gear") or 0
    # 実際に噛んでいるギア。gear は目標ギアで、変速完了までズレる。
    eng = d.get("engaged_gear") or 0
    mech = MECH.get(eng, 0)
    r = d.get("gear_ratio") or 0
    # 滑り比はアプリが算出したものを使う。
    #
    # 以前はここで gear_ratio / mech を計算していたが、これは常に 1.0 になる。
    # CAN 0x230 B2 (gear_ratio) は機械ギア比であって滑りを含まない (#132/#133 で
    # 確定済み) ため、同じ値を同じ値で割っていた。3日分・40万サンプルの slip 列が
    # すべて 1.0000 で、「トルコンは常に直結」という誤った読みを生みかけた。
    #
    # 正しい滑りは rpm / (車速 × 機械ギア比 × k) で、k はロックアップ中に学習する。
    # アプリの SlipCalibrator がそれを持っているので、API から受け取る。
    slip = d.get("slip_ratio") or 0
    f.write("%.1f,%.2f,%.1f,%d,%d,%.3f,%.3f,%.4f,%s,%s,%s,%s,%s,%.1f,%.2f,%.0f,%.5f,%.2f,%.3f,%.2f,%.1f,%.1f,%.1f,%s,%s,%s,%d\n" % (
        time.time(),
        d.get("speed_kmh") or 0, d.get("rpm") or 0, g, eng, r, mech, slip,
        d.get("tcc_lock_pct") or 0, d.get("tc_locked"),
        d.get("hold"), d.get("at_range_str"), d.get("shifting"),
        d.get("atf_temp_c") or 0,
        d.get("voltage") or 0,
        d.get("odometer_can_km") or 0,
        d.get("trip_km") or 0, d.get("elec_b0_pct") or 0,
        # 燃料消費レート (L/h)。これを時間積分すれば消費量が出る。
        # 燃料計の pt と対にすることで「残量域ごとの L/pt」が求まり、
        # センダーの非線形を含んだ変換表を走行データだけから作れる。
        # 給油記録と違い、上端で振り切れている区間を避けて集められる。
        d.get("fuel_rate_lh") or 0, d.get("avg_fuel_economy") or 0,
        d.get("coolant_temp") or 0, d.get("intake_map") or 0,
        d.get("engine_load") or 0,
        # 2026-09-01 に同定した信号 (#150)。ブレーキ・ファン・エアコンは真偽値、
        # 勾配は符号付きの生値で単位未確定 (負が登り)。
        d.get("brake_pedal"), d.get("radiator_fan"), d.get("ac_compressor"),
        d.get("grade_raw") or 0,
    ))
    # 0.2秒周期。加速度を差分から求めるため、0.5秒では全開加速のサンプルが
    # 数点しか取れずトルク推定の分解能が足りなかった。書き込み量は
    # 0.6MB/時 → 1.5MB/時 で、SDへの負担は許容範囲。
    time.sleep(0.2)
