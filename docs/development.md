# 開発ガイド

## ブランチ戦略

| ブランチ | 役割 | CI | デプロイ |
|---|---|---|---|
| **develop** | 日常開発 | push / PR で自動実行 | CI成功時に `dev-latest` pre-release を自動作成 |
| **main** | リリース | push / PR で自動実行 | タグ push で GitHub Release を作成 |

- **main ブランチは保護されている**: CI (`test` ジョブ) の通過が必須。直接 push 不可（admin は緊急時のみ可）
- PR レビューは不要（一人開発）
- `gas/` 配下の変更は main / develop push 時に GAS に自動デプロイ

### 日常の開発フロー

```
1. develop で開発
2. make deploy（即時）または git push（2分以内に Pi 自動更新）
3. リリース時: make release (develop → main PR → CI待機 → マージ → タグ push)
4. GitHub Actions が ARM64 バイナリをビルド → Release 作成
5. Pi が2分以内に検出して自動更新
```

---

## ビルドとテスト

### Make ターゲット

```bash
# テスト
make test            # go test -v -race -count=1 ./...
make test-cover      # テスト + カバレッジレポート
make lint            # golangci-lint run ./...
make check           # lint + test（CI と同等）

# ビルド
make build           # ローカルビルド → bin/
make build-arm64     # ARM64 クロスコンパイル → bin/
make clean           # bin/ と coverage.out を削除
```

### クロスコンパイル

ターゲット: `GOOS=linux GOARCH=arm64` (Raspberry Pi 4)

```bash
GOOS=linux GOARCH=arm64 go build -o bin/pi-obd-meter-arm64 ./cmd/pi-obd-meter
```

バージョンはビルド時に `-ldflags` で埋め込む:

```bash
go build -ldflags "-s -w -X main.version=v0.4.0" -o pi-obd-meter ./cmd/pi-obd-meter
```

---

## デプロイ

### 開発デプロイ（rsync）

```bash
make deploy          # ビルド + rsync転送 + サービス再起動
```

`scripts/deploy.sh` が以下を実行:
1. ARM64 クロスコンパイル
2. rsync でバイナリ + configs + web/static + scripts を転送
3. `pi-obd-meter` + `kiosk` サービスを再起動

接続先は `PI_HOST` 環境変数で上書き可能（デフォルト: `laurel@pi-obd-meter.local`）。

### その他の操作

```bash
make logs            # リアルタイムログ表示 (journalctl -f)
make ssh             # ラズパイにSSH接続
make status          # サービス状態確認
make restart         # サービス再起動（ファイル転送なし）
```

`deploy.sh` を直接使う操作:

```bash
./scripts/deploy.sh setup            # 初回セットアップ（swap無効化含む）
./scripts/deploy.sh shutdown         # ラズパイをシャットダウン
./scripts/deploy.sh reboot           # ラズパイを再起動
./scripts/deploy.sh release-install  # GitHub Releasesから手動インストール（Pi上で実行）
```

### GAS デプロイ

```bash
make deploy-gas      # gas/webhook.gs を GAS に push（ローカルから手動実行）
```

通常は `gas/` 配下を main または develop に push すれば GitHub Actions が自動デプロイする。

---

## CI/CD パイプライン

![CI/CD パイプライン](cicd.svg)

### ワークフロー一覧

5つの GitHub Actions ワークフローが連携する:

#### 1. CI (`ci.yml`)

**トリガー**: push (main, develop) / PR (main, develop)

| ステップ | 内容 |
|---|---|
| go mod tidy | go.mod/go.sum の整合性チェック |
| Lint | golangci-lint (errcheck, govet, staticcheck, gofmt 等) |
| Test | `go test -v -race` + カバレッジ計測 |
| Build (host) | ホストアーキテクチャでビルド |
| Build (ARM64) | `GOOS=linux GOARCH=arm64` クロスコンパイル |

#### 2. Deploy Dev (`deploy-dev.yml`)

**トリガー**: CI ワークフロー完了 (develop, 成功時のみ)

CI が成功した develop コミットに対して:
1. ARM64 バイナリをビルド（`-ldflags` でコミットハッシュを埋め込み）
2. `pi-obd-meter` + `pi-obd-scanner` + `web/static/` を tar.gz にパッケージ
3. `dev-latest` タグを更新（既存削除→再作成）
4. pre-release として GitHub Release を作成

> `workflow_run` トリガーを使用し、CI 失敗時はデプロイしない。

#### 3. Release (`release.yml`)

**トリガー**: タグ `v*` push

1. semver タグ形式を検証 (`vX.Y.Z`)
2. テスト実行
3. ARM64 バイナリをビルド（バージョンを `-ldflags` で埋め込み）
4. スモークテスト（バージョン文字列 + go:embed ファイルの存在確認）
5. 2種類のアーカイブを作成:
   - `pi-obd-meter_linux_arm64.tar.gz` — 自動更新用（バイナリのみ）
   - `pi-obd-meter-vX.Y.Z-arm64.tar.gz` — 手動インストール用（バイナリ + configs）
6. SHA256 チェックサムを生成
7. GitHub Release を作成

#### 4. Deploy GAS (`deploy-gas.yml`)

**トリガー**: push (main, develop) + `gas/` 配下に変更あり

1. ESLint で GAS コードを構文チェック（エラーでデプロイ中止）
2. `clasp push` で HEAD デプロイメントに反映
3. `clasp deploy` で本番 Web アプリ URL に反映

初回のみ `clasp login` で Google OAuth 認証が必要（`~/.clasprc.json`）。
CI では `CLASP_REFRESH_TOKEN` シークレットを使用。

#### 5. Claude Code Review (`claude-review.yml`)

**トリガー**: PR open / sync / `@claude` メンション

AI によるコードレビュー:
- バグ・セキュリティ問題の検出と自動修正
- Go ベストプラクティス (gofmt, govet, errcheck) のチェック
- 軽微な問題は自動コミット、設計判断が必要な場合はレビューコメント

---

## リリース

### PR ベースリリースフロー

main ブランチが保護されているため、`make release` は PR 経由でマージする:

```bash
# develop ブランチで実行
make release         # パッチ自動インクリメント (v0.3.1 → v0.3.2)
make release V=v1.0.0  # バージョン明示指定
```

`make release` が実行する手順:

```
1. gh pr create (develop → main の PR 作成)
2. gh pr checks --watch (CI 完了を待機。失敗したら中断)
3. gh pr merge --merge (マージ)
4. git pull origin develop
5. git tag vX.Y.Z && git push origin vX.Y.Z (タグ push → Release ワークフロー発火)
```

### リリース後の自動配信

タグ push → Release ワークフロー → GitHub Release 作成 → Pi が2分以内に自動検出 → バイナリ差し替え + サービス再起動

Web UI はバイナリに埋め込み済み（`go:embed`）のため、バイナリ1つで完結する。

---

## Pi 自動更新

### 仕組み

`auto-update.timer` (systemd) が2分間隔で `scripts/auto-update.sh` を実行。

**チェック順序:**
1. **Stable release** (`latest`) — タグ名で差分を検出
2. **Dev build** (`dev-latest` pre-release) — `published_at` で差分を検出

**更新フロー:**
1. GitHub API でリリース情報を取得
2. 新バージョン検出時に tar.gz をダウンロード
3. `pi-obd-meter` サービスを停止
4. バイナリ + web/static を差し替え
5. サービス + キオスクを再起動

**安全装置:**
- flock によるロック（多重実行防止）
- ネットワーク未接続時はスキップ
- ダウンロード失敗時は更新しない

### バージョン管理

```
/var/lib/pi-obd-meter/
├── release-version   # 現在インストール済みの stable タグ名
└── dev-version       # 現在インストール済みの dev ビルドの published_at
```

### systemd 設定

```
configs/auto-update.service   # oneshot — auto-update.sh を実行
configs/auto-update.timer     # 2分間隔 (OnBootSec=2min, OnUnitActiveSec=2min)
```

---

## ローカル API

メーター UI (`meter.html`) とバックエンドの通信に使用。

| エンドポイント | メソッド | 内容 |
|---|---|---|
| `/api/config` | GET | max_speed_kmh, ECO閾値, スロットル設定 等 |
| `/api/realtime` | GET | 速度・RPM・負荷・スロットル・MAP・燃費・トリップ・接続状態 |
| `/api/maintenance` | GET | メンテナンス全項目の進捗 |
| `/api/health` | GET | OBD/WiFi接続・キューサイズ・uptime・バージョン |
| `/api/kiosk/stop` | POST | キオスクモード終了 |

ポート番号は `configs/config.json` の `local_api_port`（デフォルト: 9090）。
