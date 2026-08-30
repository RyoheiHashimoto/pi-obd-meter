# 走行データの記録

解析用のロガー2本。**どちらも systemd で常時起動**しており、エンジンをかければ
自動で記録が始まる。エンジン停止で電源が落ちても、次の起動で再開する。

## poll22 — Mode 22 の連続ポーリング

```
実体   /usr/local/bin/poll22-lean.py   (scripts/ops/poll22-lean.py)
出力   /var/log/can-verify/poll22_YYYYmmdd_HHMMSS.csv
形式   t,pid,b1,b2,coolant,speed
周期   87個のPIDを約2.6秒で1周
書込   約3 MB/時
```

有効 PID の一覧は `/var/log/can-verify/scan22_full_*.log` の最新から読む。
標準 PID (05/0F/10/0B/06/07/0E/42) も叩き、`01xx` として同じ CSV に記録する。

**生の candump を保存してはいけない。** 全フレームを保存すると 115 MB/時に
なり、その大半は解析に使わないブロードキャストだった。車載機はエンジン停止で
毎回不正電断するため、**書き込み量がそのまま破損リスクになる。**

同定が済んだら `systemctl disable poll22` で止めてよい。

## drive-verify — 走行状態の記録

```
実体   /usr/local/bin/drive-verify.py   (scripts/ops/drive-verify.py)
出力   /var/log/drive-verify/drive-MMDD-HHMM.csv
周期   0.2秒 (5行/秒)
書込   約1.5 MB/時
```

記録項目:

```
t, speed, rpm, gear, ratio, mech, slip, tcc, locked,
hold, range, shifting, atf, volt, odo, trip_km, fuel_pt,
rate_lh, eco, coolant, map, load
```

**hold と range は必ず記録すること。** ギア段だけでは、AT の自動変速なのか
運転者の手動操作なのか区別できない。DY デミオは HOLD スイッチとレンジ操作で
任意にギアを固定できる。実際「負荷が上がると AT が3速へ落とす」と解釈した
観測が、**すべて運転者の手動操作だった**ことがある。

`rate_lh` (燃料消費レート) は油温の解析に必須。`load` は 0x201 B6 で
**200 が全開**のスケール (パーセントではない)。

## 全数スキャン

```
tools/scan-mode22.sh <開始高位バイト> <終了高位バイト>
例: sudo bash scan-mode22.sh 00 FF   # 全 65,536 通り、約33分
```

エンジン稼働中・停車で実行する。走行は不要。**poll22 とバスで競合するので
止めてから実行し、終わったら戻すこと。**

中断した場合は最後に到達した高位バイトから再開できる。結果は
`scan22_full_*.log` という名前にしておくこと (poll22 がこの名前で探す)。

## 解析時の注意

**ログ全体の統計で分類してはいけない。** サンプルごとの瞬間値で分けること。
「最高速101km/h のログ」の中の 87℃ のサンプルが、走行前のアイドリング中
だったなら、条件を変えた比較になっていない。一度これで誤った結論を出した。

**日をまたいだ複数走行で突き合わせること。** 単一の走行では、たまたま相関して
いるだけの量を拾う。条件の違う3走行で同じ関係が成り立つかを見るのが確実。
