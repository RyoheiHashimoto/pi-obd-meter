---
name: release
description: develop から main にマージしてリリースを作成する
disable-model-invocation: true
argument-hint: "[version]"
allowed-tools: Bash, Read
---

リリースを作成する:

1. 未コミットの変更がないか確認。あればユーザーに報告して中断
2. develop ブランチにいることを確認
3. `make check` (lint + test) を実行。失敗したら中断
4. バージョンを決定:
   - 引数 $ARGUMENTS があればそれを使う（例: v1.0.0）
   - なければ `git describe --tags --abbrev=0` から自動インクリメント
5. ユーザーにバージョンを確認してから実行
6. `make release V=<version>` を実行（develop→main マージ + タグ push）
7. GitHub Actions の Release ワークフローが起動したことを確認:
   ```bash
   gh run list --workflow=release.yml --limit=1
   ```
8. 結果を報告
