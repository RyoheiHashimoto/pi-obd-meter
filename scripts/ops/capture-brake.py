#!/usr/bin/env python3
# ブレーキ信号を探すための、操作ラベル付き CAN キャプチャ。
#
#   sudo python3 capture-brake.py
#
# 画面の指示どおりに操作し、各操作の開始時に Enter を押す。押した時刻が
# ラベルとして記録されるので、あとから「踏んだ瞬間に変わったビット」を
# 機械的に探せる。
#
# 【なぜラベルが要るか】
# 手持ちの生ログ3本のうち走行を含むのは1本だけで、最高17km/hの駐車場
# 走行しかなかった。0x200 (80,244フレーム流れているのに未デコード) と
# 減速度を照合しても相関が全部弱く (r=+0.20/-0.30/+0.08)、決着しなかった。
#
# 減速そのものではなく「ブレーキ操作」に連動するビットを見分けたい。
# そのために「踏まずにアクセルオフだけで減速する」手順を必ず含める。
# これが無いと、単に減速と相関するだけのビットを誤って掴む。
import os, subprocess, sys, time

if os.geteuid() != 0:
    sys.exit("sudo で実行すること")

OUT_DIR = "/var/log/can-verify"
os.makedirs(OUT_DIR, exist_ok=True)
stamp = time.strftime("%Y%m%d_%H%M%S")
dump_path = os.path.join(OUT_DIR, "brake_%s.log" % stamp)
label_path = os.path.join(OUT_DIR, "brake_%s.labels" % stamp)

STEPS = [
    ("停車・ブレーキを踏む",        "踏んだまま5秒待つ"),
    ("停車・ブレーキを離す",        "離したまま5秒待つ"),
    ("停車・ブレーキを踏む(2回目)", "踏んだまま5秒待つ"),
    ("停車・ブレーキを離す(2回目)", "離したまま5秒待つ"),
    ("停車・ブレーキを踏む(3回目)", "踏んだまま5秒待つ"),
    ("停車・ブレーキを離す(3回目)", "離したまま5秒待つ"),
    ("★アクセルオフのみで減速",     "40km/hから、ブレーキを踏まずに惰行で減速する"),
    ("フットブレーキで緩やかに減速", "40km/hから、軽くブレーキだけで減速する"),
    ("★アクセルオフのみで減速(2)",  "もう一度、ブレーキを踏まずに惰行"),
    ("フットブレーキで強めに減速",   "安全な範囲で、はっきり踏んで減速する"),
    ("サイドブレーキを引く",        "停車してから。引いたまま5秒"),
    ("サイドブレーキを解除",        "解除して5秒"),
]

print("記録先: %s" % dump_path)
print("ラベル: %s" % label_path)
print()
print("【安全第一】広い駐車場か、交通のない道で行うこと。")
print("同乗者に操作してもらえるなら、そのほうが安全。")
print("途中でやめる場合は Ctrl-C。それまでの記録は残る。")
print()
input("準備ができたら Enter を押す > ")

dump = subprocess.Popen(["candump", "-t", "a", "-l", "can0"], cwd=OUT_DIR)
time.sleep(1.0)

# candump -l は candump-<日時>.log という名前で書く。あとで rename する。
labels = open(label_path, "w", buffering=1)
labels.write("t,label\n")
labels.write("%.3f,記録開始\n" % time.time())

try:
    for i, (name, how) in enumerate(STEPS, 1):
        print("\n--- %d/%d  %s ---" % (i, len(STEPS), name))
        print("    %s" % how)
        input("    操作を始める直前に Enter > ")
        t = time.time()
        labels.write("%.3f,%s\n" % (t, name))
        print("    記録: %s" % time.strftime("%H:%M:%S", time.localtime(t)))
        time.sleep(0.5)
except KeyboardInterrupt:
    print("\n中断した。ここまでの記録は残る。")

labels.write("%.3f,記録終了\n" % time.time())
labels.close()
time.sleep(1.0)
dump.terminate()
dump.wait(timeout=5)

# candump が作ったファイルを、ラベルと対になる名前に付け替える
made = sorted(
    (f for f in os.listdir(OUT_DIR) if f.startswith("candump-")),
    key=lambda f: os.path.getmtime(os.path.join(OUT_DIR, f)),
)
if made:
    os.rename(os.path.join(OUT_DIR, made[-1]), dump_path)
    size = os.path.getsize(dump_path) / 1e6
    print("\n完了。%s (%.1f MB)" % (dump_path, size))
else:
    print("\ncandump の出力が見つからない。can0 が上がっているか確認すること。")
print("ラベル: %s" % label_path)
print("\nMac へ回収:")
print("  scp laurel@<pi>:%s* ." % os.path.join(OUT_DIR, "brake_%s" % stamp))
