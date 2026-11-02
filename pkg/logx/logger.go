package logx

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// defaultLogger / defaultSugar 用原子指针保存：任何包在 Init 之前调用日志都会触发兜底初始化，
	// 与业务显式 Init 可能发生在不同 goroutine，原子读写避免竞态与可见性问题。
	defaultLogger atomic.Pointer[zap.Logger]
	defaultSugar  atomic.Pointer[zap.SugaredLogger]
	// initOnce 保证显式 Init 只生效一次；fallbackOnce 专用于兜底初始化。
	// 两者必须分离：若兜底复用同一个 once，任何在 Init 之前发生的日志调用
	// 都会让后续 Init(cfg) 变成空操作，业务配置（级别/目录/轮转）被静默丢弃。
	initOnce     sync.Once
	fallbackOnce sync.Once
)

// Config 日志配置。
type Config struct {
	// Level 最低日志级别。默认 info
	Level string
	// Dir 日志文件存放目录。默认 ./logs
	Dir string
	// Filename 日志文件名前缀。默认 app
	Filename string
	// MaxSize 单个日志文件的大小上限（MB），写满后按大小轮转。默认 200
	MaxSize int
	// MaxBackups 历史日志文件的份数上限。默认 0，表示不按份数限制，仅由 MaxAge 决定保留
	MaxBackups int
	// MaxAge 历史日志文件的保留天数，超期删除。默认 7
	MaxAge int
	// Compress 是否压缩轮转出的历史日志。默认 true
	//
	// 注意这是 lumberjack 的原生语义：在轮转（写满 MaxSize）时就地压缩为 .gz，
	// 并非「按天延迟压缩」，因此当天较早轮转出的文件也会立即被压缩。
	Compress bool
	// Console 是否同时输出到标准输出。默认 true
	Console bool
}

// logFilePerm 是日志文件的目标权限：属主读写，同组与其他用户只读。
// lumberjack 库创建文件时固定使用 0600，导致非 root 用户（如宿主机 ubuntu）无法查看日志，
// 因此在写入后主动 chmod 放宽为 0644。
const logFilePerm os.FileMode = 0644

// permVerifyEvery 表示每累计写入多少条日志复检一次当前文件权限。
// lumberjack 轮转新文件时会复制「当前文件」的权限，因此当前文件一旦放宽为 0644，
// 后续轮转出的文件会自动继承，正常情况下首次校正后即长期有效；
// 保留周期性复检是为了自愈「当前文件被外部脚本删除后由 lumberjack 以 0600 重建」的场景，
// 否则非 root 运维用户会长期读不到日志。
const permVerifyEvery = 1024

// dailyCleanupInterval 低流量服务的兜底清理周期。
// lumberjack 仅在「启动后首次写入」与「写满 MaxSize 轮转」两个时点执行 mill（删除超期备份 + 压缩），
// 一天写不满 MaxSize 的服务可能数天不触发清理，超期日志一直留在磁盘上。
// permFixWriter 在距上次兜底轮转超过该时长后的首次写入主动调用 Rotate，
// 使 mill 至少每天执行一次，从而保证「超过 MaxAge 的日志必定被删除」。
// 声明为变量以便测试缩短周期。
var dailyCleanupInterval = 24 * time.Hour

// DefaultConfig 返回一份带合理默认值的配置。
func DefaultConfig() *Config {
	return &Config{
		Level:      "info",
		Dir:        "./logs",
		Filename:   "app",
		MaxSize:    200,
		MaxBackups: 0,
		MaxAge:     7,
		Compress:   true,
		Console:    true,
	}
}

// Init 以给定配置初始化全局 logger，应在应用启动时调用一次。
// 即使此前已发生兜底初始化，这里仍会以业务配置覆盖，保证配置不被丢弃。
func Init(cfg *Config) {
	initOnce.Do(func() {
		logger := newLogger(cfg)
		defaultLogger.Store(logger)
		defaultSugar.Store(logger.Sugar())
	})
}

// initFallback 在未显式 Init 就调用日志时以默认配置兜底。
// 该路径刻意不消耗 initOnce：后续业务 Init(cfg) 仍可用真实配置覆盖。
func initFallback() {
	fallbackOnce.Do(func() {
		logger := newLogger(DefaultConfig())
		// 仅在仍为空时写入：与并发执行的显式 Init 竞争时，不覆盖业务配置
		if defaultLogger.CompareAndSwap(nil, logger) {
			defaultSugar.CompareAndSwap(nil, logger.Sugar())
		}
	})
}

// Logger 返回全局 zap.Logger 实例。
func Logger() *zap.Logger {
	if logger := defaultLogger.Load(); logger != nil {
		return logger
	}
	initFallback()
	return defaultLogger.Load()
}

// Sugar 返回全局 zap.SugaredLogger 实例。
func Sugar() *zap.SugaredLogger {
	if sugar := defaultSugar.Load(); sugar != nil {
		return sugar
	}
	initFallback()
	return defaultSugar.Load()
}

// Sync 刷新并关闭日志文件句柄，应在进程退出前调用。
func Sync() {
	if logger := defaultLogger.Load(); logger != nil {
		_ = logger.Sync()
	}
}

func newLogger(cfg *Config) *zap.Logger {
	level := parseLevel(cfg.Level)

	// 确保日志目录存在
	_ = os.MkdirAll(cfg.Dir, 0755)

	// 固定当前日志文件名，轮转、保留、压缩交给 lumberjack 原生实现：
	// 当前文件恒为 <filename>.log，写满 MaxSize(MB) 后被重命名为
	// <filename>-<2006-01-02T15-04-05.000>.log，再由 MaxAge 依据文件名中的时间戳清理、
	// 由 Compress 压缩为 .gz。permFixWriter 另提供每日兜底轮转，保证低流量服务也能清理超期日志。
	// 备份名中的时间戳统一使用 UTC（LocalTime=false）：lumberjack 解析备份名时固定按 UTC，
	// 只有生成侧也用 UTC，MaxAge 的「now - maxAge*24h」口径才与文件名完全一致，
	// 否则保留时长会随服务器本地时区偏移（UTC+8 多留 8 小时、UTC-8 少留 8 小时）。
	//
	// 这里刻意不做「按天切换文件名」：lumberjack 只识别「当前文件前缀 + 时间戳」的备份名，
	// 而按天命名会让前缀逐日变化（<filename>-2026-09-14.log → <filename>-2026-09-15-），
	// 前一天的文件永远匹配不上当天前缀，MaxAge/MaxBackups 对它们完全失效。
	logFile := filepath.Join(cfg.Dir, cfg.Filename+".log")
	lj := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    cfg.MaxSize,    // MB
		MaxBackups: cfg.MaxBackups, // 份数上限；0 表示不按份数限制
		MaxAge:     cfg.MaxAge,     // 天；0 表示不按天数清理
		Compress:   cfg.Compress,
		LocalTime:  false, // 备份名时间戳使用 UTC，与 lumberjack 的解析口径保持一致
	}

	// 编码器配置：JSON 字段名统一小写，时间用 ISO8601 且强制 UTC
	encoderCfg := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     utcISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// 文件输出：JSON 格式
	fileCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(newPermFixWriter(lj, logFile)),
		level,
	)

	cores := []zapcore.Core{fileCore}

	// 控制台输出：人类可读格式
	if cfg.Console {
		consoleEncoderCfg := encoderCfg
		consoleEncoderCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		consoleCore := zapcore.NewCore(
			zapcore.NewConsoleEncoder(consoleEncoderCfg),
			zapcore.AddSync(os.Stdout),
			level,
		)
		cores = append(cores, consoleCore)
	}

	core := zapcore.NewTee(cores...)

	return zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
}

// utcTimeLayout 是日志时间字段的格式化模板，与 zapcore.ISO8601TimeEncoder 保持一致。
const utcTimeLayout = "2006-01-02T15:04:05.000Z0700"

// utcISO8601TimeEncoder 以 UTC 输出 ISO8601 日志时间。
// zap 自带的 ISO8601TimeEncoder 直接按 entry.Time 的时区格式化，而 entry.Time 来自 time.Now()，
// 即服务器本地时区：同一时刻在 UTC+8 与 UTC 的机器上会输出不同的偏移量，跨地区日志无法直接比对。
// 这里统一转换为 UTC（偏移为 0，按模板输出 Z），保证容器、宿主机与不同地区服务器下日志时间字符串完全一致。
//
// 参数：
//   - t: 日志条目的时间（可能携带任意时区）
//   - enc: zap 的基础类型编码器
func utcISO8601TimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.UTC().Format(utcTimeLayout))
}

func parseLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "fatal":
		return zapcore.FatalLevel
	default:
		return zapcore.InfoLevel
	}
}

// permFixWriter 包装 lumberjack，只负责两件 lumberjack 自身做不到的事：
// 把当前日志文件权限放宽到 logFilePerm，以及为低流量服务提供每日兜底轮转。
// 常规的文件轮转、超期保留（依备份名时间戳）、压缩仍走 lumberjack 原生逻辑。
//
// zap 会并发调用 Write，故计数用原子操作；lumberjack 自身持锁，底层写入无需额外保护。
type permFixWriter struct {
	lj     *lumberjack.Logger
	path   string       // 当前日志文件路径，lumberjack 原生命名，固定为 <filename>.log
	writes atomic.Int64 // 已写入条数，用于周期性复检权限
	// lastCleanupMs 上次触发兜底轮转的 Unix 毫秒时间戳；仅当 MaxAge > 0（按天保留）时启用兜底。
	// 用毫秒而非秒，与项目「时间戳统一毫秒」的约定保持一致。
	lastCleanupMs atomic.Int64
}

func newPermFixWriter(lj *lumberjack.Logger, path string) *permFixWriter {
	w := &permFixWriter{lj: lj, path: path}
	// 兜底计时从构造时刻起算：首个周期内不轮转，避免启动即切走一份空备份。
	w.lastCleanupMs.Store(time.Now().UnixMilli())
	return w
}

func (w *permFixWriter) Write(p []byte) (n int, err error) {
	n, err = w.lj.Write(p)
	if err != nil {
		return n, err
	}

	// lumberjack 创建文件固定 0600，导致非 root 用户无法查看日志；
	// 此处写入后（此时文件已创建）校正为 logFilePerm。
	// 轮转会复制当前文件权限，故校正一次即长期有效，周期性复检仅用于自愈。
	if count := w.writes.Add(1); count == 1 || count%permVerifyEvery == 0 {
		_ = os.Chmod(w.path, logFilePerm)
	}

	// 低流量兜底：MaxAge > 0 时才存在「长期不轮转就永不清理」的问题。
	// 距上次兜底轮转超过 dailyCleanupInterval 后主动 Rotate 一次，令 lumberjack 执行 mill；
	// CAS 保证并发写入下每个周期只触发一次。Rotate 内部持 lj.mu，与本次 Write 已释放的锁不冲突。
	if w.lj.MaxAge > 0 {
		nowMs := time.Now().UnixMilli()
		if last := w.lastCleanupMs.Load(); nowMs-last >= dailyCleanupInterval.Milliseconds() &&
			w.lastCleanupMs.CompareAndSwap(last, nowMs) {
			_ = w.lj.Rotate()
		}
	}

	return n, nil
}

// Sync 关闭底层文件句柄，应在进程退出前调用：lumberjack 直写 os.File、无用户态缓冲，
// 关闭即完成落盘，同时释放句柄，便于停机后清理或归档日志。
//
// 注意：运行期删除当前日志文件不会立即重建——lumberjack 会继续写入已 unlink 的 inode，
// 直到下一次轮转才按原文件名新建，此期间的日志将不可见。故请勿在服务运行中删除 <filename>.log。
func (w *permFixWriter) Sync() error {
	return w.lj.Close()
}
