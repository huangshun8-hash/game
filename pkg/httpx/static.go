package httpx

import (
	"errors"
	"net/url"
	"os"
	gopath "path"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/proxy"
	"go.uber.org/zap"

	"kemiluo/pkg/logx"
)

// apiPathPrefix 需要绕过静态托管、交给后端 API 路由（或反向代理）处理的路径前缀。
// GMT 的后台接口统一挂在 /api 下，静态中间件对其放行，避免被 SPA 回退吞掉。
const apiPathPrefix = "/api"

// indexFileName SPA 入口文件名，history 回退与根路径映射均指向该文件。
const indexFileName = "index.html"

// wellKnownSegment 标准的 /.well-known 路径段（ACME 校验、applinks 等），
// 是点文件拦截规则唯一的白名单例外。
const wellKnownSegment = ".well-known"

// assetMaxAgeSeconds 带内容 hash 的前端产物（Vite/Webpack 输出的 assets）缓存时长，
// 单位秒。文件名随内容变化，可安全长缓存一年。
const assetMaxAgeSeconds = 31536000

// assetCacheControl 静态资源缓存头：内容 hash 保证文件不可变，浏览器可直接复用不回源。
// max-age 数值须与 assetMaxAgeSeconds 保持一致。
const assetCacheControl = "public, max-age=31536000, immutable"

// revalidateCacheControl 非 hash 命名文件（HTML 入口页、/.well-known 校验文件）的缓存头：
// 必须每次回源校验，否则发版后浏览器仍用旧 HTML，引用到已被删除的旧 hash 资源。
const revalidateCacheControl = "no-cache, no-store, must-revalidate"

// noStoreCacheControl 用于 404 与传输层错误响应：不允许缓存，
// 否则发版补齐资源后浏览器仍复用失败结果。
const noStoreCacheControl = "no-store"

// assetContentTypes 前端静态资源扩展名 → Content-Type 的权威映射。
//
// 为什么不依赖 fasthttp 的 mime.TypeByExtension：该函数会读取宿主机的 MIME 数据库
// （Windows 读注册表、Linux 读 /etc/mime.types 与内建表），结果随机器而变——
// Go 内建表无 .mjs/.cjs，Windows 上会回落到 text/plain，浏览器对 ES module
// 做严格 MIME 校验时直接拒绝执行（控制台报 MIME type 错误、页面白屏）。
// 这里显式声明前端产物的类型，保证跨平台行为一致且可预测。
// 键必须全小写，与 hasAssetExtension 的折叠逻辑一致。
var assetContentTypes = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".cjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".map":   "application/json; charset=utf-8",
	".wasm":  "application/wasm",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".eot":   "application/vnd.ms-fontobject",
	".mp4":   "video/mp4",
	".webm":  "video/webm",
	".txt":   "text/plain; charset=utf-8",
}

// contentTypeForAsset 返回静态资源应下发的 Content-Type。
// 命中显式映射时以其为准（覆盖 fasthttp 依宿主机 MIME 库推断的结果）；
// 未命中返回空串，表示沿用 SendFile 已写入的值。
// path 为 URL 路径，故使用 path 包取扩展名，与 hasAssetExtension 保持一致。
func contentTypeForAsset(path string) string {
	return assetContentTypes[strings.ToLower(gopath.Ext(path))]
}

// staticAssetLocalsKey 标记当前请求由静态托管中间件直接响应，供日志中间件调整记录策略：
// 跳过请求/响应体记录，并把成功命中的访问日志降级为 Debug。
const staticAssetLocalsKey = "staticAsset"

// assetSendFileConfig / revalidateSendFileConfig 为两份固定的 SendFile 配置。
// Fiber 会按配置内容缓存 fasthttp FS handler（app.sendfiles），固定两份可保证缓存条目
// 不随请求增长。ByteRange 开启后才会下发 Accept-Ranges 并支持 206，
// 否则视频、大体积产物的拖动会退化为整包下载。
var (
	assetSendFileConfig      = fiber.SendFile{MaxAge: assetMaxAgeSeconds, ByteRange: true}
	revalidateSendFileConfig = fiber.SendFile{ByteRange: true}
)

// isStaticAssetRequest 返回当前请求是否由静态托管中间件直接响应。
// c: 当前请求上下文。返回 true 表示日志中间件应跳过 reqBody/respBody，
// 且成功响应按 Debug 级别记录。
func isStaticAssetRequest(c fiber.Ctx) bool {
	isStatic, _ := c.Locals(staticAssetLocalsKey).(bool)
	return isStatic
}

// StaticServer 封装 Vue 等 SPA 打包产物的静态托管与可选的后台 API 反向代理。
// DistDir 为空表示不启用静态托管。
type StaticServer struct {
	DistDir string
	// ProxyTarget 可选：后台 API 反向代理目标地址（如 http://localhost:8082）。
	// 为空时 /api 请求交由同端口 GMT 路由处理（同源集成）；非空时将 /api 请求反向代理到该目标。
	ProxyTarget string

	// rootAbs 为 DistDir 的绝对路径，创建时预计算：
	// 用于路径穿越防护的根目录基准，避免每个请求都调用 filepath.Abs（相对路径时会触发 os.Getwd 系统调用）。
	rootAbs string
	// rootReal 为 rootAbs 解析符号链接后的真实路径，用于「产物目录内的符号链接不得指向根目录之外」检查。
	// 创建时目录不存在（解析失败）则为空，此时退化为仅做 rootAbs 前缀检查。
	rootReal string
	// indexPath 为入口文件 index.html 的绝对路径，创建时预计算，用于启动期诊断日志。
	// 请求期的入口文件解析统一走 resolveFile，以复用同一套校验。
	indexPath string
}

// NewStaticServer 创建静态托管服务器。ProxyTarget 为空时不启用反向代理。
// distDir: 前端打包产物目录（相对路径会在此处一次性解析为绝对路径）；
// proxyTarget: 后台 API 反向代理目标，为空表示 /api 由同端口路由处理。
func NewStaticServer(distDir, proxyTarget string) *StaticServer {
	s := &StaticServer{
		DistDir:     distDir,
		ProxyTarget: strings.TrimSpace(proxyTarget),
	}
	if distDir == "" {
		return s
	}
	// 预计算绝对根路径：失败时退化为原始配置值，穿越防护仍基于同一基准比较，行为保持一致。
	root, err := filepath.Abs(distDir)
	if err != nil {
		logx.Warn("解析静态目录绝对路径失败，退化使用原始配置值",
			zap.String("distDir", distDir),
			zap.Error(err))
		root = filepath.Clean(distDir)
	}
	s.rootAbs = root
	s.indexPath = filepath.Join(root, indexFileName)
	// 根目录自身可能是软链接（部署侧用软链接切换版本），先取真实路径作为符号链接检查的基准，
	// 否则合法请求会因「真实路径不在配置路径之下」被全部误判为穿越。
	if realRoot, realErr := filepath.EvalSymlinks(root); realErr == nil {
		s.rootReal = realRoot
	}

	// 启动期输出一次静态托管诊断信息：产物目录未挂载或缺 index.html 是部署阶段最常见的故障，
	// 在这里暴露比逐请求打日志更有价值（每请求的 404 由 LogMiddleware 统一记录）。
	hasIndex := false
	if info, statErr := os.Stat(s.indexPath); statErr == nil && !info.IsDir() {
		hasIndex = true
	}
	logx.Info("启用前端静态托管",
		zap.String("distDir", distDir),
		zap.String("rootAbs", root),
		zap.String("rootReal", s.rootReal),
		zap.Bool("hasIndexHtml", hasIndex),
		zap.String("proxyTarget", s.ProxyTarget))
	return s
}

// Enabled 返回是否启用了静态托管。
func (s *StaticServer) Enabled() bool {
	return s != nil && s.DistDir != ""
}

// Middleware 返回 Fiber 中间件，用于托管静态文件并支持 SPA history 回退。
// 该中间件必须在 JWT 等业务中间件之前注册：命中静态文件/回退时直接响应，
// 不进入后续中间件；/api 路径与未知路径则调用 Next() 交给后续路由处理。
func (s *StaticServer) Middleware() fiber.Handler {
	if !s.Enabled() {
		return func(c fiber.Ctx) error { return c.Next() }
	}
	return func(c fiber.Ctx) error {
		path := c.Path()

		// API 路径由后端路由处理（ProxyTarget 为空时同源）或反向代理到后台服务。
		if isAPIPath(path) {
			if s.ProxyTarget != "" {
				if err := proxy.Do(c, s.ProxyTarget); err != nil {
					logx.Warn("反向代理后台接口失败",
						zap.String("requestId", localRequestID(c)),
						zap.String("url", path),
						zap.Error(err))
					return err
				}
				return nil
			}
			return c.Next()
		}

		// 静态内容只对 GET/HEAD 提供：其余方法放行给后续路由，
		// 由路由层给出 405 等正确语义，避免 POST /assets/x.js 或 POST /任意路径
		// 被静默回以 200 + 文件内容（掩盖客户端调用错误）。
		if method := c.Method(); method != fiber.MethodGet && method != fiber.MethodHead {
			return c.Next()
		}

		// 直接托管存在的静态文件（带路径穿越防护）。
		if full, ok := s.resolveFile(path); ok {
			return s.sendAsset(c, full, path)
		}

		// 带资源扩展名的请求（.js/.css/.wasm 等）不做 SPA 回退：
		// 浏览器加载 ES module 时会带上 Accept: */*，若回退到 index.html
		// 会以 text/html 返回 JS 资源，触发严格 MIME 校验错误。
		if hasAssetExtension(path) {
			return s.sendNotFound(c)
		}

		// SPA history 回退：仅对浏览器导航类请求（能接受 HTML）回退到 index.html；
		// 其他（如资源请求 404）交由后续路由，最终返回 404，不吞掉未知资源请求。
		// 入口文件同样走 resolveFile，保证与直接命中共用同一套校验（含符号链接检查）。
		if acceptsHTML(c) {
			if full, ok := s.resolveFile("/" + indexFileName); ok {
				return s.sendAsset(c, full, "/"+indexFileName)
			}
		}
		return c.Next()
	}
}

// sendAsset 输出静态文件并附加合适的缓存头，同时标记为静态请求（供日志中间件降级处理）。
// full: 文件绝对路径；urlPath: 原始请求路径（用于判定是否为 HTML 文档）。
func (s *StaticServer) sendAsset(c fiber.Ctx, full, urlPath string) error {
	// 静态资源响应体（JS/CSS bundle 可达数 MB，图片字体为二进制）不写入日志。
	c.Locals(staticAssetLocalsKey, true)

	// 非 hash 命名的文件必须每次回源校验，带 hash 的资源可长缓存。
	mustRevalidate := isRevalidatePath(urlPath)
	cfg := assetSendFileConfig
	if mustRevalidate {
		cfg = revalidateSendFileConfig
	}
	if err := c.SendFile(full, cfg); err != nil {
		return s.wrapSendFileError(c, err, urlPath)
	}

	// 覆写 Content-Type：SendFile 内部按宿主机 MIME 库推断，.mjs/.cjs 等会得到
	// text/plain 导致浏览器严格 MIME 校验失败，故以本仓库的显式映射为准。
	// 仅对成功响应覆写；未命中映射（如 /.well-known 校验文件）沿用 SendFile 的结果。
	if ct := contentTypeForAsset(urlPath); ct != "" && isSuccessStatus(c.Response().StatusCode()) {
		c.Set(fiber.HeaderContentType, ct)
	}

	// Cache-Control 统一在 SendFile 之后按最终状态码设置：
	// SendFile 内部会按 MaxAge 写入该头，且 fasthttp FS 的内部错误（如读取失败写 500）
	// 不会以 error 形式返回，若无条件覆写，500 响应也会被打上一年 immutable 缓存，
	// 被浏览器与 CDN 长期复用。
	c.Set(fiber.HeaderCacheControl, cacheControlFor(mustRevalidate, c.Response().StatusCode()))
	return nil
}

// cacheControlFor 按「是否必须回源校验」与最终状态码决定 Cache-Control 取值。
// mustRevalidate: 该路径是否为非 hash 命名文件（HTML 入口页、/.well-known 等）；
// status: SendFile 执行后的真实响应状态码。
// 返回待下发的 Cache-Control 值：仅成功响应（200/206/304）下发缓存策略，其余一律 no-store。
func cacheControlFor(mustRevalidate bool, status int) string {
	switch status {
	case fiber.StatusOK, fiber.StatusNotModified, fiber.StatusPartialContent:
		if mustRevalidate {
			return revalidateCacheControl
		}
		return assetCacheControl
	default:
		return noStoreCacheControl
	}
}

// isSuccessStatus 判断状态码是否为可下发正文的成功响应（200/206/304）。
// 用于 Content-Type 覆写：传输层错误（如 500）不应被附加资源类型头。
func isSuccessStatus(status int) bool {
	switch status {
	case fiber.StatusOK, fiber.StatusNotModified, fiber.StatusPartialContent:
		return true
	default:
		return false
	}
}

// wrapSendFileError 处理 SendFile 失败：文件在 Stat 与发送之间被删除或替换（发版覆盖产物）
// 会得到 404，其余为读取失败等传输层错误。两者都必须直接输出真实状态码，
// 不能交给 DefaultErrorHandler（后者会统一转成 HTTP 200 + JSON 信封，
// 浏览器加载 module 时仍报严格 MIME 错误，且 DevTools 显示 200 掩盖问题）。
func (s *StaticServer) wrapSendFileError(c fiber.Ctx, err error, urlPath string) error {
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) && fiberErr.Code == fiber.StatusNotFound {
		return s.sendNotFound(c)
	}
	logx.Warn("发送静态文件失败",
		zap.String("requestId", localRequestID(c)),
		zap.String("url", urlPath),
		zap.Error(err))
	return s.sendPlainStatus(c, fiber.StatusInternalServerError, "Internal Server Error")
}

// sendNotFound 为静态资源请求输出真实的 HTTP 404 纯文本响应。
// 每请求的 404 由 LogMiddleware 以 Warn 级别统一记录（含 path/status/ip/requestId），
// 此处不重复打日志，避免扫描器批量探测不存在的资源时放大日志量。
func (s *StaticServer) sendNotFound(c fiber.Ctx) error {
	return s.sendPlainStatus(c, fiber.StatusNotFound, "Not Found")
}

// sendPlainStatus 以纯文本形式输出传输层状态码，绕开业务 code 信封。
// 静态资源属于传输层：若返回 fiber.Error 交给 DefaultErrorHandler，
// 会被统一转成 HTTP 200 + application/json，浏览器加载 ES module 时依旧触发严格 MIME 校验失败。
// c: 当前请求上下文；status: HTTP 状态码；message: 英文纯文本说明（对外输出禁止中文）。
func (s *StaticServer) sendPlainStatus(c fiber.Ctx, status int, message string) error {
	c.Locals(staticAssetLocalsKey, true)
	c.Set(fiber.HeaderCacheControl, noStoreCacheControl)
	c.Set(fiber.HeaderContentType, fiber.MIMETextPlainCharsetUTF8)
	return c.Status(status).SendString(message)
}

// resolveFile 将请求路径映射到 DistDir 下的实际文件，并做路径穿越防护。
// path: 请求 URL 路径（始终以 "/" 开头，可能带 percent 转义）。
// 返回 (文件绝对路径, 是否存在可读文件)。
func (s *StaticServer) resolveFile(path string) (string, bool) {
	if path == "" || path == "/" {
		path = "/" + indexFileName
	}
	// URL 解码：Fiber 的 UnescapePath 默认关闭，c.Path() 返回的是原始转义串，
	// 不解码则产物中含空格、中文等字符的文件（%20 / %E4%B8%AD）永远无法命中。
	// 解码后仍走同一套 Clean + 根目录前缀检查，因此 %2e%2e%2f 这类编码穿越同样被拦住。
	decoded, err := url.PathUnescape(path)
	if err != nil {
		// 非法转义序列（如单独的 %zz）不做部分解码，直接判为未命中
		return "", false
	}
	// NUL 字节会被底层文件 API 拒绝，提前拦掉避免依赖各平台的实现差异
	if strings.ContainsRune(decoded, 0) {
		return "", false
	}
	// 请求路径始终以 "/" 开头（URL 根），这是正常情况而非路径穿越。
	// 先用 path 包（正斜杠语义）清洗并去掉前导斜杠，再拼到 DistDir 下，
	// 避免在 Linux 上把以 "/" 开头的 URL 误判为绝对路径而全部拒绝
	// （filepath.IsAbs 在 Linux 上对任意 "/xxx" 均为 true，Windows 需盘符，
	// 这正是本地正常、Linux 远端所有静态资源都回退到 index.html 的根因）。
	clean := gopath.Clean("/" + strings.TrimPrefix(decoded, "/"))
	// 拒绝隐藏文件：产物目录被误拷进 .env、.git/config 时不应可下载。
	// .well-known 为标准例外（ACME 校验、applinks 等）。
	if hasHiddenSegment(clean) {
		return "", false
	}

	full := filepath.Join(s.rootAbs, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	// 穿越防护：full 必须等于 rootAbs 或位于其之下。
	// 该检查在 Windows 上是必需的：gopath.Clean 不把 "\" 当分隔符，
	// 形如 "/..\..\secret" 的路径能原样穿过 Clean，随后被 filepath.Join 当作分隔符逃出根目录。
	if !isWithin(full, s.rootAbs) {
		return "", false
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return "", false
	}
	// 符号链接逃逸防护：os.Stat 会跟随链接，仅靠字符串前缀检查无法发现
	// 产物目录内指向根目录之外的链接（如 dist/link -> /etc/passwd）。
	// rootReal 为空表示启动时根目录尚不可解析，此时退化为仅前缀检查。
	if s.rootReal != "" {
		realPath, realErr := filepath.EvalSymlinks(full)
		if realErr != nil || !isWithin(realPath, s.rootReal) {
			return "", false
		}
	}
	return full, true
}

// isWithin 判断 target 是否等于 root 或位于 root 之下（按路径分隔符对齐，避免 /data/webx 命中 /data/web）。
// target/root 均须为已 Clean 的绝对路径。
func isWithin(target, root string) bool {
	if target == root {
		return true
	}
	return strings.HasPrefix(target, root+string(filepath.Separator))
}

// hasHiddenSegment 判断清洗后的路径是否包含以 "." 开头的隐藏段（.env、.git 等）。
// cleanPath 须为 gopath.Clean 处理过、以 "/" 开头的路径；.well-known 为白名单例外。
func hasHiddenSegment(cleanPath string) bool {
	for _, segment := range strings.Split(strings.TrimPrefix(cleanPath, "/"), "/") {
		if !strings.HasPrefix(segment, ".") {
			continue
		}
		if segment == wellKnownSegment {
			continue
		}
		return true
	}
	return false
}

// isAPIPath 判断是否为需放行给后端路由/反向代理的 API 路径。
// 精确到路径段边界，避免 /apifoo 这类前缀相近的静态路径被误放行。
func isAPIPath(urlPath string) bool {
	return urlPath == apiPathPrefix || strings.HasPrefix(urlPath, apiPathPrefix+"/")
}

// staticAssetExtensions 常见前端静态资源扩展名集合，这类请求未命中时
// 应返回 404 而非 SPA 回退，避免以 text/html 响应导致严格 MIME 校验失败。
// 不含 .html/.htm：SPA 深链（如 /dashboard/players）需保留 index.html 回退能力。
var staticAssetExtensions = map[string]struct{}{
	".js":    {},
	".mjs":   {},
	".cjs":   {},
	".css":   {},
	".json":  {},
	".map":   {},
	".wasm":  {},
	".png":   {},
	".jpg":   {},
	".jpeg":  {},
	".gif":   {},
	".webp":  {},
	".avif":  {},
	".svg":   {},
	".ico":   {},
	".woff":  {},
	".woff2": {},
	".ttf":   {},
	".eot":   {},
	".otf":   {},
	".mp4":   {},
	".webm":  {},
	".txt":   {},
}

// hasAssetExtension 判断请求路径是否为已知的前端静态资源扩展名。
// path 为 URL 路径，故使用 path 包（正斜杠语义）取扩展名，与 resolveFile 的清洗逻辑保持一致。
func hasAssetExtension(path string) bool {
	ext := strings.ToLower(gopath.Ext(path))
	if ext == "" {
		return false
	}
	_, ok := staticAssetExtensions[ext]
	return ok
}

// isRevalidatePath 判断请求路径是否必须每次回源校验（即不可长缓存）。
// 覆盖两类非内容 hash 命名的文件：
//   - HTML 文档（根路径、SPA 回退、.html/.htm）：长缓存会让发版后的浏览器引用到已删除的旧 hash 资源；
//   - /.well-known 下的校验文件（ACME 证书续期、applinks）：内容会变而路径固定，长缓存会导致校验失败。
func isRevalidatePath(urlPath string) bool {
	if urlPath == "/" {
		return true
	}
	if strings.HasPrefix(urlPath, "/"+wellKnownSegment+"/") {
		return true
	}
	switch strings.ToLower(gopath.Ext(urlPath)) {
	case ".html", ".htm":
		return true
	}
	return false
}

// acceptsHTML 判断请求是否期望 HTML 内容（浏览器页面导航）。
func acceptsHTML(c fiber.Ctx) bool {
	accept := c.Get(fiber.HeaderAccept)
	if accept == "" {
		return true
	}
	return strings.Contains(accept, "text/html") || strings.Contains(accept, "*/*")
}
