---
name: push
description: 変更をまとめてコミットし develop ブランチに push する
disable-model-invocation: true
allowed-tools: Bash, Read, Grep, Glob
---

現在の変更を commit して develop に push する:

1. `git status` と `git diff` で変更内容を確認
2. 変更内容から適切な日本語コミットメッセージを自動生成
   - feat: / fix: / refactor: / docs: / ci: などの prefix を使う
   - 本文は変更の要約を簡潔に
   - Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com> を付ける
3. 変更ファイルを `git add` してコミット
4. `git push origin develop` で push
5. push 結果を報告

注意:
- develop ブランチにいることを確認。main にいる場合はユーザーに確認する
- .env やクレデンシャルファイルはコミットしない
- 変更がない場合は「変更なし」と報告
