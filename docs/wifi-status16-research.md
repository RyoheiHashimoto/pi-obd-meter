# status_code=16 調査の現在地と次の手

2026-09-12 の調査記録。`docs/wifi-troubleshooting.md` が「何を測ったか」の記録なのに対し、
こちらは「**世の中に何が既にあり、自分の手に何が残っているか**」の記録。

## 1. 先行事例の棚卸し

`status_code=16` / `bssid=00:00:00:00:00:00` で検索したときの上位と、その中身。

| 場所 | 状態 | 結論 | 深さ |
|---|---|---|---|
| Raspberry Pi Forums **t=377009** | **Locked** / 2024-10 停止 / 6投稿3人 | `roamoff=1` で解決したという投稿で終了 | AP側の確認のみ |
| RPi-Distro/firmware-nonfree **#38** | **Open** / 2023-09〜 | **未解決**。報告者が「16 の意味を知りたい」と書いている | — |
| raspberrypi/firmware **#1829** | 未確認 | — | — |
| Stack Exchange **Q77144** | 生きている / 9k views / 4回答 / 16日前更新 | 回答4件（下記） | 1件だけ深い |
| fiquett.com (2026-04) | Pi 5 + eero | **原因不明。再起動で直った** | 802.11 交渉レベル |

### Q77144 の回答4件

1. `roamoff=1` (2021)
2. `roamoff=1 feature_disable=0x82000` (2023) — ビットマスクの解説あり
3. 電源/干渉（古いマウスを抜いたら直った） (2022)
4. **ファームウェアのブロブを Infineon の新しいものに差し替える** (2026)

**1 と 2 は当環境では効果なしを確認済み**（`docs/wifi-troubleshooting.md`）。

### 誰も書いていないこと

- `status_code=16` が**何を意味するか**。4回答とも対処法だけ
- ドライバのデバッグの**有効な出し方**。Q77144 の回答4は `debug=2` を試して
  「nondescript（要領を得ない）」と諦めている。当方は `brcmfmac.debug=0x8400`
  (CONN|EVENT) で `SET_SSID → status 1` / `LINK 無し` を読めている
- boot ごとの定量データ（31回全滅 vs 一発成功、-26dBm でも失敗）

### 誤情報が流通している

`status_code=16` は IEEE 802.11 では **AUTH_TIMEOUT**（次フレーム待ちのタイムアウト）。
「AP が追加の STA を処理できない」は **17**。fiquett の記事も Google の要約もこれを
取り違えている。fiquett は「eero は満杯ではなかった」と矛盾に気づきながら、解釈のほうを
疑えずに 3時間以上を消費している。

さらにその先がある。ドライバ (`brcmf_bss_connect_done()`) は成功なら 0、
**それ以外はすべて 16 を返す**ため、16 は「タイムアウトした」ですらなく「失敗した」の意味しかない。
→ **公開前に `ieee80211.h` と `cfg80211.c` で自分で裏を取ること。**

## 2. まだ試していない手（最優先）

**ベンダ提供の新しいファームウェアブロブへの差し替え。**

```
RPi OS 同梱 (43455)     Version 7.45.265
                        Pi 3B+ / 4 / 5 / 500 / CM5 で byte-for-byte 同一
                        (checksum 64410bcb — fiquett が実測)

Infineon/ifx-linux-firmware
                        最新タグ release-v6.1.145-2026_0108 (Longma)
                        cyfmac43455-sdio.bin を含む
ミラー                  murata-wireless/cyw-fmac-fw
```

`docs/wifi-troubleshooting.md` に「設定で直せるものではない」と書いたのは正しいが、
**ブロブの差し替えは「設定」ではない**。この経路は未検証。

### 手順

```bash
# 現行バージョン
strings /lib/firmware/cypress/cyfmac43455-sdio.bin | grep -iE "version|date"

# バックアップ（必須）
sudo cp -a /lib/firmware/cypress/cyfmac43455-sdio.bin ~/cyfmac43455-sdio.bin.bak

# Infineon の Longma を取得して同じコマンドで比較
# 新しければ置換 (644 root:root)
# brcmfmac.debug=0x8400 で boot ごとの SET_SSID / status 1 / LINK を数える
```

### 注意

- Wi-Fi が完全に停止する可能性。**車載前に、有線か SD リーダーで復旧できる状態で試す**
- `apt upgrade` が `firmware-brcm80211` を上書きする可能性。hold の要否を確認
- 過去に greetd で SSH ロックアウトした事故がある（`docs/boot-resilience.md`）。同じ轍を踏まない

**どちらに転んでも価値がある。** 直れば3年未解決の問題への答えになり、
直らなければ「最新ブロブでも駄目」という誰も書いていない否定結果になる。

## 3. 報告先（ファーム検証のあと）

| 優先 | 先 | 理由 |
|---|---|---|
| 1 | **firmware-nonfree #38** | open / 3年未解決 / 報告者が 16 の意味を尋ねている / 検索上位 |
| 2 | **Stack Exchange Q77144** | ロックされない / 9k views / 投票で残る / 既存4回答と被らない |
| 3 | raspberrypi/firmware #1829 | 未確認。状態を見てから |
| — | Forums t=377009 | **Locked。投稿不可** |
| — | fiquett.com | コメントに要ログイン。別ルートが要る |

### 投稿の構成（案）

解釈より測定値を先に置く。4 以外は議論の余地がないため、4 が外れても全体は残る。

1. boot ごとの測定データ（表）
2. 否定した仮説と、その根拠
3. 観測: `SET_SSID` に status 1、`LINK` が来ない
4. 解釈: 16 はドライバの固定値（ソースの位置を示す）
5. 再現方法: `brcmfmac.debug=0x8400`
6. （2 の検証結果があればここに）

**「解決した」とは書かない。** 解決していない。書くのは
「16 に診断情報は無い」と「AP が関与しているかの確かめ方」。

## 4. 補足

- Pi 4 は現役。生産は 2034年1月まで継続を明言、3GB 版が 2026年4月に新発売
- Wi-Fi チップとファームは Pi 3B+ 〜 Pi 5 で同一。**Pi 5 に替えてもこの問題は直らない**
- 日本語でこの症状を扱った記事は見当たらない（2026-09 時点）
