#!/usr/bin/env python3
# 長期の傾向変化から劣化の予兆を探す (#187)
#
#   sudo ./health-trend.py [走行ログのディレクトリ]
#
# 絶対値ではなく「走行ごとの代表値の推移」を出す。単発では正常でも、
# 数ヶ月の並びに劣化が現れることがある。
#
# 【列は時期によって増える】
# 2026-08-27 の記録は 14 列、2026-09-08 は 27 列。volt・atf・eco などは
# 途中から追加された。列番号で読むと古いログが全部落ちるので、必ず
# ヘッダ名で引き、無い列はその走行だけ欠測として扱う。
#
# 【判定はしない】
# 閾値を決めて「異常」と言うには、まだ基準になる期間が足りない。
# ここは推移を並べるところまで。傾きを読むのは人間の仕事。
import csv, glob, os, statistics, sys, datetime

DIR = sys.argv[1] if len(sys.argv) > 1 else "/data/log/drive-verify"
MIN_ROWS = 200          # これ未満の走行は代表値が安定しないので除く


def load(path):
    """ヘッダ名 -> 値のリスト。数値化できた行だけ。"""
    try:
        f = open(path, errors="replace")
    except OSError:
        return None
    # 電断で切れたファイルは末尾が NUL で埋まる。csv.reader は NUL を見ると
    # _csv.Error を投げ、下の「行の長さが合わなければ捨てる」分岐まで届かない。
    # 実測 (2026-09-09): 手元の走行ログ 45 本中 30 本、GPS 22 本中 17 本が該当。
    # 1本あたりの欠損は 252〜3,873 バイトで実害は無いが、1本でも混ざると
    # 解析全体が起動しない。ここで落としてから csv に渡す。
    r = csv.reader(line.replace("\0", "") for line in f)
    try:
        hdr = next(r)
    except StopIteration:
        return None
    if not hdr or hdr[0] != "t":
        return None
    cols = {n: [] for n in hdr}
    for row in r:
        if len(row) != len(hdr):
            continue        # 電断で切れた末尾の NUL 行など
        for n, v in zip(hdr, row):
            # bool 列は True/False の文字列で入る。float() が落ちて
            # 全欠測になるので先に拾う (locked, hold, brake, fan, ac, shifting)
            if v == "True":
                cols[n].append(1.0)
            elif v == "False":
                cols[n].append(0.0)
            else:
                try:
                    cols[n].append(float(v))
                except ValueError:
                    cols[n].append(None)
    return cols


def med(xs):
    xs = [x for x in xs if x is not None]
    return statistics.median(xs) if xs else None


def idle_voltage(c):
    """アイドル中の電圧。オルタネータ・充電制御の劣化が出る。"""
    if "volt" not in c:
        return None
    v = [c["volt"][i] for i in range(len(elapsed(c)))
         if c["speed"][i] == 0 and c["rpm"][i] and 550 < c["rpm"][i] < 950
         and c["volt"][i] and c["volt"][i] > 10]
    return med(v)


def elapsed(c):
    """経過時間の列を返す。単調時刻 tm があればそちらを使う。

    壁時計 t は RTC 無しの Pi で NTP が効いた瞬間に数時間跳ぶ。跳びは
    WiFi の association 完了と同時に起きるため走行の途中に来る (#184)。
    実測 (2026-09-09) で 45 走行中 3 件が 1070〜1374 分と出て、暖機の傾きが
    全滅していた。tm は 2026-09-09 に drive-verify.py へ追加した列なので、
    それ以前のログには無い。無ければ t で代用する (跳びは弾けない)。
    """
    return c["tm"] if c.get("tm") else c["t"]


def warmup_slope(c):
    """冷間始動からの水温の上がり方 (℃/分)。サーモスタット・冷却系。

    条件を厳しくしないと値が壊れる。最初の版では 0.04〜36 ℃/分 という
    ありえない幅が出た。原因は (a) 暖機済みの再始動を拾っていた、
    (b) 18時間アイドル放置した記録が混ざり分母が巨大になっていた。

    採用するのは「40℃未満から始まり、20分以内に80℃へ達した」記録だけ。

    さらに 0℃ は捨てる (2026-09-09 追加)。CAN の最初の 0x420 が届く前の
    未取得値が 0 で流れており、これを冷間始動と読むと「0℃から0.0分で80℃」
    という記録が量産される。実測で 17 件の「冷間」のうち 8 件がこれだった。
    ATF で HasATF を併走させたのと同じ問題で、水温側には印が無いので値で弾く。
    """
    t, w = elapsed(c), c["coolant"]
    if not t or w[0] is None or w[0] <= 0 or w[0] >= 40:
        return None
    for i in range(len(t)):
        if w[i] is not None and w[i] >= 80:
            dt = (t[i] - t[0]) / 60.0
            if not (1.0 <= dt <= 20.0):
                return None
            return (w[i] - w[0]) / dt
    return None


def atf_stats(c):
    if "atf" not in c:
        return None, None
    a = [x for x in c["atf"] if x and x > 20]
    if len(a) < 50:
        return None, None
    a.sort()
    return statistics.median(a), a[int(len(a) * 0.95)]


# 始動時間（セル回転〜アイドル安定）は、このログからは測れない。
#
# drive-verify が動き出す時点でエンジンは既に回っている。実際に測ると
# 129走行のほぼ全部で 0.2 秒（＝最初のサンプルで既にアイドル域）になり、
# 劣化を見る指標にならなかった。測るには起動直後の rpm を別経路で
# 拾う必要がある。指標から外す。


def slip_at_lock(c):
    """ロックアップ中の滑り。トルコンの劣化。"""
    if "locked" not in c:
        return None
    s = [c["slip"][i] for i in range(len(elapsed(c)))
         if c["locked"][i] and c["slip"][i] and 0 < c["slip"][i] < 2]
    return med(s) if len(s) > 50 else None


def main():
    rows = []
    for p in sorted(glob.glob(os.path.join(DIR, "*.csv"))):
        c = load(p)
        if not c or len(c.get("t", [])) < MIN_ROWS:
            continue
        atf_med, atf_p95 = atf_stats(c)
        dist = None
        if "trip_km" in c:
            tk = [x for x in c["trip_km"] if x is not None]
            if tk:
                dist = max(tk) - min(tk)
        rows.append({
            "file": os.path.basename(p),
            "date": datetime.datetime.fromtimestamp(c["t"][0]).strftime("%m/%d %H:%M"),
            "min": (elapsed(c)[-1] - elapsed(c)[0]) / 60.0,
            "km": dist,
            "volt": idle_voltage(c),
            "warm": warmup_slope(c),
            "atf": atf_med,
            "atf95": atf_p95,
            "slip": slip_at_lock(c),
        })

    if not rows:
        print("対象の走行が無い")
        return

    print(f"対象 {len(rows)} 走行  ({rows[0]['date']} 〜 {rows[-1]['date']})\n")
    hdr = f"{'日時':>12} {'分':>5} {'km':>6} {'アイドル電圧':>12} {'暖機℃/分':>9} {'ATF中央':>8} {'ATF95%':>7} {'滑り':>7}"
    print(hdr)
    print("-" * len(hdr))
    f2 = lambda v, w, d=2: (f"{v:>{w}.{d}f}" if v is not None else " " * (w - 1) + "-")
    for r in rows:
        print(f"{r['date']:>12} {f2(r['min'],5,1)} {f2(r['km'],6,1)} "
              f"{f2(r['volt'],12,3)} {f2(r['warm'],9,2)} {f2(r['atf'],8,1)} "
              f"{f2(r['atf95'],7,1)} {f2(r['slip'],9,4)}")

    print("\n--- 前半と後半の比較（傾向があれば差が出る） ---")
    half = len(rows) // 2
    for key, name, unit in [("volt", "アイドル電圧", "V"), ("warm", "暖機の傾き", "℃/分"),
                            ("atf", "ATF中央値", "℃"), ("slip", "ロック中の滑り", "")]:
        a = [r[key] for r in rows[:half] if r[key] is not None]
        b = [r[key] for r in rows[half:] if r[key] is not None]
        if len(a) < 3 or len(b) < 3:
            print(f"  {name:14} データ不足 (前半{len(a)}件 / 後半{len(b)}件)")
            continue
        ma, mb = statistics.median(a), statistics.median(b)
        d = mb - ma
        pct = (d / ma * 100) if ma else 0
        print(f"  {name:14} {ma:8.3f} → {mb:8.3f} {unit:4} ({d:+.3f}, {pct:+.1f}%)  n={len(a)}/{len(b)}")

    print("\n閾値判定はしない。基準になる期間が足りない。傾きを読むのは人間の仕事。")


if __name__ == "__main__":
    main()
