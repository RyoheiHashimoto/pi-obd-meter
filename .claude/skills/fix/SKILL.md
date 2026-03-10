---
name: fix
description: 症状や issue から原因調査・修正・テスト・デプロイまで実行
disable-model-invocation: true
argument-hint: "[症状 or issue番号]"
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Task
---

症状または issue 番号 $ARGUMENTS から修正を行う:

## 手順

1. **原因調査**
   - 症状の場合: 関連するコードを検索・読解して原因を特定
   - issue 番号の場合: `gh issue view <番号>` で内容を確認してから調査
   - Pi のログが必要なら `ssh laurel@pi-obd-meter.local "journalctl -u pi-obd-meter --no-pager -n 50"` で確認

2. **原因をユーザーに報告**
   - 根本原因と修正方針を説明
   - ユーザーの確認を得てから修正に進む

3. **修正**
   - CLAUDE.md の規約に従う（日本語ログ、config.json で設定管理等）
   - 最小限の変更にとどめる

4. **テスト**
   - `make check` (lint + test) を実行
   - 失敗したら修正

5. **ユーザーに完了報告**
   - 変更内容のサマリー
   - デプロイするか確認
