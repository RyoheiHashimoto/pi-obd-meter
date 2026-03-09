// Package web provides embedded static files for the meter UI.
package web

import "embed"

// StaticFS はメーターUI の静的ファイル（meter.html, meter.css, meter.js）を埋め込む。
// ファイルは "static/" プレフィックス付きで格納される。
// 使用時は fs.Sub(StaticFS, "static") でプレフィックスを剥がす。
//
//go:embed static
var StaticFS embed.FS
