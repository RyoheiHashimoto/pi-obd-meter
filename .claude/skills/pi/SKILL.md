---
name: pi
description: Raspberry Pi のサービス管理（logs, restart, status, kiosk）
disable-model-invocation: true
argument-hint: "[command]"
allowed-tools: Bash
---

Pi をリモート操作する。$ARGUMENTS に応じて実行:

**logs** — リアルタイムログ表示:
```bash
ssh laurel@pi-obd-meter.local "journalctl -u pi-obd-meter -f --no-pager -n 30"
```

**restart** — サービス + キオスク再起動:
```bash
ssh laurel@pi-obd-meter.local "sudo systemctl restart pi-obd-meter && sudo systemctl restart kiosk"
```

**status** — サービス状態 + ヘルスチェック:
```bash
ssh laurel@pi-obd-meter.local "systemctl status pi-obd-meter --no-pager -l | head -15"
ssh laurel@pi-obd-meter.local "curl -s http://localhost:9090/api/health 2>/dev/null || echo 'API未応答'"
```

**kiosk** — キオスク（Chromium）のみ再起動:
```bash
ssh laurel@pi-obd-meter.local "sudo systemctl restart kiosk"
```

**kiosk-logs** — キオスクログ表示:
```bash
ssh laurel@pi-obd-meter.local "journalctl -u kiosk -f --no-pager -n 20"
```

**shutdown** — Pi を安全にシャットダウン（ユーザーに確認してから実行）:
```bash
ssh laurel@pi-obd-meter.local "sudo shutdown -h now"
```

**reboot** — Pi を再起動:
```bash
ssh laurel@pi-obd-meter.local "sudo reboot"
```

引数なしの場合は status を実行する。
SSH タイムアウト時は「Pi に接続できません」と報告する。
