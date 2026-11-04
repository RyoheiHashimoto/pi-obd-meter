# CLAUDE.md — DYデミオ車載メーター

## プロジェクト概要

OBD-2対応車向けの車載メーター + 走行記録システム。
Raspberry Pi 4 + CAN HAT 直結で速度・RPM・スロットル・インマニ圧・瞬間燃費を5インチLCDにリアルタイム表示。
給油（自動検出）・累計距離・オイル交換の状態を Google Sheets に自動記録し、スマホから給油の較正・ODO補正・トリップ補正・オイル交換リセットを行う。
開発・動作確認はDYマツダデミオ（DBA-DY3W, ZJ-VE 1.3L）で行っている。

## 技術スタック

- **言語:** Go（車載バイナリ、`log/slog` 構造化ログ）+ HTML/CSS/JS（メーターUI）+ Google Apps Script（データ記録 + Webダッシュボード）
- **ターゲット:** Raspberry Pi 4 Model B 2GB (ARM64, Raspberry Pi OS Lite 64-bit)
- **ディスプレイ:** ELECROW 5インチ IPS HDMI タッチモニター (800×480) — Pi USBから給電可 (5V/1A)
- **OBD通信:** **CAN HAT (SocketCAN 直結) が主**、ELM327 Bluetooth (`/dev/rfcomm0`) はフォールバック
- **キオスク描画:** **WPE WebKit (cog 0.18.4)** on **labwc** Wayland コンポジタ (via **greetd** 自動ログイン)。Chromium / X11 / lightdm は使用しない
- **データ保存:** Google Sheets (GAS webhook)。ローカル状態ファイルはアトミック書き込み（tmp+rename+fsync）で電源断保護

## ブランチ戦略

- **develop**: 日常開発ブランチ。push → CI + dev ビルド自動デプロイ（CI成功時のみ）
- **main**: リリースブランチ。**ブランチ保護あり**（CI `test` ジョブ通過必須、直接push不可、admin bypass可）
- **GASデプロイ**: develop/main push時（`gas/` 変更時）に自動デプロイ
- **Pi 自動更新**: `auto-update.timer` (systemd) が**起動時に1回だけ** GitHub をポーリング、新ビルド検出時に自動更新

### ワークフロー
1. develop で開発・`make deploy`（即時）または push（CI成功後に dev-latest へ公開）
2. リリース時: `make release` → PR作成 (develop→main) → CI待機 → マージ → タグ push
3. GitHub Actions が ARM64 バイナリをビルド・リリース
4. Pi が**次回のエンジン始動時**に検出して自動更新（走行中には更新されない）

### コミットは必ずパスを明示する

**この作業ツリーは複数のセッションが同時に使う。** `git add -A` / `git add .` /
`git commit -a` は禁止。`git add <path>` で自分が触ったファイルだけを staging する。

2026-09-09、ops 修正のコミットが別セッションの作業中だった
`internal/trip/tracker.go` を巻き込んで push した（`3ac98f1`）。今回は完成品
だったので無害だったが、書きかけを拾えばそのまま CI を通り、dev-latest として
**走行中の車のメーターに OTA で配られる。**

コミット前に `git status` を見て、身に覚えのない差分があれば残す。
並行作業するなら `git worktree` で分けること。

### GitHub Actions ワークフロー

| ワークフロー | ファイル | トリガー |
|---|---|---|
| CI | `ci.yml` | push / PR (main, develop) |
| Deploy Dev | `deploy-dev.yml` | CI成功 (develop) — `workflow_run` トリガー |
| Release | `release.yml` | タグ `v*` push |
| Deploy GAS | `deploy-gas.yml` | push (main, develop) + `gas/` 変更 |

### Pi 自動更新の仕組み
- `scripts/auto-update.sh` が起動10秒後に1回だけ実行（`configs/auto-update.timer` の `OnBootSec=10sec`）
- 周期実行 (`OnUnitActiveSec=2min`) は 8281f78 で廃止。GitHub API のレート制限 (60回/時) を空回りで食い潰していたため。
  ACC ON のたびに1回チェックされるので OTA としては足りる。走行中にメーターが再起動しない利点もある
- Stable release → `dev-latest` pre-release の順にチェック
- 新ビルド検出時: ダウンロード → バイナリ + scripts/ + systemd ユニット差し替え → サービス再起動
- **UI (web/static) は配らない。** バイナリに go:embed で入っており、配信元は設定ファイルでは
  変えられない（`-web-dir` フラグ専用）。**`/opt` へ展開し直してはいけない**:
  `/` は overlayfs の上層が tmpfs なので再起動で消えるのに、バージョン記録は `/data` に残り
  「適用済み」と判定されて配り直されない。これで 2026-09-05 以降、UI の更新が一度も車に
  届いていなかった（2026-09-24 に実機で確認）
- バージョン管理: `/data/pi-obd-meter/app/{release-version,dev-version}`（バイナリと同じ永続層に置く）

## ビルド & デプロイ

```bash
# 開発（Mac上で実行、develop ブランチ）
make deploy          # ビルド + rsync転送 + サービス再起動
make logs            # リアルタイムログ表示
make ssh             # ラズパイにSSH接続
make status          # サービス状態確認
make restart         # サービス再起動（転送なし）

# リリース（PRベース: develop→main → タグ push → GitHub Actions → Pi 自動更新）
make release         # パッチ自動インクリメント (v0.3.1 → v0.3.2)
make release V=v1.0.0  # バージョン明示指定

# GAS デプロイ (clasp — develop/main push時にCI/CDで自動実行)
make deploy-gas      # gas/webhook.gs を GAS に push（ローカルから手動実行時）

# ビルド・テスト
make build           # ローカルビルド
make test            # テスト実行
make lint            # golangci-lint
make check           # lint + test

# 初回セットアップ（deploy.sh 直接）
./scripts/deploy.sh setup   # キオスク (greetd/labwc/cog) は入らない → docs/setup-guide.md「6. キオスクモード」
./scripts/deploy.sh kiosk-restart   # キオスク再起動 (= greetd 再起動)
```

ターゲット: `GOOS=linux GOARCH=arm64`
デプロイ先: `$PI_HOST:/opt/pi-obd-meter/`（デフォルト: `laurel@pi-obd-meter.local`、`scripts/deploy.sh` で設定）

## ディレクトリ構成

ファイル単位で全部は並べない（並べると増減に追いつかず古くなる。2026-11-04 に確かめたとき、
無いファイルが4つ載っていて、`internal/can`・`fuel`・`health` が丸ごと抜けていた）。
入口と、名前から役割が分からないものだけを書く。

```
cmd/
  pi-obd-meter/
    main.go                   エントリポイント + graceful shutdown + メインの処理ループ (obdProcessingLoop)
    app.go                    アプリケーション状態 + GAS とのやりとり (メンテ送信・起動時の復元)
    can_loop.go               CAN 直結の受信ループ (主)。フレームのデコード + OBD 要求の送信スロット
    obd_loop.go               ELM327 のポーリングループ (フォールバック)
    api.go / ws_hub.go        ローカルHTTP API / WebSocket 配信
    config.go                 設定読み込み + バリデーション。燃料補正係数の既定値と較正の経緯もここ
    fuel.go                   燃費計算 (MAF / MAP Speed-Density / 負荷×RPM)
    filter.go                 OBDスパイク除去フィルター
    refuel_ui.go              給油ダイアログの状態 (#120)
    dtc_state.go              始動時に読んだ故障コードの保持 (/api/dtc)
  pi-obd-scanner/main.go     対応PIDスキャナー（診断・初期確認用、DTC読取含む）

internal/
  can/          SocketCAN 受信とデコード、OBD over CAN、ISO-TP (DTC の複数フレーム)、
                距離パルス (pulse.go)、トルコンの滑り比 (slip.go)
  obd/          ELM327 シリアル通信、PID 定義、DTC 読取 (dtc.go = ELM327 / dtc_can.go = CAN 直結)
  fuel/         給油の自動検出 (refuel.go) + 航続距離用の燃料残量推定 (estimate.go)
  trip/         トリップ追跡。車速積分で距離・燃料を積算し、状態を永続化
  sender/       GAS Webhook送信。メモリ内リトライキュー（最大100件、指数バックオフ）
  maintenance/  オイル交換の残距離 (manager.go)。メンテ項目はオイル交換だけ
  health/       Pi 本体の健全性 (電圧降下・不正終了など) の記録
  atomicfile/   アトミックファイル書き込み（tmp + rename + fsync）

web/
  embed.go                  go:embed で static/ をバイナリに埋め込み
  static/
    meter.html / meter.css  メーター画面 (本番)
    meter-dev*.html         開発用プレビュー・試作 (車では使わない)
    js/
      main.js               エントリポイント + WebSocket/HTTPポーリング + オープニング + watchdog + 版の検知 + キオスク終了
      gauge.js              速度ゲージ(針+アーク) + RPMアーク + スロットルアーク + ギア/レンジ表示 + 60fps LERP補間
      indicators.js         右パネル バキューム計 + 4行インジケーター (ECO/RNG/TRIP/OIL)
      refuel.js             給油ダイアログ (自動検出の進み具合。停車中のみ表示、入力なし)
    fonts/                  Orbitron (速度・数値) / ShareTechMono (リードアウト)

gas/
  webhook.gs                Google Apps Script。doPost (maintenance / restore の2種) + doGet (スマホ用ダッシュボード)
  .clasp.json / appsscript.json   clasp 設定 / GAS マニフェスト

configs/
  config.json.example       設定テンプレート。複製して config.json を作る (config.json は git 管理外)
  config.mac.json.example   Mac ローカル demo 用 (can_interface=空。UI は -web-dir で repo を指す)
  pi-obd-meter.service      systemd メインサービス (run.sh を起動)
  run.sh                    SSD 側 (OTA) のバイナリが使えればそれを、無ければ SD 側を起動
  cog-kiosk.sh              cog (WPE WebKit) キオスク起動 — labwc autostart から実行
  kiosk.sh / kiosk.service  旧 cage + Chromium 版 (レガシー。どこからも入れない)
  auto-update.service/.timer  systemd 自動更新（起動時1回のみ）
  cold-candump.service      冷間始動時の CAN ログ取得 (arm ファイルがあるときだけ動く)

scripts/
  deploy.sh                 開発・デプロイ (make deploy / setup / kiosk-restart 等)
  release.sh                リリース (make release から呼ぶ)
  auto-update.sh            Pi 自動更新（GitHub Releases チェック）
  capture-screenshots.sh / drive-test.sh   スクリーンショット / 実走テスト
  ops/                      車載の運用スクリプトと systemd ユニット (persist-check, log-retention,
                            wifi-watchdog, 車両調査・計測)。scripts/ops/README.md

docs/                       主なものだけ。全体は ls docs/
  setup-guide.md            セットアップガイド
  development.md            開発・CI/CD・リリースフロー
  configuration.md          config.json 全パラメータ・車種チューニング
  calculation-logic.md      算出ロジック・閾値一覧
  meter-indicators.md       メーターの各表示の意味
  refuel-detection.md       給油の自動検出
  boot-resilience.md        不正電断・起動まわりの対策
  can-pid-map.md            DY デミオの CAN / PID マップ (分かっていることだけ)
  incident-*.md             障害記録
```

## アーキテクチャ上の重要な決定

### データフロー
```
ECU → CAN HAT (SocketCAN 直結) → pi-obd-meter (Go) → meter.html (cog/WPE WebKit on labwc)
                                                     → GAS Webhook → Google Sheets (給油記録 / 累計距離・オイル状態)
                                                                   ↕
                                              スマホブラウザ → GAS doGet (給油の較正/ODO・トリップ補正/オイル交換リセット)
```

### キオスク起動シーケンス
```
systemd → greetd (autologin laurel) → labwc (Wayland) → ~/.config/labwc/autostart
  ↓
swaybg (黒背景) + /opt/pi-obd-meter/configs/cog-kiosk.sh
  ↓
cog --platform=wl --bg-color=#000000FF --scale=1 http://localhost:9090/meter.html
```
- X11 / lightdm / Chromium 不使用 (Wayland 白フラッシュ回避、低メモリ)
- **systemd の `kiosk` ユニットは車に無い。** キオスクの再起動は `sudo systemctl restart greetd`
  (`deploy.sh kiosk-restart`)。auto-update.sh の `systemctl restart kiosk` はこの車では何もしない。
  OTA 後の画面の更新は、meter.html に埋めた版を main.js (`startVersionCheck`) が比べて reload する
- greetd / labwc の設定ファイルはリポジトリに入っていない (setup では入らない)
- `greetd` で systemd-user-sessions 依存、SSH ロックアウト注意 (過去事故 → バイナリ起動時に復旧コード実行)

### 給油記録（自動検出が本線 — #120）
- **Pi が燃料計 (CAN 0x430 B0) の跳躍から給油を検出する**（`internal/fuel/refuel.go`）。判定は起動直後の停車中に1回だけ
- 検出した瞬間にメンテナンス送信へ相乗りさせて GAS へ送り、GAS が「給油記録」シートに行を作る
- 同じ応答で `trip_correction_km=0` が返り、Pi がトリップをリセットする（`tracker.go` の `SetDistance`。0 は「新しいトリップを始めろ」の意味）
- 進み具合（検出 → 記録 → トリップリセット）はメーター画面のダイアログに出す（`web/static/js/refuel.js`、停車中のみ）
- **満タンのときは給油量を出さない。** センダーが上限でクリップするため、跳躍量から出した値に根拠が無い
- スマホの GAS フォームは残しているが、**レシートと突き合わせて検出量を較正するためのもの**で、毎回手入力する前提ではない

### GAS Webダッシュボード
- `doGet` で `HtmlService.createHtmlOutput()` によるモバイル対応HTMLを返す
- 表示内容: 給油履歴、走行統計（通算燃費など）、オイル交換の状態
- 操作: 給油記録（レシートによる較正用）、トリップ補正、ODO補正、オイル交換完了
- ダークテーマ、外部ライブラリなし、ホーム画面追加対応
- データはすべてGoogle Sheetsから直接取得

### Pi ↔ GAS 通信サイクル
- Pi はエンジン始動時 + 5分間隔でメンテナンス状態を GAS に送信
- GAS のレスポンスに `pending_resets`（オイル交換リセット）、`odometer_correction`（ODO補正）、`trip_correction_km`（トリップ補正。0 は給油によるリセット）が含まれる
- Pi はレスポンスを処理し、リセット適用後に即座に再送信してGAS側を最新に更新

### 瞬間燃費推定（3段階フォールバック）
- **MAFセンサー搭載車**: MAF (g/s) → 燃料消費量 (L/h) → 燃費 (km/L)（最も正確）
  - **DYデミオは MAF を持つ。** PID 0x10 が 1.52〜30.58 g/s の実値を返す (2026-08-31 の生ログで確認)。
    実機はこの経路を使っており、下2つのフォールバックには入らない
- **MAPセンサー搭載車**: Speed-Density法: MAP/大気圧 × RPM/2 × 排気量 → 吸入空気量 → 燃費
- **上記なし**: エンジン負荷 × RPM × 排気量から吸入空気量を推定
- 物理定数: 理論空燃比 14.7、ガソリン密度 750 g/L、空気密度 1.225 g/L
- エンブレ判定: MAP < 30 kPa（優先） or 負荷 < 5%（フォールバック）
- **減速時燃料カット中は燃料レートを 0 にする。** 1,300rpm 以上が条件（それ未満は ECU が燃料を復帰させる）。
  空気は流れ続けるため、MAF から求めたレートをそのまま計上すると燃やしていない燃料を数える
- 低速域 (<10 km/h) では燃費表示しない（クリープ等でのノイズ回避）

### SD書き込み低減
- swap無効化（`deploy.sh setup` で `dphys-swapfile` を停止・無効化）
- 状態ファイル（maintenance.json, trip_state.json）: アトミック書き込み（tmp+rename+fsync）で電源断保護
- トリップ状態: 0.1km 走行・燃料 0.02L・30秒 のいずれかで保存（距離だけだとアイドリング中の燃料が電源断で消える。2026-09-06）
- 送信失敗データ: メモリ内キュー（最大100件、指数バックオフ 5m→30m）
- ログ: journald（SSD `/data/log/journal` に永続化、上限 512M。2026-10-02 まで上限が 64M のままで、前回起動のシステム記録が毎回消えていた。docs/boot-resilience.md）。起動ごとに `scripts/ops/persist-check.sh` が恒久化を確かめ、外れは `/api/health` の `pi.persist` に出る
- 起動時にGASからODO復元（`type: "restore"`）。電源断でリセットされた場合のフォールバック

### CAN データの注意事項
- 0x230 B2（ギア比）: 1バイトオーバーフローあり。1速(2.816×100=282)とR(2.648×100=265)は256を超えるため折り返す。1速/R時は `(B2+256)/100` でデコード
- 0x210 B0-B1: DYデミオでは常に 0xFFFF（タービン回転数は CAN に出ていない）

### CAN 直結 (主)
- CAN HAT (MCP2515 SPI) 経由で `/dev/can0`、SocketCAN でパッシブ受信
- カーネルが CAN バスの全フレームを受け取り、pi-obd-meter が goroutine で読み取り
- `lastFrameTime` は IDEngine (0x201) フレームのみで更新 (staleTimeout 1s で disconnect)
- 無通信の間はポーリング周期ごとに Connected:false を送る (UI の ACC 移行を滑らかに)。WebSocket は内容が変わらなくても 1 秒ごとに送り直す (`ws_hub.go`)
- 0x201 は 100Hz で鳴っているので `poll_interval_ms: 50` で十分間に合う

### ELM327通信 (フォールバック)
- Bluetooth 2.0 Classic (SPP)。BLEはGATT複雑で不採用
- rfcomm bind → /dev/rfcomm0 でシリアルポートとして扱う
- マルチPIDバッチ: 1リクエストで複数PID取得 (6-7往復 → 2往復)
- ECUレスポンスが律速 (50-100ms/PID)。BT遅延 (20-50ms) は支配的でない
- `can_interface` が空文字列の時のみ起動する

### メーターUI
- 800×480 全画面、速度の270° SVGアークゲージ (r=230, cy=270)
- 内側にスロットルアーク (CAN LOAD 生値対応: idle=1, max=197, dimZone=5)
- 外側にRPMアーク (レッドゾーン背景付き)
- ゲージ左上にレンジ(P/R/N/D/S/L)、右上にギア番号、その下にHOLD/LOCKラベル
- 右パネル: バキューム計 + 4行インジケーター
  - バキューム計: MAP kPa → Bar 変換 (-1.0〜0 Bar、目盛りは線も数字もなし)。バキュームのアークは内側のリング。針の付け根の上に瞬間燃費（0.6 秒の合計窓、0.6 秒ごとに書き換え、停車中は L/h、エンブレ判定中も km/L で燃料カット中は 99.99）、外側のリングに「燃料の食い方」を 30 個の区切りの点灯数で出す（30 − km/L / L/h ÷ 0.05、最低 1 個、0.6 秒の合計、減るときは 1 秒に 15 個まで）。詳細は `docs/meter-indicators.md`
  - 4行インジケーター: ECO (平均燃費) / RNG (給油までの推定残距離) / TRIP / OIL (オイル交換までの残距離)。縦配置、bloom 付きアイコン + ガラスパネル枠
- **fake bloom** による軽量グロー (SVG `feGaussianBlur` 相当の重い処理なし)
  - 針: clone 方式 + `transform 0.6s cubic-bezier` で光の尾のように遅延追従
  - アーク: clone 方式 + main より太い stroke + 半透明で halo (CSS transition なし = 即時)
  - WebKit Bug 246106 (CSS filter が SVG 子要素に効かない) 回避のための実装
- **大数値オフセット影** (speed/RPM/MAP): 同じ `<text>` を dx/dy オフセット + 暗色で下敷きに複製 (CSS `text-shadow` は WebKit の SVG で未対応)
- **オープニングアニメ** 4 phase (sweep out 1.2s → sweep back 0.8s → text フェードイン 0.8s → 通常)
- **ACC 検出** (CAN 無通信 1秒) で `body.obd-offline` → `.acc-dim` + svg text が 800ms フェードアウト
- **フリーズ watchdog** (rAF 3s 停止で `location.reload()`) + POST /api/client-error でエラー通知
- CSS/JS分離済み (meter.html + meter.css + js/main.js + js/gauge.js + js/indicators.js)
- CSS Custom Properties で色・レイアウトを一元管理
- requestAnimationFrame で 60fps LERP 補間
- WebSocket 優先 (/ws/realtime) + HTTP フォールバック (ライブラリ: github.com/coder/websocket)
- 画面3秒長押しでキオスク終了
- 輝度の自動調整は無い（2026-06-19 に削除。動いていなかった）

### ディスプレイ設定 (config.txt)
ELECROW 5インチ IPS (800×480) 用。`/boot/firmware/config.txt` に追記:
```
hdmi_force_hotplug=1
max_usb_current=1
hdmi_drive=1
hdmi_group=2
hdmi_mode=87
hdmi_cvt 800 480 60 6 0 0 0
```

### データ送信の責任分離
- **GAS が受けるのは `maintenance` と `restore` の2種だけ。** トリップ単位の記録は 2026-03-07 に廃止した
- **メンテナンス状態**: Pi → GAS Webhook (type: "maintenance") → 設定シート（始動時 + 5分間隔）。オイル状態に加え、累計距離・トリップ距離・平均燃費・補正係数を載せる
- **状態復元**: Pi起動時 → GAS Webhook (type: "restore") → ODO/最終給油距離を取得
- **リアルタイム表示**: Pi → meter.html（車載LCD、WebSocket優先 / HTTP APIフォールバック）
- **給油記録**: Pi が自動検出 → GAS Webhook（メンテナンス送信に相乗り）→ Google Sheets。スマホからの入力はレシートによる較正用
- **ODO補正・トリップ補正・オイル交換リセット**: スマホ → GAS → Pi（次回メンテ送信レスポンスで反映）

### ローカルAPI
- `GET /api/config` — max_speed_kmh, ECO閾値, スロットル設定, version
- `GET /api/realtime` — 速度・RPM・負荷・スロットル・MAP・燃費・トリップ・接続状態・給油ダイアログの状態 (`refuel`)
- `WS /ws/realtime` — WebSocket リアルタイム配信 (HTTP フォールバック: GET /api/realtime)
- `GET /api/maintenance` — オイル交換の進捗
- `GET /api/dtc` — 始動時に1回読んだ故障コード（`read` が false なら未読。空でも「異常なし」とは限らない）
- `GET /api/health` — OBD/WiFi接続・キューサイズ・uptime・バージョン
- `POST /api/kiosk/stop` — キオスクモード終了
- `POST /api/client-error` — フロントからの JS エラー・watchdog 発動を journal に記録

### Graceful Shutdown
- SIGINT/SIGTERM受信時にトリップ状態とメンテナンス状態を保存してから終了
- バージョン: `-ldflags "-X main.version=vX.Y.Z"` でビルド時に埋め込み

### Web UI 埋め込み（go:embed）
- `web/embed.go` で `web/static/` をバイナリに埋め込み（`go:embed static`）
- 本番: 埋め込みファイルから配信（バイナリ1つで完結）
- 開発: `-web-dir <path>` を渡せばファイルシステムから配信（例: `-web-dir ./web/static`）

### 自動更新
- **起動時 (auto-update.sh)**: `scripts/auto-update.sh` (`configs/auto-update.service`) が stable/dev ビルドをチェックし、バイナリなどを差し替える。起動に失敗したら `.bak` に戻す
- Go 側の自己更新 (go-selfupdate、`update.go`) は 2026-10-21 に外した。auto-update.sh と同じ仕事を、安定版のバイナリだけ・ロールバック無しで二重にやっていたため
- リリースアセット: `pi-obd-meter_linux_arm64.tar.gz`（auto-update.sh がこの名前で取る。旧 go-selfupdate と同じ命名）

### GAS 自動デプロイ（clasp）
- `gas/` 配下の変更を main/develop に push → GitHub Actions が自動実行
- ESLint で構文チェック → `clasp push` (HEAD) → `clasp deploy` (本番)
- ESLint でエラー検出時はデプロイ中止
- `make deploy-gas` でローカルから手動 push も可能
- 初回のみ `clasp login` で Google OAuth 認証が必要（`~/.clasprc.json`）

## 車両固有の設定（config.json）

以下はすべてconfig.jsonで設定する。コード内にハードコードしない。
例外は `fuel_rate_correction` で、これは車両の設定ではなく燃料モデルの一部なのでバイナリが持つ（下記）。

テンプレートは `configs/config.json.example`。`config_example_test.go` が、テンプレートに
Config に無い項目（消した機能の設定）と `fuel_rate_correction` が入っていないことを検査する。
json.Unmarshal は知らない項目を黙って捨てるので、読み込み側では気づけないため。

- `can_interface`: CAN インタフェース名 (例: `can0`)。空文字列なら ELM327 フォールバック
- `serial_port`: ELM327 のシリアルポート (例: `/dev/rfcomm0`)。`can_interface` 空時のみ使用
- `webhook_url`: GAS WebアプリのURL
- `engine_displacement_l`: エンジン排気量 (例: ZJ-VE=1.3) — 燃費推定に使用
- `max_speed_kmh`: 速度メーター最大値 (例: 180)
- `initial_odometer_km`: 初期ODO値 (km)
- **Web UI の配信元は config.json では指定できない。** 常に埋め込み。開発時のみ `-web-dir` フラグ
  （設定ファイルは git 管理外で OTA からも更新できず、古い指定が残ると画面が更新されないため）
- `throttle_idle_pct`: スロットルアイドル開度 (例: 1) — CAN LOAD 生値のゼロ基準
- `throttle_max_pct`: スロットル最大開度 (例: 197) — CAN LOAD 生値の100%基準
- `fuel_tank_l`: 燃料タンク容量 (例: 46) — 航続距離 (RNG)・燃料残量推定、TRIP 警告閾値の導出に使用
- `trip_warn_km` / `trip_danger_km`: TRIP の警告距離。未指定ならタンク容量 × `eco_gradient_max_kmpl` から導出
- `eco_gradient_max_kmpl`: ECO 色のグラデーション上限 (km/L)。未指定なら排気量から導出
- `oil_change`: オイル交換 (`interval_km`, `warning_km`, `danger_km`)。メンテ項目はこれだけ
- `coolant_temp`: 水温の色分け閾値 (`cold_max`, `normal_max`, `warning_max`)
- `max_ps` / `max_ps_rpm` / `max_torque_kgfm` / `max_torque_rpm`: エンジン諸元 (`/api/config` で UI へ)
- `maintenance_path`: メンテ状態ファイルの場所。トリップ・給油検出などの状態ファイルも同じディレクトリに置く
- `obd_protocol`: OBDプロトコル (例: "6" = CAN 11bit 500k)
- `poll_interval_ms`: ポーリング間隔 (CAN 直結時は 50ms 推奨、ELM327 時は 200ms)
- `local_api_port`: ローカルAPIポート (デフォルト: 9090)
- `websocket`: WebSocket設定 (`enabled`, `broadcast_interval_ms`, `max_clients`)
- **`fuel_rate_correction` は書かない。** 既定値はバイナリの `defaultFuelRateCorrection` (config.go、現在 1.12)。
  燃料モデルを直すと係数も変わるので、config.json に書くとバイナリとずれる（2026-08-31 に Pi の config.json が
  古い値のまま優先されかけた）。較正はアプリの燃料積算とレシートを複数区間で突き合わせて行い、経緯は
  そのコメントに残す。車で実際に使われている値は GAS 設定シートの `pi_fuel_rate_correction` で見られる
- 輝度スケジュール (`brightness`) とメンテ項目の配列 (`maintenance_reminders`) はもう無い（機能ごと削除済み）

### 開発元の確認車両
- マツダ DYデミオ DBA-DY3W / ZJ-VE 1.3L 91PS / 1,090kg / 4AT / CAN 2.0B

## 注意事項

- `go.mod` のモジュール名は `github.com/hashimoto/pi-obd-meter`
- エラーメッセージ・ログは日本語
- config.json の webhook_url は実際の GAS WebアプリURL に差し替えが必要
- CAN HAT (MCP2515) は **クリスタル 12MHz** (実装差で 8MHz/16MHz もある。overlay の `oscillator=` を合わせる)
- 車両固有の定数をコード内にハードコードしないこと (config.json から読む)
- キオスク UI は WPE WebKit (cog) 前提。Chromium/Safari 互換とは限らない (CSS text-shadow, SVG `d` transition, feGaussianBlur 等に差異)
