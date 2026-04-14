# SDL 版復活手順

## 背景

SDL2 直描画版は `c72b3ea` (2026-04-13) で廃止されたが、起動時間 4-5s という魅力があるため
復活できるよう `feature/sdl-revival` ブランチに SDL コード一式を復元した。

復活判断は **起動時間が決定的な課題** になった場合のみ。WPE + fake bloom 構成で十分なら不要。

## 復元済みファイル (このブランチ)

- `internal/sdlui/` — canvas ベースレンダラー一式
- `cmd/canvas-proto/` — 開発用プロトタイプ
- `configs/pi-obd-meter-sdl.service` — SDL 版 systemd unit
- `scripts/setup-display.sh` — ディスプレイ設定スクリプト

## 未復元 (要手動作業)

- `cmd/pi-obd-meter/main.go` — SDL 初期化コードを入れる (`c72b3ea^` の該当ファイル参照)
- `go.mod` / `go.sum` — `github.com/veandco/go-sdl2` 等の依存追加
- `.github/workflows/ci.yml` — SDL2 dev libs (`libsdl2-dev libsdl2-ttf-dev libsdl2-image-dev`) + `CGO_ENABLED=1`
- `.github/workflows/deploy-dev.yml` / `release.yml` — 同上

## 前回 SDL 版の失敗要因 (要対処)

1. **グロー描画が重い** (CPU gaussian blur)
   - **対策**: WPE 版で確立した **fake bloom** (幅広・半透明の下敷きストローク) に置き換え
   - blur 計算不要、polygon 描画のみで済むため Pi 4 でも 60fps 維持可能
   - 参考: `web/static/js/gauge.js` の `createBloom()` 関数

2. **アーク polyline ステップ数**
   - 1.5°→0.5°/度 で 1/3 に削減済み (commit `8efb140`)
   - SDL 復活時も継続

3. **CGO クロスコンパイル**
   - Mac からのビルドが困難
   - **対策**: GitHub Actions ARM64 ネイティブランナー (`runs-on: ubuntu-22.04-arm`) 利用
   - 既に `deploy-dev.yml` が ARM ランナー使ってるのでビルド部分だけ CGO 対応

## 復活手順 (予想工数 1-2 日)

1. `main.go` に SDL 初期化と renderer 起動を戻す (c72b3ea^ から `git show` で参照)
2. `go.mod` に SDL 依存を戻す (`go get github.com/veandco/go-sdl2`)
3. `internal/sdlui/` 全体を再コンパイル検証
4. **グロー描画を fake bloom 方式に書き換え** (canvas_draw.go, canvas_elements.go)
5. CI/CD で SDL2 dev libs インストール + CGO_ENABLED=1
6. 実機で起動時間 / fps 測定
7. OK なら main ブランチへマージ

## 切り戻し

SDL 版で問題発生時は `git checkout develop` で WPE 版に戻せる。両者は排他的だが、
binary の service 定義を切り替えれば共存可能 (`pi-obd-meter.service` vs `pi-obd-meter-sdl.service`)。
