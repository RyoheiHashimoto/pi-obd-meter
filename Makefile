# pi-obd-meter Makefile

.PHONY: test test-cover lint build build-arm64 clean check deploy deploy-gas logs ssh status restart release

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

check: lint test

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

# --- リリース ---
# make release        → パッチ番号を自動インクリメント (v0.3.0 → v0.3.1)
# make release V=v1.0.0 → バージョンを明示指定

V ?= $(shell git describe --tags --abbrev=0 2>/dev/null | awk -F. '{print $$1"."$$2"."$$3+1}')

release:
	@if [ -z "$(V)" ]; then echo "Error: タグが見つかりません。V=v0.1.0 で指定してください"; exit 1; fi
	@echo "Releasing $(V)..."
	@CURRENT=$$(git branch --show-current); \
	if [ "$$CURRENT" = "develop" ]; then \
		echo "develop → main にマージ中..."; \
		git checkout main && git pull origin main && git merge develop --no-edit && git push origin main; \
	elif [ "$$CURRENT" != "main" ]; then \
		echo "Error: release は main または develop ブランチで実行してください"; exit 1; \
	fi
	git tag $(V)
	git push origin $(V)
	@git checkout develop 2>/dev/null || true
	@echo "✓ $(V) — GitHub Actions がリリースを作成します"
