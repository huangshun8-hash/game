package httpx

import (
	"strings"
	"testing"
)

// TestAssetContentTypesCoverAllExtensions 锁定「可托管的静态资源扩展名均有显式 Content-Type」。
// 若某个 staticAssetExtensions 中的扩展名在 assetContentTypes 里缺失，
// 该资源的类型会回落到 fasthttp 依宿主机 MIME 库（Windows 注册表 / /etc/mime.types）推断，
// 结果随机器而变——.mjs/.cjs 会被当成 text/plain，浏览器严格 MIME 校验直接拒绝执行。
func TestAssetContentTypesCoverAllExtensions(t *testing.T) {
	for ext := range staticAssetExtensions {
		if _, ok := assetContentTypes[ext]; !ok {
			t.Errorf("静态资源扩展名 %q 缺少显式 Content-Type，会回落到宿主机 MIME 库导致跨平台不一致", ext)
		}
	}
	// 反向检查：显式映射里的键必须是会被静态托管识别的扩展名，避免出现永不生效的死条目。
	for ext := range assetContentTypes {
		if _, ok := staticAssetExtensions[ext]; !ok {
			t.Errorf("assetContentTypes 中的 %q 不在 staticAssetExtensions 内，该条目永不生效", ext)
		}
	}
}

// TestAssetContentTypesAreLowercase 锁定映射键全小写。
// 查表时用 strings.ToLower 折叠扩展名，键中一旦出现大写字母将永远无法命中。
func TestAssetContentTypesAreLowercase(t *testing.T) {
	for ext, ct := range assetContentTypes {
		if ext == "" {
			t.Fatal("assetContentTypes 不允许包含空键")
		}
		if lower := strings.ToLower(ext); lower != ext {
			t.Errorf("扩展名 %q 含大写字母，将永远无法命中，请改为 %q", ext, lower)
		}
		if ct == "" {
			t.Errorf("扩展名 %q 的 Content-Type 为空", ext)
		}
	}
}

// TestContentTypeForJSModules 锁定 ES module 相关扩展名一律返回 javascript 类型。
// 这是「前端页面白屏、控制台报 MIME type 错误」故障的直接防线：
// NODE_ENV 产物中的 .mjs/.cjs 必须在任何宿主机上都以 javascript 返回。
func TestContentTypeForJSModules(t *testing.T) {
	for _, ext := range []string{".js", ".mjs", ".cjs"} {
		got := contentTypeForAsset("/assets/chunk-abc123" + ext)
		if !strings.Contains(got, "javascript") {
			t.Errorf("contentTypeForAsset(%q) = %q，期望包含 javascript", ext, got)
		}
	}
}

// TestContentTypeForAssetUnknownAndUppercase 校验未知扩展名返回空串（沿用 SendFile 结果），
// 且扩展名大小写不影响命中（URL 里大小写混写是常见的客户端行为）。
func TestContentTypeForAssetUnknownAndUppercase(t *testing.T) {
	if got := contentTypeForAsset("/.well-known/assetlinks"); got != "" {
		t.Errorf("无扩展名路径应返回空串，实际 %q", got)
	}
	if got := contentTypeForAsset("/assets/data.bin"); got != "" {
		t.Errorf("未登记的扩展名应返回空串，实际 %q", got)
	}
	// 大写扩展名同样应命中映射
	if got := contentTypeForAsset("/assets/mod.MJS"); !strings.Contains(got, "javascript") {
		t.Errorf("大写扩展名 .MJS 应命中 javascript 映射，实际 %q", got)
	}
}
