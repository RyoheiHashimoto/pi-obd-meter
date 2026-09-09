# 不正電断からPiを守る

## 何が起きたか (2026-08)

SDが読めなくなり、Piが起動しなくなった。SDを抜いてMacで調べたところ、
**ファイルシステムの破損自体は軽微**だった。致命的だったのは起動側の設定である。

```
fsck.repair=yes が終了コード 4 (未修復のエラーが残る) を返す
  → systemd が systemd-fsck-root.service を失敗扱いにする
  → emergency モードに落ちて起動が止まる
  → 画面もキーボードも無い車載機では誰も応答できない
  → SDを抜いてMacで直すしかない
```

つまり **壊れたことより、壊れたときに起動を諦める設定の方が問題だった。**

## なぜ壊れるのか

Piは車のアクセサリ電源で動いているため、**エンジンを切るたびに電源が落ちる。**
毎回が不正シャットダウンであり、書き込み中であればそこが壊れる。

根治は #60 (ACC検知 + タイマーリレーで正しくシャットダウン) だが、ハードが要る。
ソフトだけでできることを層にして積む。

## 第1層: 起動を止めない + 書き込みを減らす

`scripts/ops/harden-boot.sh` を Pi 上で実行する。

| 変更 | 狙い |
|---|---|
| `fsck.repair=preen` | 自動で直せるものだけ直して先へ進む。直しきれなくても止まらない |
| `systemd-fsck-root` の `SuccessExitStatus=0 1 2 4` | fsck が直しきれなくても起動を続ける |
| `emergency.service` に90秒後の自動再起動 | 落ちても誰も応答できないので、待つより再起動する |
| journald を `Storage=persistent` (SSD) | 2026-09-09 に volatile から変更。理由は下記 |
| swap 無効化 | SD書き込みの最大要因 |
| root に `noatime` | 読み込みのたびに発生する書き込みを止める |

すべてべき等。`--rollback` で元に戻せる。

### 効果の確認 — SDを壊さずに障害を再現する

設定を入れただけでは効果を確かめられない。かといって実際に電源を引き抜く
試験は、それ自体が SD を壊しに行く行為であり、失敗すれば「SDを抜いて Mac で
直す」に逆戻りする。避けるための作業でそれを起こすのは筋が悪い。

8月に起動を止めた直接の原因は「fsck が終了コード4を返したこと」だった。
破損そのものではなく終了コードへの反応が問題だったので、**fsck を偽物に
差し替えて4を返させれば、ファイルシステムを一切壊さずに同じ状況を作れる。**

```
./scripts/ops/verify-boot-resilience.sh <PiのIP>
```

| 段階 | 内容 | 破損リスク |
|---|---|---|
| 1 | fsck を偽装して終了コード4 を返させる | **ゼロ** |
| 2 | fsck を強制した上で正常に再起動 | **ゼロ** |
| 3 | 同期せずに即再起動 (電源断と同じ) | エンジン停止1回分 |

段階3は既定では実行しない。`--with-power-cut` を明示したときだけ1回行う。
段階1-2 が通ってからでなければ意味がないので、順番も固定してある。

#### 安全網

`cmdline.txt` は FAT の `/boot` にあるため、万一起動しなくなっても
**Mac に挿してテキストを1行戻すだけで復旧できる。** 8月の ext4 修復とは
難易度が違う。スクリプトは適用前の `cmdline.txt` を Pi 上と Mac 上の
両方に残す。

## 第2層: root を読み取り専用にする (2026-09-05 実施)

第1層は「壊れても起動する」だが、第2層は「そもそも壊さない」。
overlayfs で root を読み取り専用にし、書き込みをRAMへ逃がす。

書き込み可能な置き場は外付けSSDを `/data` にすることで解決した。
ログと状態ファイルは `/data` 配下へシンボリックリンクしてある。

| リンク元 | リンク先 |
|---|---|
| `/var/log/gps` | `/data/log/gps` |
| `/var/log/drive-verify` | `/data/log/drive-verify` |
| `/var/log/can-verify` | `/data/log/can-verify` |
| `/var/log/journal` | `/data/log/journal` |
| `/var/lib/pi-obd-meter` | `/data/pi-obd-meter` |

SDへの書き込みは実測で **60秒あたり 0 セクタ**。摩耗は止まった。

### journald を volatile から persistent に戻した (2026-09-09)

第1層で `Storage=volatile` にしたのは SD の摩耗と電断破損を避けるためで、
当時は正しかった。第2層で `/data` に SSD を足し `/var/log/journal` を
そこへリンクした時点で、その理由は消えている。

volatile のままだと **再起動をまたぐログが一切残らない**。この機体で
追っている内蔵WiFi の association 失敗 (#184) は起動時の事象なので、
記録が起動の境界で毎回消えるのは致命的だった。`/data` に直接書く
ロガー4本だけが例外的に残っていた。

`harden-boot.sh` を `Storage=persistent` + `SystemMaxUse=512M` に変更した。
同時に、同スクリプトにあった `rm -rf /var/log/journal` を撤去した。
リンクになった後のこの機体で再実行すると、**リンクごと消してしまう**。

`wifi-watchdog.sh` も `/var/log/wifi-watchdog.log` (tmpfs) への書き込みを
やめ、journal へ出すようにした。自前の日時も外した。RTC が無く、WiFi が
繋がって NTP が効くまで壁時計が当てにならないため、
`journalctl -u wifi-watchdog -o short-monotonic` で読む。

設定は `/etc/overlayroot.conf`:

```
overlayroot_cfgdisk="disabled"
overlayroot="tmpfs:recurse=0"
```

**`recurse=0` は必須。** 既定の `recurse=1` は `/` 以外のマウントも
まとめて読み取り専用にするため、`/data` までRAMのオーバーレイに載る。
つまりログも状態ファイルも再起動で消える。有効化直後に一度これを踏み、
2分ほどで気づいて戻した。

### 恒久的な変更は overlayroot-chroot から

`/` への書き込みは再起動で消える。`apt` も設定ファイルの編集も同じ。
下層の本物のルートに入るには:

```
sudo overlayroot-chroot                 # シェルに入る
sudo overlayroot-chroot apt update      # コマンドを直接実行
```

`/media/root-ro` を手で `mount -o remount,rw` してもよいが、
**稼働中に `ro` へ戻すことはできない**。overlayfs が下層を掴んでいるため
`mount -o remount,ro` は EBUSY で失敗する (サブマウントも書き込み中ファイルも
無くても失敗する)。戻すには再起動が要る。overlayroot-chroot は終了時に
自動で `ro` に戻すので、そちらを使うこと。

### deploy が下層を rw のまま残すことがある (2026-09-08 実際に発生)

`deploy.sh` の永続化は `mount -o remount,rw /media/root-ro` → rsync →
`overlayroot-chroot true`（終了処理を借りて ro へ戻す）という流れ。
**最後の `overlayroot-chroot` が失敗すると、下層が rw のまま残る。**

```
  ★ /media/root-ro が rw のままです。再起動して戻してください
ERROR: Note that [/media/root-ro] is still mounted read/write
mount: /media/root-ro: mount point is busy.
```

`fuser -vm` で見ると掴んでいるのは `kernel mount` ＝ overlayfs 本体
（`lowerdir=/media/root-ro`）。これは常にそうなので、これ自体が原因ではない。
**`sync` して間を置いて数回試しても戻らなかった。**

**復旧は再起動のみ。** エンジンを切って入れ直せば ro に戻る。

その間のリスク評価（rw のまま電断した場合）:

```
永続化は完了している    上層と下層のバイナリが一致することを cmp で確認
書き込みは走っていない  sync 済み
fsck.mode=auto          不正電断時は起動時に自動検査される
```

**壊れる確率は低いが、ゼロではない。** overlayfs を入れた目的そのものが
「下層に書かない」ことなので、気づいたら早めに再起動する。

デプロイ後は毎回 `mount | grep root-ro` で `(ro` を確認すること。

### デプロイも下層へ複製しないと消える

`/opt/pi-obd-meter` は `/` の上にあるため、`rsync` でバイナリを置いても
**再起動で元に戻る**。overlayfs を有効にした 2026-09-05 以降、
「デプロイしたのにエンジンを切ったら元のバージョンだった」が起きうる状態
だった (実際に踏む前に気づいた)。

`scripts/deploy.sh` の `deploy` は転送・再起動のあとに `persist` を呼び、
下層へ複製するようにした。単体でも呼べる。

```
./scripts/deploy.sh persist
```

中身は下層を rw にして rsync し、`overlayroot-chroot true` で ro に戻すだけ。
**`mount -o remount,ro` を自分で叩いてはいけない。** 稼働中は overlayfs が
下層を掴んでいるため EBUSY で失敗し、SDのルートが rw のまま残る。
overlayroot-chroot の終了処理なら確実に戻せるので、それを借りている。

### 副作用: 時計が毎回巻き戻る (2026-09-06 に発覚・対処済み)

**Pi 4 に RTC は無い** (`timedatectl` の `RTC time: n/a`)。
時刻は systemd-timesyncd が `/var/lib/systemd/timesync/clock` の mtime に
保存し、次回起動時にそこまで進めることで引き継いでいる。fake-hwclock と
同じ仕組みが systemd に内蔵されている。

このファイルは `/` 上にあるため、overlayfs を有効にした瞬間から
**再起動のたびに捨てられる**ようになった。結果、毎回まったく同じ時刻
(2026-09-06 03:22:48) から起動するようになり、次の実害が出た。

- ログのファイル名が毎回衝突する。`drive-verify.py` は `open(path, "w")` で
  開いていたため、**前回のブートの走行記録を丸ごと消していた**。2.8MB を実際に消失した
- `can-verify.sh` は `>>` で追記するため、**複数ブートのストリームが1本のファイルに
  混ざった**。そのまま距離パルスを積算すると 45km の走行が 139km になる
- 車内ではNTPに繋がらないことがあり、その間ログの時刻が全て嘘になる

対処は保存先を `/data` へ逃がすこと。`/etc/fstab` に追加する:

```
/data/state/timesync /var/lib/systemd/timesync none bind,nofail,x-systemd.requires-mounts-for=/data 0 0
```

timesyncd は `Before=time-set.target sysinit.target` で非常に早く動くため、
マウント待ちを明示する drop-in も要る
(`scripts/ops/systemd/systemd-timesyncd.service.d/data-state.conf`)。
`Requires` ではなく **`After` のみ**にしてある。`/data` が無くてもNTPは動かしたい。

fake-hwclock を `apt install` する案は採らなかった。同じ機構が二重になるうえ、
その保存先 `/etc/fake-hwclock.data` も `/` 上なので**同じ理由で消える**。
壊れているのは機構ではなく置き場所だけだった。

あわせて、ロガー側も**同名ファイルを絶対に開かない**よう連番を振るようにした。
時計が直っても車内でNTPに繋がらなければ時刻は巻き戻りうるため、二重に防ぐ。

### fstab を触るときの注意

overlayroot は起動時に `/etc/fstab` を書き換えて `/` を overlay に差し替える。
本物は `/media/root-ro/etc/fstab` にある。`recurse=0` の場合、
**`/` 以外の行はコメントごと原文のまま通る**
(初期化スクリプトの `overlayrootify_fstab()` を読んで確認した)。

編集したら再起動する前に必ず検証すること。起動しなくなると車内では復旧できない。

```
sudo findmnt --verify --tab-file /media/root-ro/etc/fstab
```

## USB とストレージ — 挿す場所を間違えると全部が落ちる

### SSD は USB2.0 側に挿す

`ELECOM ESD-EXS 250GB` (`056e:6a20`)。**青い USB3.0 ポートに挿すと 1GB の連続書き込みで
`xhci_hcd: Host System Error` → `HC died` となり、全バスの USB 機器が同時に消える。**
電源ではない（`throttled=0x0`、ディスプレイを外部電源にしても不変）。
黒い USB2.0 側（VIA ハブ `2109:3431` 経由）では完走する。2026-09-05 実証。

### AIC8800 ドングルは同じハブ上の SSD を殺す

起動時に3回再列挙し（`a69c:5723` → `a69c:8d80` → `368b:8d83`）、その 0.5 秒後に
同じハブの SSD が `-71` で落ちる。`/data` ごと消えるため、TRIP・燃料積算・時刻の永続化・
journal の永続化が同時に失われる。

**対処済み:** `/etc/modprobe.d/blacklist-aic8800.conf` でドライバを無効化（下層へ永続化）+
**物理撤去**。以後の起動で `-71` は 0 回。詳細は #183。

```
blacklist aic8800_fdrv
blacklist aic8800_bsp
blacklist aic_load_fw
blacklist aic8800
```

### /data の電断耐性

```
/dev/sda1 → /data   ext4, noatime, commit=5, nofail, x-systemd.device-timeout=10
```

`commit=5` で 5 秒ごとに確定し、状態ファイルは一時ファイル→rename で書く。
**エンジン停止で失われるのは各ログの末尾数秒だけ**（実測: IMU 0.4 秒、drive-verify 2.6 秒。
NUL バイトの塊として現れる）。TRIP・燃料積算・メンテ状態は rename 方式なので壊れない。

### 画面キャプチャは grim

この機械は Wayland (labwc + cog)。**`scrot` は X11 専用なので `DISPLAY=:0` を渡しても
真っ黒な画像しか出ない** (#89)。`grim` を使い、`WAYLAND_DISPLAY` と `XDG_RUNTIME_DIR` は
`/run/user/$(id -u)/wayland-*` から取る。
