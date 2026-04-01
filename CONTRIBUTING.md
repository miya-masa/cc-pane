# Contributing to cc-pane

cc-pane へのコントリビューションを歓迎します。

## 開発環境

- Go 1.22 以上
- tmux (動作確認用)

## ビルド・テスト

```bash
make build    # ビルド
make test     # テスト実行
make lint     # go vet
```

## コーディング規約

- `gofmt` でフォーマットすること
- `go vet` をパスすること
- 外部ライブラリへの依存は追加しない (標準ライブラリのみ)

## コミットメッセージ

[Conventional Commits](https://www.conventionalcommits.org/) 形式を使用してください:

| Prefix | 用途 |
|--------|------|
| `feat:` | 新機能 |
| `fix:` | バグ修正 |
| `docs:` | ドキュメント変更 |
| `refactor:` | リファクタリング |
| `test:` | テスト追加・修正 |
| `chore:` | ビルド・CI 等の雑務 |

## Issue

- **バグ報告**: 再現手順、期待動作、実際の動作を記載してください
- **機能要望**: ユースケースと期待する動作を記載してください

## Pull Request

1. `main` ブランチから feature ブランチを作成
2. 変更を実装し、テストを追加
3. `make test` と `make lint` がパスすることを確認
4. `main` ブランチに対して PR を作成

1つの PR は1つの変更にフォーカスしてください。
