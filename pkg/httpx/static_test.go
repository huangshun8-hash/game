package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"kemiluo/pkg/logx"
)

// testLogDir 保存 TestMain 为测试指定的日志目录，供日志级别断言用例读取真实落盘内容。
var testLogDir string

// TestMain 将全局日志重定向到临时目录，避免测试在包目录下生成 logs/。
// 级别保持默认 info，这样「静态成功请求降级为 debug」可以通过「日志里查不到」来验证。
func TestMain(m *testing.M) {
	cfg := logx.DefaultConfig()
	cfg.Dir = filepath.Join(os.TempDir(), "httpx_static_test_logs")
	cfg.Console = false
	testLogDir = cfg.Dir
	logx.Init(cfg)
	code := m.Run()
	logx.Sync()
	_ = os.RemoveAll(cfg.Dir)
	os.Exit(code)
}

// readTestLogs 读取测试日志目录下的全部内容，用于断言某条日志是否落盘。
func readTestLogs(t *testing.T) string {
	t.Helper()
	logx.Sync()
	entries, err := os.ReadDir(testLogDir)
	if err != nil {
		t.Fatalf("读取日志目录失败: %v", err)
	}
	var all strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(testLogDir, entry.Name()))
		if readErr != nil {
			t.Fatalf("读取日志文件 %s 失败: %v", entry.Name(), readErr)
		}
		all.Write(content)
	}
	return all.String()
}

// newTestDist 构造一份最小化的前端产物目录，返回目录路径。
// 不使用 t.TempDir()：Fiber 的 SendFile 底层是 fasthttp FS，会按 CacheDuration（默认 10s）
// 缓存已打开的文件句柄，Windows 上句柄未释放前无法删除文件，t.TempDir() 的强制清理会让用例失败。
// 这里改为自建临时目录 + 尽力清理（忽略删除失败），残留由操作系统临时目录回收。
func newTestDist(t *testing.T) string {
	t.Helper()
	dist, err := os.MkdirTemp("", "httpx_dist_")
	if err != nil {
		t.Fatalf("创建临时产物目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dist) })
	for _, dir := range []string{"assets", ".git", ".well-known"} {
		if err := os.MkdirAll(filepath.Join(dist, dir), 0o755); err != nil {
			t.Fatalf("创建 %s 目录失败: %v", dir, err)
		}
	}
	files := map[string]string{
		"index.html":              "<html><body>spa</body></html>",
		"favicon.ico":             "icon",
		"assets/index-abc123.js":  "export const answer = 42",
		"assets/index-abc123.css": ".a{color:red}",
		"assets/mod-abc123.mjs":   "export default 1",
		"assets/logo space.png":   "spaced-png",
		".env":                    "SECRET=leaked",
		".git/config":             "[core] leaked",
		".well-known/assetlinks":  "acme-token",
		"sub/index.html":          "<html><body>sub-spa</body></html>",
	}
	for name, body := range files {
		full := filepath.Join(dist, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("创建 %s 父目录失败: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", name, err)
		}
	}
	return dist
}

// newTestApp 构造挂载了静态中间件的 Fiber 应用，错误处理器与线上保持一致，
// 以便验证静态资源不会被 DefaultErrorHandler 转成 HTTP 200 信封。
func newTestApp(t *testing.T, dist string) *fiber.App {
	t.Helper()
	app := newBareTestApp(t)
	app.Use(NewStaticServer(dist, "").Middleware())
	// 后续业务路由：用于验证 /api 与未知路径被正确放行。
	app.Get("/api/ping", func(c fiber.Ctx) error {
		return c.SendString("pong")
	})
	return app
}

// newBareTestApp 构造未挂载任何中间件的测试应用，配置与线上一致。
func newBareTestApp(t *testing.T) *fiber.App {
	t.Helper()
	return fiber.NewWithCustomCtx(func(app *fiber.App) fiber.CustomCtx {
		return NewCtx(app)
	}, fiber.Config{ErrorHandler: DefaultErrorHandler})
}

// doRequest 发起测试请求，返回响应对象与响应体。
func doRequest(t *testing.T, app *fiber.App, target, accept string) (*http.Response, string) {
	t.Helper()
	return doRequestWith(t, app, http.MethodGet, target, map[string]string{fiber.HeaderAccept: accept})
}

// doRequestWith 以指定方法与请求头发起测试请求，返回响应对象与响应体。
// header 中值为空的项不设置，便于复用同一份表格。
func doRequestWith(t *testing.T, app *fiber.App, method, target string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for key, value := range header {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("请求 %s %s 失败: %v", method, target, err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 %s 响应体失败: %v", target, err)
	}
	_ = resp.Body.Close()
	return resp, string(body)
}

// TestResolveFileHitsRealAssets 回归用例：Linux 上 filepath.IsAbs("/assets/x.js") 恒为 true，
// 旧实现据此把全部静态请求判为路径穿越，导致所有资源回退到 index.html 并以 text/html 响应。
func TestResolveFileHitsRealAssets(t *testing.T) {
	s := NewStaticServer(newTestDist(t), "")

	cases := []struct {
		urlPath string
		wantRel string
	}{
		{"/", "index.html"},
		{"/index.html", "index.html"},
		{"/favicon.ico", "favicon.ico"},
		{"/assets/index-abc123.js", filepath.Join("assets", "index-abc123.js")},
		{"/assets/index-abc123.css", filepath.Join("assets", "index-abc123.css")},
		{"/assets/mod-abc123.mjs", filepath.Join("assets", "mod-abc123.mjs")},
		// 冗余斜杠与 . 段清洗后仍应命中
		{"//assets/index-abc123.js", filepath.Join("assets", "index-abc123.js")},
		{"/assets/./index-abc123.js", filepath.Join("assets", "index-abc123.js")},
		// percent 转义需先解码再映射（Fiber 的 UnescapePath 默认关闭）
		{"/assets/logo%20space.png", filepath.Join("assets", "logo space.png")},
		// .well-known 是隐藏段拦截规则的白名单例外
		{"/.well-known/assetlinks", filepath.Join(".well-known", "assetlinks")},
	}
	for _, tc := range cases {
		full, ok := s.resolveFile(tc.urlPath)
		if !ok {
			t.Errorf("resolveFile(%q) 未命中，期望命中 %s", tc.urlPath, tc.wantRel)
			continue
		}
		if want := filepath.Join(s.rootAbs, tc.wantRel); full != want {
			t.Errorf("resolveFile(%q) = %q，期望 %q", tc.urlPath, full, want)
		}
	}
}

// TestResolveFileRejectsTraversal 校验路径穿越防护。
// 注意：前导 ".." 会被 path.Clean 折叠回根内（/../secret.js -> <root>/secret.js），
// 因此这里为每个用例都在根目录外放置了真实文件，逃逸成功即可读到，用例才有判别力。
func TestResolveFileRejectsTraversal(t *testing.T) {
	dist := newTestDist(t)
	// 在产物目录的兄弟位置放一个敏感文件，穿越成功即可读到。
	secret := filepath.Join(filepath.Dir(dist), "secret.js")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatalf("写入 secret 失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(secret) })

	s := NewStaticServer(dist, "")

	cases := []string{
		"/../secret.js",
		"/../../etc/passwd",
		"/assets/../../secret.js",
		// Windows 下反斜杠不被 path.Clean 视为分隔符，需靠隐藏段与根目录前缀检查兜住
		`/..\secret.js`,
		`/..\..\etc\passwd`,
		// percent 编码的穿越：解码后仍走同一套清洗与前缀检查
		"/%2e%2e/secret.js",
		"/assets/%2e%2e/%2e%2e/secret.js",
		// 非法转义序列不做部分解码
		"/assets/%zz.js",
		// 隐藏文件不得下载
		"/.env",
		"/.git/config",
		// 目录本身不应作为文件返回
		"/assets",
	}
	for _, p := range cases {
		if full, ok := s.resolveFile(p); ok {
			t.Errorf("resolveFile(%q) 未被拒绝，解析到 %q", p, full)
		}
	}
}

// TestResolveFileRejectsSymlinkEscape 校验产物目录内指向根目录之外的符号链接不会被送出。
// os.Stat 会跟随链接，仅靠字符串前缀检查发现不了这种逃逸。
func TestResolveFileRejectsSymlinkEscape(t *testing.T) {
	dist := newTestDist(t)
	outside := filepath.Join(filepath.Dir(dist), "outside-secret.js")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("写入外部文件失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	link := filepath.Join(dist, "assets", "escape.js")
	if err := os.Symlink(outside, link); err != nil {
		// Windows 未开启开发者模式时创建符号链接需要管理员权限
		t.Skipf("当前环境无法创建符号链接，跳过: %v", err)
	}

	s := NewStaticServer(dist, "")
	if full, ok := s.resolveFile("/assets/escape.js"); ok {
		t.Errorf("指向根目录外的符号链接未被拒绝，解析到 %q", full)
	}

	// 对照：指向根目录内的符号链接应正常命中
	inner := filepath.Join(dist, "assets", "inner.js")
	if err := os.Symlink(filepath.Join(dist, "assets", "index-abc123.js"), inner); err != nil {
		t.Skipf("创建内部符号链接失败，跳过对照检查: %v", err)
	}
	if _, ok := s.resolveFile("/assets/inner.js"); !ok {
		t.Error("指向根目录内的符号链接被误拒")
	}
}

// TestResolveFileEnforcesRealRoot 校验「真实路径必须落在 rootReal 之下」这道检查确实生效。
// 不依赖创建符号链接（Windows 未开启开发者模式时无权限），改为直接把 rootReal 指向别处，
// 等价于「解析出的真实路径逃出了根目录」这一情形。
func TestResolveFileEnforcesRealRoot(t *testing.T) {
	dist := newTestDist(t)
	s := NewStaticServer(dist, "")
	if _, ok := s.resolveFile("/assets/index-abc123.js"); !ok {
		t.Fatal("正常状态下应命中，测试前提已变化")
	}

	other, err := os.MkdirTemp("", "httpx_other_root_")
	if err != nil {
		t.Fatalf("创建对照目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	s.rootReal = other

	if full, ok := s.resolveFile("/assets/index-abc123.js"); ok {
		t.Errorf("真实路径不在 rootReal 之下时未被拒绝，解析到 %q", full)
	}

	// rootReal 为空表示启动时根目录不可解析，此时退化为仅做前缀检查，不应误拒
	s.rootReal = ""
	if _, ok := s.resolveFile("/assets/index-abc123.js"); !ok {
		t.Error("rootReal 为空时应退化为仅前缀检查，不应拒绝根目录内的文件")
	}
}

// TestStaticAssetContentTypeAndCache 校验命中的静态资源 MIME 与缓存头。
// 这是本次故障的核心：ES module 必须以 javascript 类型返回，否则浏览器严格 MIME 校验失败。
func TestStaticAssetContentTypeAndCache(t *testing.T) {
	app := newTestApp(t, newTestDist(t))

	cases := []struct {
		urlPath     string
		wantCTPart  string
		wantCache   string
		wantBodyHas string
	}{
		{"/assets/index-abc123.js", "javascript", assetCacheControl, "answer = 42"},
		{"/assets/mod-abc123.mjs", "javascript", assetCacheControl, "export default"},
		{"/assets/index-abc123.css", "text/css", assetCacheControl, "color:red"},
		{"/favicon.ico", "", assetCacheControl, "icon"},
		{"/assets/logo%20space.png", "image/png", assetCacheControl, "spaced-png"},
		{"/", "text/html", revalidateCacheControl, "spa"},
		{"/index.html", "text/html", revalidateCacheControl, "spa"},
		// 子目录下的 index.html 同样是入口文档，不得长缓存
		{"/sub/index.html", "text/html", revalidateCacheControl, "sub-spa"},
		// /.well-known 下的校验文件路径固定、内容会变，同样不得长缓存
		{"/.well-known/assetlinks", "", revalidateCacheControl, "acme-token"},
	}
	for _, tc := range cases {
		// 浏览器加载 ES module 时 Accept 为 */*，正是旧实现回退到 index.html 的触发条件
		resp, body := doRequest(t, app, tc.urlPath, "*/*")
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s 状态码 = %d，期望 200", tc.urlPath, resp.StatusCode)
			continue
		}
		ct := resp.Header.Get(fiber.HeaderContentType)
		if tc.wantCTPart != "" && !strings.Contains(ct, tc.wantCTPart) {
			t.Errorf("GET %s Content-Type = %q，期望包含 %q", tc.urlPath, ct, tc.wantCTPart)
		}
		if strings.HasSuffix(tc.urlPath, ".js") || strings.HasSuffix(tc.urlPath, ".mjs") {
			if strings.Contains(ct, "text/html") {
				t.Errorf("GET %s 以 text/html 返回，会触发浏览器严格 MIME 校验失败", tc.urlPath)
			}
		}
		if got := resp.Header.Get(fiber.HeaderCacheControl); got != tc.wantCache {
			t.Errorf("GET %s Cache-Control = %q，期望 %q", tc.urlPath, got, tc.wantCache)
		}
		if !strings.Contains(body, tc.wantBodyHas) {
			t.Errorf("GET %s 响应体 = %q，期望包含 %q", tc.urlPath, body, tc.wantBodyHas)
		}
	}
}

// TestCacheControlFor 校验缓存头按状态码收口：
// fasthttp FS 的内部错误（如 500）不会以 error 形式从 SendFile 返回，
// 若无条件下发 immutable 长缓存，错误响应会被浏览器与 CDN 长期复用。
func TestCacheControlFor(t *testing.T) {
	cases := []struct {
		status         int
		mustRevalidate bool
		want           string
	}{
		{http.StatusOK, false, assetCacheControl},
		{http.StatusPartialContent, false, assetCacheControl},
		{http.StatusNotModified, false, assetCacheControl},
		{http.StatusOK, true, revalidateCacheControl},
		{http.StatusNotModified, true, revalidateCacheControl},
		{http.StatusInternalServerError, false, noStoreCacheControl},
		{http.StatusInternalServerError, true, noStoreCacheControl},
		{http.StatusNotFound, false, noStoreCacheControl},
		{http.StatusForbidden, false, noStoreCacheControl},
		{http.StatusRequestedRangeNotSatisfiable, false, noStoreCacheControl},
	}
	for _, tc := range cases {
		if got := cacheControlFor(tc.mustRevalidate, tc.status); got != tc.want {
			t.Errorf("cacheControlFor(%v, %d) = %q，期望 %q", tc.mustRevalidate, tc.status, got, tc.want)
		}
	}
}

// TestStaticAssetByteRange 校验 Range 请求返回 206 与 Accept-Ranges，
// 否则视频与大体积产物的拖动会退化为整包下载。
func TestStaticAssetByteRange(t *testing.T) {
	app := newTestApp(t, newTestDist(t))

	resp, body := doRequestWith(t, app, http.MethodGet, "/assets/index-abc123.js", map[string]string{
		fiber.HeaderAccept: "*/*",
		"Range":            "bytes=0-5",
	})
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("Range 请求状态码 = %d，期望 206", resp.StatusCode)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q，期望 bytes", got)
	}
	if body != "export" {
		t.Errorf("Range 响应体 = %q，期望 %q", body, "export")
	}
	if got := resp.Header.Get(fiber.HeaderCacheControl); got != assetCacheControl {
		t.Errorf("206 响应 Cache-Control = %q，期望 %q", got, assetCacheControl)
	}
}

// TestMissingAssetReturnsRealNotFound 校验缺失的静态资源返回真实 404 纯文本，
// 而不是被 DefaultErrorHandler 转成 HTTP 200 + application/json（那样浏览器依旧报 MIME 错误）。
func TestMissingAssetReturnsRealNotFound(t *testing.T) {
	app := newTestApp(t, newTestDist(t))

	for _, p := range []string{
		"/assets/missing-xyz.js",
		"/assets/missing-xyz.mjs",
		"/assets/missing-xyz.css",
		"/assets/missing-xyz.wasm",
		"/assets/missing-xyz.js.map",
		"/assets/missing-xyz.woff2",
	} {
		resp, body := doRequest(t, app, p, "*/*")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s 状态码 = %d，期望 404（不得退化为业务信封 200）", p, resp.StatusCode)
		}
		ct := resp.Header.Get(fiber.HeaderContentType)
		if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/json") {
			t.Errorf("GET %s Content-Type = %q，不应为 html/json", p, ct)
		}
		if got := resp.Header.Get(fiber.HeaderCacheControl); got != noStoreCacheControl {
			t.Errorf("GET %s Cache-Control = %q，期望 %q（404 不得被缓存）", p, got, noStoreCacheControl)
		}
		if strings.Contains(body, "spa") {
			t.Errorf("GET %s 返回了 index.html 内容，SPA 回退未被正确阻断", p)
		}
	}
}

// TestHiddenFilesNotLeaked 校验隐藏文件不会经静态托管泄露。
// .env / .git 这类文件若被误拷进产物目录，必须拿不到内容。
func TestHiddenFilesNotLeaked(t *testing.T) {
	app := newTestApp(t, newTestDist(t))

	for _, tc := range []struct {
		urlPath  string
		leakWord string
	}{
		{"/.env", "SECRET=leaked"},
		{"/.git/config", "[core] leaked"},
	} {
		resp, body := doRequest(t, app, tc.urlPath, "*/*")
		if strings.Contains(body, tc.leakWord) {
			t.Errorf("GET %s 泄露了隐藏文件内容: %q", tc.urlPath, body)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s 状态码 = %d，期望 200（SPA 回退）或 404", tc.urlPath, resp.StatusCode)
		}
	}
}

// TestSPAHistoryFallback 校验前端路由深链回退到 index.html。
func TestSPAHistoryFallback(t *testing.T) {
	app := newTestApp(t, newTestDist(t))

	for _, p := range []string{"/dashboard", "/dashboard/players", "/config/releases/v1"} {
		resp, body := doRequest(t, app, p, "text/html,application/xhtml+xml")
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s 状态码 = %d，期望 200", p, resp.StatusCode)
		}
		if !strings.Contains(body, "spa") {
			t.Errorf("GET %s 未回退到 index.html，响应体 = %q", p, body)
		}
		if got := resp.Header.Get(fiber.HeaderCacheControl); got != revalidateCacheControl {
			t.Errorf("GET %s Cache-Control = %q，期望 %q（入口 HTML 不得长缓存）", p, got, revalidateCacheControl)
		}
	}
}

// TestNonGetMethodsNotServed 校验非 GET/HEAD 方法不会被静态中间件响应，
// 否则 POST /assets/x.js 会被静默回以 200 + 文件内容，掩盖客户端调用错误。
func TestNonGetMethodsNotServed(t *testing.T) {
	app := newTestApp(t, newTestDist(t))

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		for _, p := range []string{"/assets/index-abc123.js", "/", "/dashboard"} {
			_, body := doRequestWith(t, app, method, p, map[string]string{fiber.HeaderAccept: "*/*"})
			if strings.Contains(body, "answer = 42") || strings.Contains(body, "<body>spa") {
				t.Errorf("%s %s 返回了静态文件内容: %q", method, p, body)
			}
		}
	}

	// HEAD 属于静态内容的正常访问方式，须继续命中
	resp, _ := doRequestWith(t, app, http.MethodHead, "/assets/index-abc123.js", map[string]string{fiber.HeaderAccept: "*/*"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD 静态资源状态码 = %d，期望 200", resp.StatusCode)
	}
	if got := resp.Header.Get(fiber.HeaderCacheControl); got != assetCacheControl {
		t.Errorf("HEAD 静态资源 Cache-Control = %q，期望 %q", got, assetCacheControl)
	}
}

// TestAPIPathNotSwallowed 校验 /api 前缀不被静态中间件吞掉。
func TestAPIPathNotSwallowed(t *testing.T) {
	app := newTestApp(t, newTestDist(t))

	resp, body := doRequest(t, app, "/api/ping", "*/*")
	if resp.StatusCode != http.StatusOK || body != "pong" {
		t.Errorf("GET /api/ping = %d %q，期望 200 \"pong\"", resp.StatusCode, body)
	}

	// /apifoo 与 /api 只是前缀相近，不属于 API 路径，应按静态请求处理（此处回退到 index.html）
	_, fooBody := doRequest(t, app, "/apifoo", "text/html")
	if !strings.Contains(fooBody, "spa") {
		t.Errorf("GET /apifoo 未按静态请求处理，响应体 = %q", fooBody)
	}
}

// TestIsAPIPath 校验 API 路径判定精确到路径段边界。
func TestIsAPIPath(t *testing.T) {
	cases := map[string]bool{
		"/api":              true,
		"/api/":             true,
		"/api/admin/login":  true,
		"/apifoo":           false,
		"/apifoo/bar":       false,
		"/":                 false,
		"/assets/api.js":    false,
		"/public/api/thing": false,
	}
	for path, want := range cases {
		if got := isAPIPath(path); got != want {
			t.Errorf("isAPIPath(%q) = %v，期望 %v", path, got, want)
		}
	}
}

// TestHasAssetExtension 校验资源扩展名判定：不含 .html，保证 SPA 深链仍可回退。
func TestHasAssetExtension(t *testing.T) {
	cases := map[string]bool{
		"/assets/app.js":         true,
		"/assets/app.JS":         true, // 大小写不敏感
		"/assets/app.mjs":        true,
		"/assets/app.js.map":     true,
		"/assets/logo.svg":       true,
		"/assets/font.woff2":     true,
		"/index.html":            false, // 保留 SPA 回退能力
		"/dashboard/players":     false,
		"/config/releases/v1.2":  false, // 版本号中的点不应被误判
		"/":                      false,
		"/api/admin/login":       false,
		"/assets/bundle.unknown": false,
	}
	for path, want := range cases {
		if got := hasAssetExtension(path); got != want {
			t.Errorf("hasAssetExtension(%q) = %v，期望 %v", path, got, want)
		}
	}
}

// TestIsRevalidatePath 校验「必须回源校验」的路径判定。
func TestIsRevalidatePath(t *testing.T) {
	cases := map[string]bool{
		"/":                             true,
		"/index.html":                   true,
		"/sub/index.html":               true,
		"/page.HTM":                     true,
		"/.well-known/acme-challenge/x": true,
		"/assets/index-abc123.js":       false,
		"/favicon.ico":                  false,
		"/dashboard/players":            false,
	}
	for path, want := range cases {
		if got := isRevalidatePath(path); got != want {
			t.Errorf("isRevalidatePath(%q) = %v，期望 %v", path, got, want)
		}
	}
}

// TestHasHiddenSegment 校验隐藏段判定与 .well-known 白名单。
func TestHasHiddenSegment(t *testing.T) {
	cases := map[string]bool{
		"/.env":                   true,
		"/.git/config":            true,
		"/assets/.hidden.js":      true,
		`/..\secret.js`:           true,
		"/.well-known/assetlinks": false,
		"/assets/index-abc123.js": false,
		"/index.html":             false,
		"/":                       false,
	}
	for path, want := range cases {
		if got := hasHiddenSegment(path); got != want {
			t.Errorf("hasHiddenSegment(%q) = %v，期望 %v", path, got, want)
		}
	}
}

// TestStaticMarksAssetRequest 校验静态资源与 404 均被标记为静态请求，
// 日志中间件据此跳过响应体记录（避免数 MB 的 bundle 与二进制乱码入库）并把成功日志降级为 Debug。
func TestStaticMarksAssetRequest(t *testing.T) {
	dist := newTestDist(t)
	var marked []bool
	app := newBareTestApp(t)
	// 模拟 LogMiddleware 的位置：注册在静态中间件之前，在 Next() 之后读取标记
	app.Use(func(c fiber.Ctx) error {
		err := c.Next()
		marked = append(marked, isStaticAssetRequest(c))
		return err
	})
	app.Use(NewStaticServer(dist, "").Middleware())

	for _, p := range []string{"/assets/index-abc123.js", "/", "/assets/missing.js"} {
		doRequest(t, app, p, "*/*")
	}
	for i, ok := range marked {
		if !ok {
			t.Errorf("第 %d 个静态请求未被标记为静态资源", i)
		}
	}

	// 反向确认：静态资源的 Content-Type 若不跳过，会被 bodyLogField 全量写入日志
	for _, ct := range []string{
		"text/javascript; charset=utf-8",
		"text/css; charset=utf-8",
		"image/png",
		"font/woff2",
	} {
		if _, ok := bodyLogField("respBody", ct, []byte("x")); !ok {
			t.Errorf("bodyLogField(%q) 未返回字段，测试前提已变化，请复核跳过逻辑", ct)
		}
	}
}

// TestStaticRequestLogLevel 校验访问日志级别策略：
// 静态资源成功命中降级为 Debug（info 级别下不落盘），业务请求仍为 Info，静态 404 仍为 Warn。
// 一次后台页面加载会产生数十个资源请求，若全按 Info 记录会淹没业务日志。
func TestStaticRequestLogLevel(t *testing.T) {
	dist := newTestDist(t)
	app := newBareTestApp(t)
	app.Use(LogMiddleware())
	app.Use(NewStaticServer(dist, "").Middleware())
	app.Get("/api/logprobe", func(c fiber.Ctx) error { return c.SendString("pong") })

	before := readTestLogs(t)

	for _, p := range []string{"/assets/index-abc123.js", "/api/logprobe", "/assets/missing-logprobe.js"} {
		doRequest(t, app, p, "*/*")
	}
	added := strings.TrimPrefix(readTestLogs(t), before)

	if strings.Contains(added, "/assets/index-abc123.js") {
		t.Error("静态资源成功命中仍以 info 级别落盘，降级未生效")
	}
	if !strings.Contains(added, "/api/logprobe") {
		t.Error("业务请求的 info 级访问日志缺失")
	}
	if !strings.Contains(added, "/assets/missing-logprobe.js") {
		t.Error("静态资源 404 未被记录，产物缺失类故障将无从排查")
	}
	if !strings.Contains(added, `"level":"warn"`) {
		t.Error("静态资源 404 未按 warn 级别记录")
	}
}

// TestStaticServerMissingDist 校验配置了不存在的产物目录时不会 panic，
// 且所有请求都放行给后续路由（部署时漏挂载产物是常见故障）。
func TestStaticServerMissingDist(t *testing.T) {
	missing := filepath.Join(os.TempDir(), "httpx_dist_missing_"+t.Name())
	_ = os.RemoveAll(missing)

	s := NewStaticServer(missing, "")
	if !s.Enabled() {
		t.Fatal("配置了 DistDir 时 Enabled() 应为 true")
	}
	if full, ok := s.resolveFile("/assets/app.js"); ok {
		t.Errorf("产物目录不存在时 resolveFile 仍命中 %q", full)
	}

	app := newBareTestApp(t)
	app.Use(s.Middleware())
	app.Get("/dashboard", func(c fiber.Ctx) error { return c.SendString("fallthrough") })

	resp, body := doRequest(t, app, "/dashboard", "text/html")
	if resp.StatusCode != http.StatusOK || body != "fallthrough" {
		t.Errorf("产物缺失时 GET /dashboard = %d %q，期望放行到业务路由", resp.StatusCode, body)
	}

	// 带资源扩展名的请求仍返回真实 404，而不是业务信封 200
	resp, _ = doRequest(t, app, "/assets/app.js", "*/*")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("产物缺失时 GET /assets/app.js 状态码 = %d，期望 404", resp.StatusCode)
	}
}

// TestStaticServerDisabled 校验未配置产物目录时中间件直接放行。
func TestStaticServerDisabled(t *testing.T) {
	s := NewStaticServer("", "")
	if s.Enabled() {
		t.Fatal("DistDir 为空时 Enabled() 应为 false")
	}
	app := newBareTestApp(t)
	app.Use(s.Middleware())
	app.Get("/", func(c fiber.Ctx) error { return c.SendString("api only") })

	resp, body := doRequest(t, app, "/", "text/html")
	if resp.StatusCode != http.StatusOK || body != "api only" {
		t.Errorf("未启用静态托管时 GET / = %d %q，期望放行到业务路由", resp.StatusCode, body)
	}
}

// TestIsWithin 校验根目录归属判定按分隔符对齐，避免 /data/webx 命中 /data/web。
func TestIsWithin(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"data", "web")
	cases := map[string]bool{
		root:                                  true,
		filepath.Join(root, "index.html"):     true,
		filepath.Join(root, "assets", "a.js"): true,
		root + "x":                            false,
		filepath.Join(string(filepath.Separator)+"data", "webx", "a.js"): false,
		filepath.Join(string(filepath.Separator)+"etc", "passwd"):        false,
	}
	for target, want := range cases {
		if got := isWithin(target, root); got != want {
			t.Errorf("isWithin(%q, %q) = %v，期望 %v", target, root, got, want)
		}
	}
	if runtime.GOOS == "windows" {
		// 占位：Windows 下分隔符为 \，上面的用例已通过 filepath.Join 适配
		t.Log("Windows 分隔符已由 filepath.Join 适配")
	}
}
