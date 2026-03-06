# pi-obd-meter Makefile
# ローカル開発用。Pi デプロイは ./scripts/deploy.sh を使用

.PHONY: test test-cover lint build build-arm64 clean check deploy deploy-web logs

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

deploy-web:
	./scripts/deploy.sh deploy-web

logs:
	./scripts/deploy.sh logs
