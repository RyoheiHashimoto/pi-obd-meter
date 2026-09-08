# 上流への貢献

このプロジェクトの調査から出た、他所へ出すもの。

## 投稿済み

**RPi-Distro/firmware-nonfree#38 — `status_code=16` の正体**
[コメント](https://github.com/RPi-Distro/firmware-nonfree/issues/38#issuecomment-5584166077)（2026-09-08）

3年間・21コメント誰も答えていなかった「status 16 とは何か」に、
ドライバのソースと実機のトレースで回答した。本文は
[../docs/upstream-report-status16.md](../docs/upstream-report-status16.md)。

## 未送付

### `0001-brcmfmac-log-firmware-status-on-failed-connect.patch`

`brcmf_bss_connect_done()` が、ファームウェアから受け取った失敗理由
(`e->status`) を捨てて `WLAN_STATUS_AUTH_TIMEOUT`(16) 固定で報告している。
**情報は引数で渡ってきているのに使われていない。** 2行足して、失敗時に
ファームの status をログへ出す。

- **動作は変えない。** cfg80211 へ返す status はそのまま
- `brcmf_err()` は `net_ratelimit()` を通るのでログが溢れない
- Linux mainline (2026-09 時点) にクリーンに適用できることを確認済み

#### 送る前にやること

```
1. <YOUR NAME> <YOUR EMAIL> を2箇所（From: と Signed-off-by:）差し替える
   Signed-off-by は DCO への署名。本名とメールで書く決まり
2. 実機でビルドして、失敗時に実際にログが出ることを確認する
   （このパッチはまだ実機で動かしていない）
3. checkpatch を通す
   ./scripts/checkpatch.pl contrib/0001-*.patch
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
