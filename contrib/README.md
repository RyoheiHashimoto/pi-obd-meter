# 上流への貢献

このプロジェクトの調査から出た、他所へ出すもの。

## 投稿済み

**RPi-Distro/firmware-nonfree#38 — `status_code=16` の正体**
[コメント](https://github.com/RPi-Distro/firmware-nonfree/issues/38#issuecomment-5584166077)（2026-09-08）

3年間・21コメント誰も答えていなかった「status 16 とは何か」に、
ドライバのソースと実機のトレースで回答した。本文は
[../docs/upstream-report-status16.md](../docs/upstream-report-status16.md)。

## 未送付

### `mazda_demio_dy.dbc` — opendbc へ

DY デミオ (2002-2007 JDM / Mazda2 DY、ZJ-VE + FN4A-EL) のブロードキャスト CAN 定義。
**[commaai/opendbc](https://github.com/commaai/opendbc) にこの世代のマツダは存在しない**
（最古は `mazda_rx8.dbc`）。ADAS 非対応の車でも受け入れられている前例がある。

収録した 6 メッセージ:

```
0x201 ENGINE        RPM / 車速 / エンジン負荷
0x230 AT_CTRL       ギア / 機械ギア比（8bit ラップの注意つき）
0x231 AT_STATUS     ギア / レンジ / HOLD / TCロックアップ / 変速中
0x420 COOLANT       水温 / 距離パルス
0x430 ELECTRIC      燃料残量 / 未同定B1 / オドメーター
0x4B0 WHEEL_SPEEDS  4輪速
```

**検証済み:**

- `cantools` でパースでき、実機のデコーダ (`internal/can/frame.go`) と数値が一致
- 実車ログの DBC 対象 19,636 フレームをデコードしてエラー 0 件
  (`scripts/ops/verify-dbc.py`)
- 1速の `GEAR_RATIO` が 0.26 になる（8bit ラップ）ことを実ログで再現

**2026-09-09 に DLC の誤りを修正した。** 全メッセージを 8 バイトと宣言していたが、
実フレームは `AT_STATUS` が 4、`COOLANT` と `ELECTRIC` が 7 バイトだった。
`cantools` の `decode_message()` は長さが厳密に一致しないと
`DecodeError: Wrong data size` で落ちるため、19,636 フレーム中 4,884 件が
デコードできない状態だった。信号のビット位置はいずれも収まっていたので、
宣言だけの誤り。

この欠陥は「フレーム数を数えるだけ」では出ず、実ログを1本ずつ
`decode_message()` に通して初めて出た。出す前に検証スクリプトを書くこと。

**未確定な点も正直にコメントへ入れてある:**
`ELECTRIC.UNKNOWN_B1` は未同定（電圧に連動するが電圧ではない。
`B0 + 2*B1 ≒ 418` の拘束がある）、`FUEL_LEVEL` はセンダーが両端でクリップし
非線形であること、`GEAR_RATIO` が滑りを含まない機械比であること。

**受け入れ先の前例は確認済み:** `mazda_rx8.dbc` が存在する一方 RX-8 は
`docs/CARS.md` に載っていない。opendbc は車種ポートを伴わない DBC 単体も
持っている。opendbc の貢献フローは openpilot の car port 向けに書かれているが、
DBC だけの追加に前例がないわけではない。

**走行ログでの検証も完了 (2026-09-09):**

```
raw_20260908_200350   623,404 フレーム  最高 71.8 km/h  エラー0件
raw_20260907_131204   522,709 フレーム  最高 67.2 km/h  エラー0件
                    合計 1,146,113 フレーム
```

独立デコードの相互検証も取れた。20 km/h 超の 58,375 点で、`SPEED` (0x201) と
4輪速 (0x4B0) の平均の差は中央値 +0.08 km/h・最大 0.97 km/h。別メッセージ・
別バイト位置から同じ速度が出ている。

`GEAR_RATIO` が機械比であること（1速で 8bit ラップして 0.26）も分布で確認。
1速 61,479 フレーム中 0.26 が 76.8%、0.24〜0.29 で 85%。観測値の集合だけ見ると
0.00〜2.55 に広がるが、それは変速中の過渡で、定常値は 0.26 に集中する。
**集合ではなく分布を見ること。** 集合だけ見て「機械比という記述は誤り」と
判断しかけた。

`VAL_` に無い生値も見つけたので頻度つきで注記した (`GEAR` の 0x00/0xF1、
`AT_RANGE` の 0)。いずれも 0.1% 未満の過渡で、名前を捏造せず「未同定」と
書いてある。

**残っているのは opendbc への出し方だけ。** ファイル名と配置は
`opendbc/dbc/mazda_demio_dy.dbc` で既存の命名に合う。


### `0001-brcmfmac-log-firmware-status-on-failed-connect.patch`

`brcmf_bss_connect_done()` が、ファームウェアから受け取った失敗理由
(`e->status`) を捨てて `WLAN_STATUS_AUTH_TIMEOUT`(16) 固定で報告している。
**情報は引数で渡ってきているのに使われていない。** 2行足して、失敗時に
ファームの status をログへ出す。

- **動作は変えない。** cfg80211 へ返す status はそのまま
- `brcmf_err()` は `net_ratelimit()` を通るのでログが溢れない
- Linux mainline (2026-09 時点) にクリーンに適用できることを確認済み

#### 検証済み (2026-09-09)

Docker の arm64 コンテナで mainline を引いて確かめた。

| 項目 | 結果 |
|---|---|
| 適用 | Linux **7.3.0-rc2** (`28924df2a`) に `git apply` がクリーンに通る |
| コンパイル | `ARCH=arm64 W=1` で `cfg80211.o` 生成成功、**警告0** |
| checkpatch | `--strict` で **0 errors, 0 warnings**（名前とメールを埋めた場合） |
| 書式指定子 | `event_code` / `status` / `reason` はいずれも `u32` (`fweh.h`) なので `%u` で正しい |

再現手順は `scripts/verify-brcmfmac-patch.sh`。

#### 送る前に残っていること

```
1. <YOUR NAME> <YOUR EMAIL> を2箇所（From: と Signed-off-by:）差し替える
   Signed-off-by は DCO への署名。本名とメールで書く決まりで、
   本人以外が代筆してはいけない
2. 実機で「失敗時に実際にログが出る」ことを確認する
   コンパイルは通ったが、実行時に brcmf_err() が期待どおり出るところは
   まだ見ていない
```

#### 送り先

```
git send-email --to=linux-wireless@vger.kernel.org \
  --cc=brcm80211@lists.linux.dev \
  --cc="Arend van Spriel <arend.vanspriel@broadcom.com>" \
  contrib/0001-brcmfmac-log-firmware-status-on-failed-connect.patch
```

正確な宛先は、その時点の `scripts/get_maintainer.pl` で確認すること。

**先に #38 のスレッドに貼って反応を見る手もある。** あそこには同じ症状の人が
複数いるので、実機で試してもらえる可能性がある。カーネルのMLに投げるより
敷居が低い。
