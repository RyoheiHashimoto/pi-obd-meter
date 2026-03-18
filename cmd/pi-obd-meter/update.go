package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

// isValidSemver は version が semver 形式（v0.3.4 等）かを簡易判定する。
// dev ビルド（"dev-abc1234"）や空文字列は false。
func isValidSemver(v string) bool {
	return strings.HasPrefix(v, "v") && strings.Count(v, ".") >= 2
}

// tryAutoUpdate は起動時にGitHub Releasesから最新版をチェックし、
// 新しいバージョンがあればアトミックに差し替えて再起動する。
func tryAutoUpdate(ctx context.Context) {
	if version == "dev" || !isValidSemver(version) {
		return
	}

	updateCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	if !waitForInternet(updateCtx, 90*time.Second) {
		slog.Info("自動更新: インターネット未接続、スキップ")
		return
	}

	latest, found, err := selfupdate.DetectLatest(updateCtx,
		selfupdate.ParseSlug("RyoheiHashimoto/pi-obd-meter"))
	if err != nil {
		slog.Warn("自動更新: 最新バージョン検出失敗", "error", err)
		return
	}
	if !found {
		slog.Info("自動更新: リリースが見つかりません")
		return
	}
	if latest.LessOrEqual(version) {
		slog.Info("自動更新: 最新版で稼働中", "version", version)
		return
	}

	slog.Info("自動更新: 新バージョン検出", "current", version, "latest", latest.Version())
	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		slog.Error("自動更新: 実行パス取得失敗", "error", err)
		return
	}
	if err := selfupdate.UpdateTo(updateCtx, latest.AssetURL, latest.AssetName, exe); err != nil {
		slog.Error("自動更新: 更新失敗", "error", err)
		return
	}
	slog.Info("自動更新: 更新完了、再起動します", "version", latest.Version())
	os.Exit(0) // systemd Restart=always で新バイナリが起動
}

// waitForInternet はインターネット接続が利用可能になるまで待つ。
// タイムアウトまでに接続できなければ false を返す。
func waitForInternet(ctx context.Context, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	httpClient := &http.Client{Timeout: 5 * time.Second}

	// 初回即チェック
	if resp, err := httpClient.Get("https://api.github.com/zen"); err == nil {
		_ = resp.Body.Close()
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-ticker.C:
			if resp, err := httpClient.Get("https://api.github.com/zen"); err == nil {
				_ = resp.Body.Close()
				return true
			}
		}
	}
}
