# pi-obd-meter Makefile

.PHONY: test test-cover lint fmt vet build build-arm64 clean check deploy deploy-gas logs ssh status restart release

# --- 開発 ---

test:
	go test -v -race -count=1 ./...

test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	@echo "---"
	@echo "HTML report: go tool cover -html=coverage.out"

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

check: lint vet test

# --- ビルド ---

build:
	@mkdir -p bin
	go build -o bin/pi-obd-meter ./cmd/pi-obd-meter
	go build -o bin/pi-obd-scanner ./cmd/pi-obd-scanner

build-arm64:
	@mkdir -p bin
	GOOS=linux GOARCH=arm64 go build -o bin/pi-obd-meter-arm64 ./cmd/pi-obd-meter
	GOOS=linux GOARCH=arm64 go build -o bin/pi-obd-scanner-arm64 ./cmd/pi-obd-scanner

clean:
	rm -rf bin/ coverage.out

# --- デプロイ (deploy.sh に委譲) ---

deploy:
	./scripts/deploy.sh deploy

deploy-gas:
	cd gas && clasp push
	@echo "✓ GAS コード更新完了（HEADデプロイメントに反映）"

logs:
	./scripts/deploy.sh logs

ssh:
	./scripts/deploy.sh ssh

status:
	./scripts/deploy.sh status

restart:
	./scripts/deploy.sh restart

# --- リリース (release.sh に委譲) ---
# make release        → パッチ番号を自動インクリメント (v0.3.0 → v0.3.1)
# make release V=v1.0.0 → バージョンを明示指定
# フロー: PR作成(変更ログ付き) → CI待機 → マージ → mainにタグ → GitHub Actionsがリリース
# 冪等: 途中失敗しても再実行可能（既存PR検出、マージ済みスキップ、タグ存在チェック）

release:
	./scripts/release.sh $(V)
