package logx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// 本文件在 writer 层直接构造 lumberjack + permFixWriter 做断言，
// 刻意不调用 Init/Logger/Sugar：包级 initOnce/fallbackOnce 的消费顺序
// 由 logger_test.go 的 TestInitOverridesFallback 依赖，这里必须保持无副作用。
// 正因为不触碰任何全局状态，本文件与 logger_test.go 的执行先后无关。

// newTestLogger 构造一个固定文件名的日志写入器，参数便于把 MB 阈值缩小到 KB 级别。
func newTestLogger(t *testing.T, dir, name string, maxSizeMB, maxAge int, compress bool) (*permFixWriter, *lumberjack.Logger) {
	t.Helper()
	lj := &lumberjack.Logger{
		Filename:   filepath.Join(dir, name+".log"),
		MaxSize:    maxSizeMB,
		MaxAge:     maxAge,
		MaxBackups: 0,
		Compress:   compress,
		LocalTime:  false,
	}
	return newPermFixWriter(lj, lj.Filename), lj
}

// plantBackup 在日志目录下构造一份「ageDays 天前」的 lumberjack 命名备份并返回文件名。
// 文件名使用 lumberjack 原生格式（前缀 + ISO 时间戳），使其能被 mill 的保留策略识别。
// 时间戳统一按 UTC 生成，与生产配置（LocalTime=false）及 lumberjack 的 UTC 解析口径一致。
func plantBackup(t *testing.T, dir, prefix string, ageDays int) string {
	t.Helper()
	ts := time.Now().UTC().AddDate(0, 0, -ageDays)
	name := prefix + "-" + ts.Format("2006-01-02T15-04-05.000") + ".log"
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("old\n"), 0644); err != nil {
		t.Fatalf("构造历史日志失败: %v", err)
	}
	// 同步 mtime 与文件名时间戳，避免两者不一致时干扰断言
	_ = os.Chtimes(p, ts, ts)
	return name
}

// writeChunks 写入 n 次 chunkSize 字节，用于撑爆 MaxSize 触发轮转。
func writeChunks(t *testing.T, w *permFixWriter, n, chunkSize int) {
	t.Helper()
	chunk := make([]byte, chunkSize)
	for i := 0; i < n; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("写入日志失败: %v", err)
		}
	}
}

// waitFor 轮询等待条件成立：lumberjack 的清理/压缩在独立 goroutine（mill）中异步执行。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", desc)
}

// countMatch 统计目录中满足条件的文件名。
func countMatch(t *testing.T, dir string, pred func(string) bool) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取日志目录失败: %v", err)
	}
	var out []string
	for _, e := range entries {
		if pred(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestSizeRotationKeepsFixedCurrentFile 校验方案B的核心前提：
// 当前文件恒为 <name>.log，写满 MaxSize 后按大小轮转出带时间戳的备份，
// 且备份名使用 lumberjack 原生命名（前缀 + ISO 时间戳），使后续保留/压缩能匹配到。
func TestSizeRotationKeepsFixedCurrentFile(t *testing.T) {
	dir := tempDirBestEffort(t, "logx_size_")
	w, lj := newTestLogger(t, dir, "GameLogic", 1, 0, false)

	writeChunks(t, w, 9, 256*1024) // 约 2.25MB > MaxSize(1MB)，应轮转 2 次
	_ = lj.Close()

	backups := countMatch(t, dir, func(name string) bool {
		return name != "GameLogic.log" && strings.HasPrefix(name, "GameLogic-") && strings.HasSuffix(name, ".log")
	})
	if len(backups) < 2 {
		t.Fatalf("按大小轮转未产生预期备份数，期望 >=2，实际 %d: %v", len(backups), backups)
	}
	for _, name := range backups {
		ts := strings.TrimSuffix(strings.TrimPrefix(name, "GameLogic-"), ".log")
		if _, err := time.Parse("2006-01-02T15-04-05.000", ts); err != nil {
			t.Errorf("备份文件未使用 lumberjack 原生命名（时间戳无法解析）：%s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "GameLogic.log")); err != nil {
		t.Errorf("轮转后当前文件 GameLogic.log 不存在: %v", err)
	}
}

// TestExpiredBackupRemoved 校验超过 MaxAge 的备份会被删除，且未超期的备份保留。
func TestExpiredBackupRemoved(t *testing.T) {
	dir := tempDirBestEffort(t, "logx_age_")
	w, lj := newTestLogger(t, dir, "GameLogic", 1, 7, false)

	// 先写一笔让 lumberjack 打开当前文件
	if _, err := w.Write([]byte("today\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	expired := plantBackup(t, dir, "GameLogic", 8) // 超出 7 天，应被删
	fresh := plantBackup(t, dir, "GameLogic", 3)   // 未超期，应保留

	// 触发一次轮转，令 lumberjack 执行 mill（清理）
	if err := lj.Rotate(); err != nil {
		t.Fatalf("触发轮转失败: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(dir, expired))
		return os.IsNotExist(err)
	}, "超过 MaxAge 的备份应被删除")

	_ = lj.Close()

	if _, err := os.Stat(filepath.Join(dir, fresh)); err != nil {
		t.Errorf("未超期的备份不应被删除: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "GameLogic.log")); err != nil {
		t.Errorf("当前日志文件不应被删除: %v", err)
	}
}

// TestDailyFallbackRotationCleansExpired 校验低流量兜底轮转：
// 一直写不满 MaxSize、从不因大小轮转的服务，在距上次兜底轮转超过 dailyCleanupInterval 后，
// 首次写入应主动 Rotate 触发 mill，把超过 MaxAge 的备份删掉。
// 这是「超过 MaxAge 自动删除」对低流量服务真正生效的保证。
func TestDailyFallbackRotationCleansExpired(t *testing.T) {
	// 缩短兜底周期以便断言，用后恢复，避免影响同包其他用例
	orig := dailyCleanupInterval
	dailyCleanupInterval = 200 * time.Millisecond
	t.Cleanup(func() { dailyCleanupInterval = orig })

	dir := tempDirBestEffort(t, "logx_daily_")
	w, lj := newTestLogger(t, dir, "GameLogic", 200, 7, false)
	defer func() { _ = lj.Close() }()

	// 首次写入会执行启动 mill，但此刻尚无超期文件
	if _, err := w.Write([]byte("first\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	// 首次写入之后才植入超期备份，确保后续清理只能来自兜底轮转
	expired := plantBackup(t, dir, "GameLogic", 8)

	// 未到兜底周期：写入不应触发轮转，超期文件仍在
	time.Sleep(30 * time.Millisecond)
	if _, err := w.Write([]byte("second\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, expired)); err != nil {
		t.Fatalf("兜底周期未到时不应清理超期备份: %v", err)
	}

	// 超过兜底周期后写入，应主动 Rotate 触发清理
	time.Sleep(400 * time.Millisecond)
	if _, err := w.Write([]byte("third\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(dir, expired))
		return os.IsNotExist(err)
	}, "超过 dailyCleanupInterval 后的写入应触发兜底轮转并清理超期备份")
}

// TestCompressRotatedBackup 校验 Compress=true 时轮转出的备份被压缩为 .gz。
// 注意这是 lumberjack 原生「轮转即压缩」，不是「延迟 N 天再压缩」。
func TestCompressRotatedBackup(t *testing.T) {
	dir := tempDirBestEffort(t, "logx_gz_")
	w, lj := newTestLogger(t, dir, "GameLogic", 1, 0, true)

	writeChunks(t, w, 9, 256*1024)
	_ = lj.Close()

	gzCount := func() int {
		return len(countMatch(t, dir, func(name string) bool {
			return strings.HasPrefix(name, "GameLogic-") && strings.HasSuffix(name, ".gz")
		}))
	}
	plainCount := func() int {
		return len(countMatch(t, dir, func(name string) bool {
			return strings.HasPrefix(name, "GameLogic-") && strings.HasSuffix(name, ".log")
		}))
	}

	// mill 在独立 goroutine 中逐个压缩，须等到「已产生 .gz 且不再残留未压缩备份」才算完成。
	waitFor(t, 5*time.Second, func() bool {
		return gzCount() > 0 && plainCount() == 0
	}, "轮转出的备份应全部被压缩为 .gz，且不残留未压缩源文件")

	if gzCount() == 0 {
		t.Fatal("未找到任何 .gz 备份")
	}
}

// TestCurrentFilePermRelaxed 校验写入后当前文件权限被放宽为 logFilePerm，
// 保证宿主机非 root 运维用户（如 ubuntu）能读到日志。
func TestCurrentFilePermRelaxed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位，跳过权限断言")
	}
	dir := tempDirBestEffort(t, "logx_perm_")
	w, lj := newTestLogger(t, dir, "GameLogic", 200, 7, true)

	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	_ = lj.Close()

	info, err := os.Stat(filepath.Join(dir, "GameLogic.log"))
	if err != nil {
		t.Fatalf("获取日志文件信息失败: %v", err)
	}
	if info.Mode().Perm() != logFilePerm {
		t.Errorf("当前日志文件权限为 %o，期望 %o", info.Mode().Perm(), logFilePerm)
	}
}

// TestConcurrentWriteIsRaceFree 并发写入下校验无数据竞争（配合 go test -race 生效），
// 并确认所有日志都被写入、当前文件未因并发而损坏。
// 权限复检计数曾用非原子的 w.writes++，zap 并发调用 Write 时会构成数据竞争。
func TestConcurrentWriteIsRaceFree(t *testing.T) {
	dir := tempDirBestEffort(t, "logx_race_")
	w, lj := newTestLogger(t, dir, "GameLogic", 200, 0, false)

	const goroutines, perGoroutine = 8, 200
	line := []byte("{\"msg\":\"concurrent\"}\n")

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				if _, err := w.Write(line); err != nil {
					t.Errorf("并发写入失败: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	_ = lj.Close()

	data, err := os.ReadFile(filepath.Join(dir, "GameLogic.log"))
	if err != nil {
		t.Fatalf("读取当前日志文件失败: %v", err)
	}
	want := goroutines * perGoroutine * len(line)
	if len(data) != want {
		t.Errorf("日志文件字节数 = %d，期望 %d（并发写入丢失或交错损坏）", len(data), want)
	}
}
