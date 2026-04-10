package sdlui

import (
	"fmt"
	"log/slog"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

const maxCacheSize = 512

// FontManager はフォントの読み込みとテキストテクスチャキャッシュを管理する
type FontManager struct {
	renderer *sdl.Renderer
	fonts    map[fontKey]*ttf.Font
	cache    map[cacheKey]*TextTexture
	order    []cacheKey // 挿入順（LRU eviction 用）
}

type fontKey struct {
	path string
	size int
}

type cacheKey struct {
	text  string
	font  fontKey
	color RGBA
}

// TextTexture はレンダリング済みテキストテクスチャ
type TextTexture struct {
	Texture *sdl.Texture
	W, H    int32
}

// NewFontManager は新しいフォントマネージャーを作成する
func NewFontManager(renderer *sdl.Renderer) *FontManager {
	return &FontManager{
		renderer: renderer,
		fonts:    make(map[fontKey]*ttf.Font),
		cache:    make(map[cacheKey]*TextTexture),
	}
}

// LoadFont はフォントファイルを読み込む（同じpath+sizeは1回だけ）
func (fm *FontManager) LoadFont(path string, size int) error {
	key := fontKey{path, size}
	if _, ok := fm.fonts[key]; ok {
		return nil
	}
	font, err := ttf.OpenFont(path, size)
	if err != nil {
		return fmt.Errorf("フォント読み込み失敗 %s@%d: %w", path, size, err)
	}
	fm.fonts[key] = font
	slog.Debug("フォント読み込み", "path", path, "size", size)
	return nil
}

// GetFont は読み込み済みフォントを返す
func (fm *FontManager) GetFont(path string, size int) *ttf.Font {
	return fm.fonts[fontKey{path, size}]
}

// RenderText はテキストをテクスチャとして描画する（キャッシュあり）
// 同じ text+font+color の組み合わせはキャッシュから返す
func (fm *FontManager) RenderText(text string, fontPath string, fontSize int, color RGBA) *TextTexture {
	if text == "" {
		return nil
	}

	key := cacheKey{text, fontKey{fontPath, fontSize}, color}
	if cached, ok := fm.cache[key]; ok {
		return cached
	}

	font := fm.fonts[fontKey{fontPath, fontSize}]
	if font == nil {
		return nil
	}

	// キャッシュサイズ制限: 古いエントリを削除
	if len(fm.cache) >= maxCacheSize {
		fm.evictOldest(maxCacheSize / 4)
	}

	surface, err := font.RenderUTF8Blended(text, color.ToSDLColor())
	if err != nil {
		slog.Warn("テキストレンダリング失敗", "text", text, "error", err)
		return nil
	}
	defer surface.Free()

	texture, err := fm.renderer.CreateTextureFromSurface(surface)
	if err != nil {
		slog.Warn("テクスチャ作成失敗", "error", err)
		return nil
	}

	tt := &TextTexture{
		Texture: texture,
		W:       surface.W,
		H:       surface.H,
	}
	fm.cache[key] = tt
	fm.order = append(fm.order, key)
	return tt
}

// evictOldest は古いキャッシュエントリを n 件削除する
func (fm *FontManager) evictOldest(n int) {
	removed := 0
	for i := 0; i < len(fm.order) && removed < n; i++ {
		key := fm.order[i]
		if v, ok := fm.cache[key]; ok {
			v.Texture.Destroy()
			delete(fm.cache, key)
			removed++
		}
	}
	if removed > 0 && removed < len(fm.order) {
		fm.order = fm.order[removed:]
	} else if removed >= len(fm.order) {
		fm.order = fm.order[:0]
	}
}

// DrawTextCentered はテキストを中央揃えで描画する
func (fm *FontManager) DrawTextCentered(text string, fontPath string, fontSize int, color RGBA, cx, cy float64) {
	tt := fm.RenderText(text, fontPath, fontSize, color)
	if tt == nil {
		return
	}
	dst := sdl.Rect{
		X: int32(cx) - tt.W/2,
		Y: int32(cy) - tt.H/2,
		W: tt.W,
		H: tt.H,
	}
	fm.renderer.Copy(tt.Texture, nil, &dst)
}

// DrawTextRight はテキストを右揃えで描画する
func (fm *FontManager) DrawTextRight(text string, fontPath string, fontSize int, color RGBA, rx, cy float64) {
	tt := fm.RenderText(text, fontPath, fontSize, color)
	if tt == nil {
		return
	}
	dst := sdl.Rect{
		X: int32(rx) - tt.W,
		Y: int32(cy) - tt.H/2,
		W: tt.W,
		H: tt.H,
	}
	fm.renderer.Copy(tt.Texture, nil, &dst)
}

// Destroy は全フォントとテクスチャを解放する
func (fm *FontManager) Destroy() {
	for _, v := range fm.cache {
		v.Texture.Destroy()
	}
	fm.cache = nil
	for _, f := range fm.fonts {
		f.Close()
	}
	fm.fonts = nil
}
