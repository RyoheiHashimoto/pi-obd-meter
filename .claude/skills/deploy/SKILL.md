---
name: deploy
description: Pi にビルド・デプロイしてサービス状態を確認する
disable-model-invocation: true
allowed-tools: Bash, Read
---

Pi にデプロイする:

1. `make deploy` を実行（ARM64ビルド + rsync転送 + サービス再起動）
2. デプロイ完了後、サービス状態を確認:
   ```bash
   ssh laurel@pi-obd-meter.local "systemctl status pi-obd-meter --no-pager -l | head -15"
   ```
3. 結果をユーザーに簡潔に報告（成功/失敗 + エラーがあれば内容）

タイムアウト（Pi未接続等）の場合は、WiFi接続を確認するよう伝える。
