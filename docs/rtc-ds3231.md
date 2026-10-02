# 外付け RTC (DS3231) の載せ方

issue #195。Pi 4 に RTC が無く、ログの時刻が信用できない問題を直す。

この文書の値は 2026-09-29 に実機で確認した。

## なぜ要るか

車内では NTP に繋がらないことがある。起動直後は前回保存した時刻から始まり、
途中で実時刻へ飛ぶ。飛びは drive と GPS の両方に同じ量だけ入る。

| 起動 | 壁時計での長さ | 実際の長さ | 飛び |
|---|---|---|---|
| 0918-1424 | 219.2 分 | 109.6 分 | **110 分** |
| 0918-2047 | 203.7 分 | 59.4 分 | **144 分** |
| 0919-0006 | 71.0 分 | 22.5 分 | **48 分** |

**車の動作には影響しない。** メーター表示・燃料積算・IMU はいずれも時刻に
依存しない。困るのは後からログを解析するときだけ。ただし実際にこれが原因で
「IMU が 52% しか記録していない」「駐車 135 分」といった誤った読み取りを
複数回している。2026-09-29 にも、別々の起動の journal を混ぜて誤った因果を
3 回組み立てた。

回避策（連続する 2 行の差のうち 5 秒以内だけを足す）は既にあるが、解析の
たびにこの但し書きが要る。

**RTC で直らないもの:** IMU ログの時刻が実時間より約 1.95% 速い件は
センサー側の問題なので、RTC とは無関係。`imu-log.py` で monotonic 時計から
付け直す対処が別に要る。

## 1. 買うもの

**配線タイプの DS3231 モジュール（ピンヘッダ 4 本を線でつなぐもの）**

**HAT 型・GPIO 直挿し型は買えない。** この Pi には CAN HAT (MCP2515, SPI) が
載っていて GPIO を占有している。6×2 ヘッダで刺さる形のものは物理的に場所が無い。

実際に調べて除外した例 (2026-10-02):

| 製品 | 不可の理由 |
|---|---|
| Seeed「High Accuracy Pi RTC (DS3231)」<br>スイッチサイエンス ¥2,244 | wiki に "the 6x2 header takes up the RX/TX pin of the Raspberry Pi" とあり GPIO 直挿し型。電池は CR1225 非充電式、I2C 0x68 |

買うのは、いわゆる **ZS-042 系**（青い基板、ピンが 4〜6 本出ている、Amazon で
数百円）のような**線でつなげるもの**。

**電池の型と充電回路**

市販品の多くは **LIR2032（充電式）前提で充電回路が付いている。** そこに
CR2032（非充電式）を入れると充電しようとして危険。次のどちらかにする。

- 「CR2032 対応」と明記された製品を選ぶ
- 充電回路の抵抗（基板上の R5、200Ω 程度）を外す

DS3231 は温度補償付きで精度 ±2ppm（月差 ±1 分程度）。安価な DS1307 は
温度補償が無く月差が数分出るので選ばない。

## 2. 現状（配線前）

```
$ sudo i2cdetect -y 1
60: -- -- -- -- -- -- -- -- UU -- -- -- -- -- -- --
                            ↑ 0x68 = MPU-6050 (ドライバ使用中)
$ cat /sys/bus/iio/devices/iio:device0/name
mpu6050
```

`/boot/firmware/config.txt` の現状:

```
dtparam=i2c_arm=on
dtoverlay=i2c-sensor,mpu6050
```

## 3. 設定（電源を切る前に済ませる）

配線してから設定すると、その間の起動で IMU が見えない状態になる。先に
config.txt を書き、電源を切り、配線してから起動する。再起動は 1 回で済む。

`/boot/firmware` は **読み取り専用でマウントされている**（`harden-boot` の
仕様）。編集には remount が要る。ここは overlayfs の下層ではなく別パーティ
ション（`/dev/mmcblk0p1`, vfat）なので、`overlayroot-chroot` は使わない。

**rw にしてからでないとバックアップも取れない。** 順序を守ること。

```bash
sudo mount -o remount,rw /boot/firmware

# 既に .bak があれば作り直さない。2 回目に実行すると変更後の内容で
# 上書きしてしまい、戻せなくなる
[ -e /boot/firmware/config.txt.bak ] \
  || sudo cp /boot/firmware/config.txt /boot/firmware/config.txt.bak

sudo sed -i 's/^dtoverlay=i2c-sensor,mpu6050$/dtoverlay=i2c-sensor,mpu6050,addr=0x69/' /boot/firmware/config.txt
echo 'dtoverlay=i2c-rtc,ds3231' | sudo tee -a /boot/firmware/config.txt
sudo sync
sudo mount -o remount,ro /boot/firmware
```

**目で確認してから次へ進む。**

```bash
grep -E 'i2c|rtc' /boot/firmware/config.txt
```

期待する結果:

```
dtparam=i2c_arm=on
dtoverlay=i2c-sensor,mpu6050,addr=0x69
dtoverlay=i2c-rtc,ds3231
```

## 4. 電源を切って配線する

**必ず電源を落としてから配線する。** 通電したまま GPIO を触ると、ショートで
Pi が焼ける。USB 電源も抜く。

```bash
sudo poweroff
```

LED が消えてから、電源ケーブルを抜く。

**DS3231 も MPU-6050 も I2C アドレスは 0x68 固定。** そのままでは競合するので、
MPU-6050 を 0x69 へ移す。

| DS3231 | Pi のピン |
|---|---|
| VCC | 3.3V（ピン 1 または 17） |
| GND | GND（ピン 6, 9, 14, 20, 25, 30, 34, 39 のどれか） |
| SDA | GPIO2 / ピン 3（**MPU-6050 と共有**） |
| SCL | GPIO3 / ピン 5（**MPU-6050 と共有**） |

**加えて MPU-6050 の AD0 を 3.3V へ繋ぐ。** 未接続だと 0x68、3.3V で 0x69。

配線は 5 本から 9 本になる。SDA/SCL は分岐して両方へ配る。

**5V を使わないこと。** DS3231 モジュールには 5V 対応品もあるが、Pi の
I2C は 3.3V なので、プルアップが 5V に繋がっていると GPIO を痛める。

### I2C のプルアップが過剰にならないか確かめる

**I2C に 2 つ目のデバイスを足すと、プルアップ抵抗が並列になって合成値が
下がる。** 下がりすぎるとバスが不安定になる（デバイスが見えたり消えたり、
通信エラー）。

| 場所 | 抵抗値 |
|---|---|
| Pi の GPIO2/3（基板に固定で載っている） | 1.8 kΩ |
| MPU-6050 モジュール（GY-521 等） | 通常 4.7 kΩ |
| DS3231 モジュール（ZS-042 等） | 通常 4.7 kΩ |

3 つ並列だと **約 0.93 kΩ**。3.3 V ÷ 0.93 kΩ = **3.5 mA** で、I2C 規格の
シンク電流上限 3 mA（NXP UM10204 の I_OL(max)）を超える。最小プルアップは
3.3 V ÷ 3 mA = **1.1 kΩ** なので、計算上はこれを下回る。

**どちらか一方のモジュールのプルアップを外せば収まる。**

```
Pi 1.8k + モジュール 4.7k の 2 つ並列 = 1.31 kΩ
3.3 V ÷ 1.31 kΩ = 2.5 mA   → 規格内
```

外すのは基板上の **R2 / R3**（SDA/SCL のプルアップ。ZS-042 も GY-521 も
この番号が多い）。どちらのモジュールから外しても結果は同じなので、
**半田付けしやすいほうを外す。**

**まず外さずに繋いで、動けばそのままでよい。** 計算上は規格を下回るが、
実際には動く例も多い。次の症状が出たら外す。

- `i2cdetect` で見えるアドレスが実行のたびに変わる
- `dmesg` に `i2c` のエラーが出る
- IMU のログが途切れる

**DS3231 に電池を入れる。** 入れ忘れると、電源を切った瞬間に時刻が消える。

配線が済んだら電源を入れる。

## 5. 確認

配線して電源を入れた後、次の 4 つをすべて確認する。

```bash
# (1) I2C に両方が見えるか。0x68=DS3231, 0x69=MPU-6050 の 2 つが UU になる
sudo i2cdetect -y 1

# (2) IMU が生きているか。mpu6050 が返らなければ addr の指定が効いていない
cat /sys/bus/iio/devices/iio:device0/name

# (3) RTC が認識されたか。n/a でなく実際の時刻が出る
timedatectl

# (4) RTC のデバイスが生えたか
ls -l /dev/rtc*
```

初回は RTC に時刻が入っていないので、NTP で合った状態で書き込む。

```bash
timedatectl   # System clock synchronized: yes を確認してから
sudo hwclock -w
sudo hwclock -r   # 書けたか読み返す
```

以後は起動時にカーネルが RTC から読む。`systemd-timesyncd` は NTP が
繋がったときだけ補正するので、そのままでよい。

## 6. 効いているかの判定

RTC を載せた後、**NTP に繋がらない状態で起動して**確認する。

```bash
# 起動直後（WiFi が繋がる前）に
date
timedatectl | grep -E 'synchronized|RTC time'
```

`System clock synchronized: no` なのに `date` が正しければ RTC が効いている。

ログ側では、飛びが消えたことを次で見る。連続する 2 行の差が 5 秒を超える
箇所が無くなる。

```bash
awk -F, 'NR>2{d=$1-p; if(d>5) print NR": "d" 秒の飛び"} {p=$1}' \
  /data/log/drive-verify/drive-XXXX-XXXX.csv
```

## 7. 戻し方

RTC を外す、または不調のとき。

設定を戻してから**電源を切り**、それから配線を戻す。順序が逆だと、OS は
0x68 を探すのに実機は 0x69 のままになり、IMU が認識されない。通電中に
配線を触るのも避ける。

`cp` ではなく `mv` で戻す。`.bak` を残すと、次に 3 章を実行したときに
「既にバックアップがある」と判定され、そのときの設定が保存されない。
古い `.bak` で上書きして設定を失うことになる。

```bash
sudo mount -o remount,rw /boot/firmware
if [ -e /boot/firmware/config.txt.bak ]; then
    sudo mv /boot/firmware/config.txt.bak /boot/firmware/config.txt
    sudo sync
else
    echo "バックアップが無い。config.txt を手で直すこと" >&2
fi
sudo mount -o remount,ro /boot/firmware
```

**戻ったことを目で確認してから電源を切る。** mv が失敗したまま配線だけ
戻すと、OS は 0x69 を探すのに実機は 0x68 になり、IMU が死ぬ。

```bash
grep -E 'i2c|rtc' /boot/firmware/config.txt
```

`addr=0x69` と `i2c-rtc` の 2 行が消えていること。確認できたら:

```bash
sudo poweroff
```

電源が落ちてから、MPU-6050 の AD0 を 3.3V から外す（0x68 に戻る）。
DS3231 も外す。それから電源を入れ直す。

## 8. 踏みやすい穴

- **`/boot/firmware` を rw のまま放置しない。** 車載機は毎回不正電断するので、
  FAT が rw のまま落ちると壊れる。編集したら必ず ro に戻す
- **`addr=0x69` を書いた状態で AD0 を繋がずに起動すると IMU が死ぬ。** 3〜4 章の
  順（設定 → 電源断 → 配線 → 起動）を守れば、その状態で起動することはない。
  設定と配線のどちらかだけを済ませて起動しないこと
- **`i2c-tools` は overlayfs で消える。** `sudo apt-get install i2c-tools` は
  再起動で失われる。恒久化するなら `sudo overlayroot-chroot apt-get install -y i2c-tools`
- **DS3231 の電池を入れ忘れると意味がない。** 電池なしでは電源を切った瞬間に
  時刻が消える
- **`fake-hwclock` は入れない。** 現在も未導入。RTC があれば不要で、両方
  あると起動時にどちらが勝つか分かりにくくなる
- `/var/lib/systemd/timesync` は SSD へ bind 済み（`/data/state/timesync`）。
  RTC を載せても外す必要はない。害はない

## 関連

- issue #195
- `docs/boot-resilience.md`（overlayfs と起動まわり）
- `project_pi_obd_imu_clock_drift`（IMU の時計ずれ。RTC では直らない）
