PI = pi@raspberrypi.local
DEST = /opt/pi-obd-meter

# --- ビルド ---

build:
	GOOS=linux GOARCH=arm64 go build -o bin/pi-obd-meter ./cmd/pi-obd-meter
	GOOS=linux GOARCH=arm64 go build -o bin/pi-obd-scanner ./cmd/pi-obd-scanner

build-local:
	go build -o bin/pi-obd-meter ./cmd/pi-obd-meter
	go build -o bin/pi-obd-scanner ./cmd/pi-obd-scanner

# --- デプロイ ---

deploy: build
	rsync -avz bin/ $(PI):$(DEST)/
	rsync -avz web/static/ $(PI):$(DEST)/web/static/
	rsync -avz configs/ $(PI):$(DEST)/configs/
	ssh $(PI) 'sudo systemctl restart pi-obd-meter'
	@echo "✓ デプロイ完了"

deploy-web:
	rsync -avz web/static/ $(PI):$(DEST)/web/static/
	ssh $(PI) 'sudo systemctl restart pi-obd-meter'
	@echo "✓ Web UIのみデプロイ完了"

# --- ラズパイ初期セットアップ（初回のみ） ---

setup-pi:
	ssh $(PI) 'sudo mkdir -p $(DEST)/web/static $(DEST)/configs'
	ssh $(PI) 'sudo chown -R pi:pi $(DEST)'
	$(MAKE) deploy
	rsync -avz configs/pi-obd-meter.service $(PI):/tmp/pi-obd-meter.service
	ssh $(PI) 'sudo cp /tmp/pi-obd-meter.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable pi-obd-meter'
	@echo "✓ 初期セットアップ完了"

# --- ユーティリティ ---

ssh:
	ssh $(PI)

logs:
	ssh $(PI) 'journalctl -u pi-obd-meter -f'

status:
	ssh $(PI) 'systemctl status pi-obd-meter'

restart:
	ssh $(PI) 'sudo systemctl restart pi-obd-meter'

# --- overlayFS（SD保護） ---

overlay-on:
	ssh $(PI) 'sudo raspi-config nonint do_overlayfs 0'
	@echo "⚠️  overlayFS有効化予約済み。再起動で有効になる"
	@echo "    ssh $(PI) 'sudo reboot'"

overlay-off:
	ssh $(PI) 'sudo raspi-config nonint do_overlayfs 1'
	@echo "⚠️  overlayFS無効化予約済み。再起動で無効になる"
	@echo "    ssh $(PI) 'sudo reboot'"

clean:
	rm -rf bin/

.PHONY: build build-local deploy deploy-web setup-pi ssh logs status restart overlay-on overlay-off clean
