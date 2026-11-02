package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zapcore"
)

// tempDirBestEffort 创建临时目录并尽力清理（忽略删除失败）。
// 不用 t.TempDir()：兜底 logger 的 lumberjack 句柄不会被关闭（zap 不感知 Close，
// 只有显式调用 logx.Sync() 才关闭 Init 实例的句柄），Windows 上句柄未释放前无法删除文件，
// t.TempDir() 的强制清理会让用例失败。
func tempDirBestEffort(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestInitOverridesFallback 校验「兜底初始化不吞掉业务配置」：
// 任何包在显式 Init 之前调用日志（例如构造函数里的启动诊断日志）都会触发默认配置兜底，
// 若兜底与 Init 共用同一个 sync.Once，后续 Init(cfg) 会退化为空操作，
// 业务配置的级别/目录/轮转被静默丢弃 —— 这里锁定修复后的行为。
//
// 该用例依赖包级单次初始化状态（initOnce/fallbackOnce），必须是本包内首个消费这两个 once 的用例。
// 同包的 logger_rotate_test.go 刻意只操作 writer 层、不触碰全局状态，故与本用例执行先后无关。
func TestInitOverridesFallback(t *testing.T) {
	// 切到临时目录：兜底会按默认配置在当前工作目录创建 ./logs，避免污染包目录
	t.Chdir(tempDirBestEffort(t, "logx_fallback_cwd_"))

	// 先触发兜底初始化，模拟 Init 之前就有日志调用
	fallbackLogger := Logger()
	if fallbackLogger == nil {
		t.Fatal("兜底初始化后 Logger() 仍为 nil")
	}
	if Sugar() == nil {
		t.Fatal("兜底初始化后 Sugar() 仍为 nil")
	}

	// 再显式 Init：必须以业务配置覆盖兜底实例
	cfg := DefaultConfig()
	cfg.Dir = filepath.Join(tempDirBestEffort(t, "logx_init_"), "logs")
	cfg.Level = "debug"
	cfg.Console = false
	Init(cfg)
	t.Cleanup(Sync)

	current := Logger()
	if current == fallbackLogger {
		t.Error("显式 Init 未覆盖兜底 logger，业务日志配置被静默丢弃")
	}
	if !current.Core().Enabled(zapcore.DebugLevel) {
		t.Error("Init 传入的 debug 级别未生效")
	}
	if Sugar() == nil {
		t.Error("Init 后 Sugar() 为 nil")
	}

	// Init 幂等：重复调用不再更换实例
	Init(DefaultConfig())
	if Logger() != current {
		t.Error("重复 Init 更换了 logger 实例，应保持幂等")
	}

	// 日志实际写入到 Init 指定的目录
	Info("测试日志写入")
	Sync()
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		t.Fatalf("读取日志目录失败: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("日志未写入 Init 指定目录 %s", cfg.Dir)
	}
}

// TestEncodeTimeIsUTC 锁定日志时间字段恒为 UTC：
// 即使传入带 +08:00 偏移的时间（模拟服务器本地时区为 UTC+8），
// 编码结果也必须落在 UTC（偏移为 0 时以 Z 结尾），保证跨地区日志时间字符串一致。
func TestEncodeTimeIsUTC(t *testing.T) {
	enc := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		TimeKey:    "time",
		EncodeTime: utcISO8601TimeEncoder,
	})
	local := time.Date(2026, 10, 29, 18, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	buf, err := enc.EncodeEntry(zapcore.Entry{Time: local}, nil)
	if err != nil {
		t.Fatalf("编码日志时间失败: %v", err)
	}
	defer buf.Free()

	const want = `"time":"2026-10-29T10:00:00.000Z"`
	if line := buf.String(); !strings.Contains(line, want) {
		t.Fatalf("日志时间未按 UTC 输出，期望包含 %s，实际: %s", want, line)
	}
}
