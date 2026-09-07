# 上流への報告文（草案・未投稿）

投稿先候補: [RPi-Distro/firmware-nonfree #38](https://github.com/RPi-Distro/firmware-nonfree/issues/38)

**公開の場に出すものなので、投稿はユーザーの判断。** 以下は英文の本文案。

---

## What `status_code=16` actually is (driver-level trace)

The original question in this issue — *"We would like to know what this `status=16` means"* —
can be answered from the driver's own debug output. **It is not an AP response.** The firmware
rejects the join request locally and the driver synthesises the 802.11 status code.

### How to see it

```
# /etc/modprobe.d/brcmfmac-debug.conf
options brcmfmac debug=0x8400      # BRCMF_CONN_VAL | BRCMF_EVENT_VAL
```

### What the trace shows

Failing attempt:

```
brcmfmac: brcmf_fweh_event_worker event SET_SSID (0:0) ifidx 0 bsscfg 0
brcmfmac: brcmf_fweh_event_worker   version 2 flags 0 status 1 reason 0
wpa_supplicant: wlan0: CTRL-EVENT-ASSOC-REJECT bssid=00:00:00:00:00:00 status_code=16
```

Successful attempt, same machine, same AP, same config:

```
brcmfmac: brcmf_fweh_event_worker event SET_SSID (0:0) ifidx 0 bsscfg 0
brcmfmac: brcmf_fweh_event_worker event LINK (16:16) ... status 0
wpa_supplicant: wlan0: CTRL-EVENT-CONNECTED - Connection to ... completed
```

So: the firmware answers the `SET_SSID` (join) command with **status 1** and never emits
`LINK`. The all-zero BSSID in the wpa_supplicant message is the tell — no frame was ever
received from the AP. **No association request goes on air.** Each failure resolves in
**0.36–0.41 s**, far too fast for an over-the-air timeout, and it is remarkably constant.

### Measurements

Raspberry Pi 4 Model B Rev 1.5, `BCM4345/6`, firmware
`wl0: Aug 29 2023 01:47:08 version 7.45.265 (28bca26 CY) FWID 01-b677b91b`
(package `firmware-brcm80211 1:20260519-1~bpo13+1+rpt1`), kernel 6.18.39,
NetworkManager + wpa_supplicant, WPA2-PSK/CCMP, 2.4 GHz ch11, AP at **-26 dBm**.

| boot | `SET_SSID` | `status 1` | `LINK` | outcome |
|---|---|---|---|---|
| A | 1 | 0 | 1 | connected immediately |
| B | 31 | 31 | 0 | 7 minutes of failures, AP visible at -26 dBm throughout |

Time from boot to association, same hardware and AP, measured with
`journalctl -o short-monotonic`: **22 s / 30 s / 37 s / 68 s / 88 s / 326 s / 632 s**.

### Ruled out here (all measured, not assumed)

- **AP / router.** Failures carry an all-zero BSSID, i.e. no AP response exists.
  Other clients (macOS, phone, ESP32) associate with the same AP without trouble.
- **WPA3 / PMF / transition mode.** `brcmfmac.feature_disable=0x82000` is applied and
  *effective* — `wpa_cli get_capability key_mgmt` no longer lists `SAE`. Still fails.
- **`roamoff`.** Already `1` (Raspberry Pi OS default). Confirmed via
  `/sys/module/brcmfmac/parameters/roamoff`.
- **Signal strength.** -26 dBm during the failing streak.
- **Pinning BSSID and band.** Failures continued unchanged *after* the pin was removed.
- **Client power save**, **band restriction to `bg`**, **host CPU load**.
- **Firmware build.** Identical across boots that succeed and boots that fail. The
  board-specific file `brcmfmac43455-sdio.raspberrypi,4-model-b.bin` is byte-identical
  (md5) to `cypress/cyfmac43455-sdio-standard.bin`, so `update-alternatives` between
  `standard` and `minimal` has no effect on what is actually loaded.

### One more observation (offered as data, not as a claim)

While disconnected and retrying, the firmware emits `BRCMF_E_IF` (action ADD, ifidx 0)
at a very regular **10 Hz**:

```
brcmfmac: brcmf_fweh_event_worker event IF (54:54) ifidx 0 bsscfg 0 addr <mac>
brcmfmac: brcmf_fweh_handle_if_event action: 1 ifidx: 0 bsscfgidx: 0 flags: 0 role: 0
brcmfmac: brcmf_fweh_handle_if_event adding wl0 (<mac>)
```

Boot A: the stream ran 186–195 s and the join at 195.19 s succeeded as it stopped.
Boot B: the stream ran 1180–1614 s and all 31 joins inside it failed.
**This may simply be a companion of the disconnected state rather than a cause** —
with only one successful sample here it cannot be separated. Posting it in case it is
meaningful to someone with firmware visibility.

### Practical mitigation (does not fix the cause)

The failure itself costs ~0.4 s; the retry interval costs ~6.9 s, so >90 % of the time to
connect is spent waiting. `wpa_supplicant` also backs off on its own
(`SSID-TEMP-DISABLED duration=10 → 20 → 40`), so retrying naively makes it worse.
Clearing that with `wpa_cli enable_network <id>` before each `reassociate` keeps retries tight.

---

## 投稿する場合の注意（自分用メモ）

- 公開リポジトリなので、**SSID・BSSID・MAC・IPは伏せる**（上の本文は既に伏せてある）
- 「原因を特定した」とは書かない。**観測を提供する**という立場を崩さない
- IF イベントの件は「因果は不明」と明記済み。断定しないこと
