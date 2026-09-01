#!/usr/bin/env python3
# 停車・アイドルのまま、装備を入切して未同定PIDを探す。
#
#   sudo python3 probe-toggle.py
#
# 【なぜこの形か】
# 2026-08-31〜09-01 に7個同定できたが、成功したのはすべて「他が動かない
# 状態で、ひとつだけを断続させた」場合だった。
#
#   ブレーキ … 停車・アイドル・アクセル全閉で 20秒踏む→離す→20秒踏む
#   エアコン … 夜間走行中の入切が、アイドル停車中の記録に残っていた
#
# 逆に、走行データの相関だけを見た絞り込みは外した。平地データから
# 勾配候補を2個に絞ったが、実際の8.9%登坂ではどちらも動かず、正解は
# 候補外だった。夜間走行と昼間走行の比較でヘッドライトを探した時も、
# 気温・交通・走り方が同時に変わるため何も出なかった。
#
# だから「停車・アイドル・他は触らない」を厳守する。
import json, os, subprocess, sys, time, urllib.request

API = "http://localhost:9090/api/realtime"
OUT_DIR = "/var/log/can-verify"

STEPS = [
    ("ヘッドライト",     "ロービームを点灯"),
    ("リアデフォッガー", "リアデフォッガーのスイッチを入れる"),
    ("ブロワー最強",     "エアコンの風量を最強にする (A/Cスイッチは触らない)"),
    ("ステアリング据え切り", "ハンドルを右いっぱいまで回す"),
    ("Nで空吹かし",      "シフトをNにして、2000〜3000rpm を保つ"),
]


def speed():
    try:
        return float(json.load(urllib.request.urlopen(API, timeout=2)).get("speed_kmh") or 0)
    except Exception:
        return -1.0


if os.geteuid() != 0:
    sys.exit("sudo で実行すること")

s = speed()
if s < 0:
    sys.exit("メーターの API に繋がらない。pi-obd-meter は動いているか")
if s > 1:
    sys.exit("停車していない (%.1f km/h)。安全な場所に停めてから実行すること" % s)

os.makedirs(OUT_DIR, exist_ok=True)
label_path = os.path.join(OUT_DIR, "toggle_%s.labels" % time.strftime("%Y%m%d_%H%M%S"))
labels = open(label_path, "w", buffering=1)
labels.write("t,label\n")

print("ラベル記録先: %s" % label_path)
print()
print("【前提】停車・アイドリング・アクセルから足を離す。")
print("　　　　指示されたもの以外は触らない。窓もドアも開けない。")
print("　　　　poll22 が動いていること (systemctl is-active poll22)。")
print()
print("各項目は 20秒ON → 20秒OFF → 20秒ON。1項目あたり1分。")
print("Enter を押してから操作すること。押した時刻がラベルになる。")
print()
input("準備ができたら Enter > ")


def mark(name):
    t = time.time()
    labels.write("%.3f,%s\n" % (t, name))
    return t


try:
    for i, (name, how) in enumerate(STEPS, 1):
        print("\n--- %d/%d  %s ---" % (i, len(STEPS), name))
        print("    %s" % how)
        for phase, sec in (("ON", 20), ("OFF", 20), ("ON(2回目)", 20)):
            act = "実施" if phase.startswith("ON") else "元に戻す"
            input("    [%s] %s したら Enter > " % (phase, act))
            t = mark("%s %s" % (name, phase))
            print("        記録 %s  … %d秒そのまま待つ" % (time.strftime("%H:%M:%S", time.localtime(t)), sec))
            time.sleep(sec)
        mark("%s 終了" % name)
        print("    完了。元の状態に戻したか確認すること。")
except KeyboardInterrupt:
    print("\n中断した。ここまでのラベルは残る。")

labels.write("%.3f,記録終了\n" % time.time())
labels.close()

print("\n完了: %s" % label_path)
print("\nMac へ回収して解析:")
print("  scp laurel@<pi>:%s ." % label_path)
print("  scp laurel@<pi>:/var/log/can-verify/poll22_*.csv .")
print("  python3 scripts/ops/find-toggle-pid.py toggle_*.labels poll22_*.csv")
