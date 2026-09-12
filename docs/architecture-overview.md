# このプロジェクトは技術的に何か

人に説明するとき・自分が見失ったときの参照用。
「何を使っているか」ではなく「**なぜその形なのか**」を残す。

## 1行で

Raspberry Pi に載せた車載メーター。車の CAN バスを直接読んで、リアルタイムで画面に出す。

## 30秒で

車の ECU が流している CAN フレームを Raspberry Pi で受け、速度・回転数・燃費を計算して
5インチ液晶に出す。Go の**単一バイナリ**で、中に HTTP サーバと WebSocket サーバが入っており、
同じ Pi 上のブラウザ (cog / WPE WebKit) がそれを見て描画する。
走行距離とメンテナンス記録は Google Apps Script 経由でスプレッドシートへ送る。

## 技術スタック

**フレームワークは使っていない。** 直接依存は4個だけ。

| 依存 | 用途 |
|---|---|
| `coder/websocket` | WebSocket 配信 |
| `go.bug.st/serial` | シリアル通信 (ELM327 フォールバック用) |
| `creativeprojects/go-selfupdate` | 起動時の自動更新 |
| `golang.org/x/sys` | Linux システムコール (SocketCAN) |

- HTTP サーバ: Go 標準ライブラリ `net/http`
- フロント: 素の JavaScript (`<script type="module">`)。React / Vue / jQuery / CDN なし
- 描画: SVG + CSS Custom Properties

これは引け目ではなく選択。2GB の Pi、オフラインで動く、バイナリ1個で完結 ——
フレームワークを入れる理由がない。

## アーキテクチャ: レイヤード

層に分け、**依存の向きを一方向に固定する**。上の層は下を知ってよい。下は上を知らない。

```
            ┌─────────────────────────────────────┐
 組み立て層  │ cmd/pi-obd-meter/                   │  main, app, api, ws_hub
            │  ここだけが全部を知っている            │
            └───┬──────────┬──────────┬────────────┘
                │          │          │
       入力     ▼   ドメイン ▼   出力   ▼
   ┌────────────────┐ ┌──────────────┐ ┌──────────────┐
   │ internal/can   │ │ internal/trip│ │internal/sender│
   │ internal/obd   │ │ internal/fuel│ │  (GAS へ POST)│
   │ (CAN / ELM327) │ │ internal/    │ └──────────────┘
   └────────────────┘ │  maintenance │
                      └──────┬───────┘
                             ▼
                   ┌───────────────────┐
        土台        │ internal/atomicfile│  tmp + rename + fsync
                   └───────────────────┘
```

### 実測した依存グラフ（2026-09時点）

```
cmd/* -> internal/{can,obd,trip,fuel,maintenance,sender,health}
internal/{trip,fuel,maintenance,health} -> internal/atomicfile
```

**それ以外の辺は存在しない。** 特に:

- ドメイン (`trip` / `fuel` / `maintenance`) は `net/http` も `serial` も `can` も import していない
- ドメイン同士も互いを知らない (`trip` は `fuel` を知らない)
- 組み立て (`cmd/`) だけが全員を知っていて、そこで配線する

### なぜこの形だと嬉しいか

1. **差し替えられる** — CAN 直結と ELM327 フォールバックを入れ替えても、上の計算は変わらない
2. **テストできる** — `internal/trip` のテスト584行は、車もネットも SD カードも無しで走る
3. **壊れる範囲が読める** — GAS を変えても `sender` から先には波及しない

### 崩れるとしたらどこか

**下の層が上を知り始めた瞬間**に崩れる。具体的には:

- `internal/trip` が `net/http` を import する
- `internal/fuel` が `internal/trip` を import する
- ドメインが `config.json` の構造体を直接受け取る

Go は**循環 import をコンパイルエラーにする**ので、一番ひどい崩れ方は言語が止めてくれる。
ただし「一方向だが層を飛び越える」依存は止めてくれない。そこは人間が見る。

## Go の要点（このリポジトリで実際に効いている7つ）

| | どこで |
|---|---|
| `goroutine` | CAN 読み取りループ、WebSocket の writePump |
| `channel` | `wsClient.send` (バッファ1) |
| `select` | `ws_hub.go` のノンブロッキング送信 |
| `mutex` | `WSHub.clients` マップの保護 |
| `defer` | ロック解放、ファイルクローズ |
| `interface` | `obd.Device` (CAN / ELM327 の差し替え点) |
| `error` | 戻り値としてのエラー。例外はない |

`cmd/pi-obd-meter/ws_hub.go` の `broadcast()` 15行に、このうち4つが同時に出てくる。

## 関連ドキュメント

- `docs/development.md` — 開発・CI/CD・リリースフロー
- `docs/calculation-logic.md` — 燃費などの算出ロジック
- `docs/configuration.md` — config.json 全パラメータ
