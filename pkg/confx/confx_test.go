package confx

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

// newTestSource 构造一个以内存键值为数据源的配置视图
func newTestSource(values map[string]any) *Source {
	v := viper.New()
	for key, val := range values {
		v.Set(key, val)
	}
	return &Source{v: v}
}

// TestSourceReturnsDefaultWhenUnset 校验未配置的键一律回落到调用方给出的默认值。
// 这是「以代码默认值为基线、按需覆盖」的前提：任何一项漏配都不应把配置清零。
func TestSourceReturnsDefaultWhenUnset(t *testing.T) {
	src := newTestSource(nil)
	if got := src.Int("ws.buf", 256); got != 256 {
		t.Fatalf("Int 未配置时应返回默认值 256，实际为 %d", got)
	}
	if got := src.Int64("ws.size", 1024); got != 1024 {
		t.Fatalf("Int64 未配置时应返回默认值 1024，实际为 %d", got)
	}
	if got := src.Bool("ws.compress", true); !got {
		t.Fatal("Bool 未配置时应返回默认值 true")
	}
	if got := src.String("ws.mode", "auto"); got != "auto" {
		t.Fatalf("String 未配置时应返回默认值 auto，实际为 %q", got)
	}
	if got := src.Duration("ws.wait", 30*time.Second); got != 30*time.Second {
		t.Fatalf("Duration 未配置时应返回默认值 30s，实际为 %s", got)
	}
	want := []string{"*"}
	if got := src.StringSlice("ws.origins", want); len(got) != 1 || got[0] != "*" {
		t.Fatalf("StringSlice 未配置时应返回默认值 %v，实际为 %v", want, got)
	}
}

// TestSourceNilReceiverIsSafe 校验 nil 接收者安全：不关心专属配置的服务可继续使用 LoadBase
func TestSourceNilReceiverIsSafe(t *testing.T) {
	var src *Source
	if got := src.Int("any.key", 7); got != 7 {
		t.Fatalf("nil 接收者应返回默认值 7，实际为 %d", got)
	}
	if got := src.StringSlice("any.key", []string{"a"}); len(got) != 1 || got[0] != "a" {
		t.Fatalf("nil 接收者应返回默认值 [a]，实际为 %v", got)
	}
}

// TestSourceReadsConfiguredValues 校验已配置的键按声明类型正确读出
func TestSourceReadsConfiguredValues(t *testing.T) {
	src := newTestSource(map[string]any{
		"ws.buf":      512,
		"ws.size":     65536,
		"ws.compress": false,
		"ws.mode":     "strict",
		"ws.wait":     "45s",
	})
	if got := src.Int("ws.buf", 256); got != 512 {
		t.Fatalf("Int 应读出 512，实际为 %d", got)
	}
	if got := src.Int64("ws.size", 1024); got != 65536 {
		t.Fatalf("Int64 应读出 65536，实际为 %d", got)
	}
	if got := src.Bool("ws.compress", true); got {
		t.Fatal("Bool 应读出 false")
	}
	if got := src.String("ws.mode", "auto"); got != "strict" {
		t.Fatalf("String 应读出 strict，实际为 %q", got)
	}
	if got := src.Duration("ws.wait", 30*time.Second); got != 45*time.Second {
		t.Fatalf("Duration 应读出 45s，实际为 %s", got)
	}
}

// TestSourceStringSliceAcceptsListAndCommaSeparated 校验字符串列表同时兼容
// 配置文件中的 YAML 列表与环境变量中的逗号分隔串（环境变量无法表达列表结构）。
func TestSourceStringSliceAcceptsListAndCommaSeparated(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  []string
	}{
		{"YAML 列表", []string{"https://a.com", "https://b.com"}, []string{"https://a.com", "https://b.com"}},
		{"逗号分隔串", "https://a.com,https://b.com", []string{"https://a.com", "https://b.com"}},
		{"逗号分隔串忽略多余空格与空元素", " https://a.com , ,https://b.com ", []string{"https://a.com", "https://b.com"}},
		{"单个值", "https://a.com", []string{"https://a.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := newTestSource(map[string]any{"ws.origins": c.value})
			got := src.StringSlice("ws.origins", []string{"*"})
			if len(got) != len(c.want) {
				t.Fatalf("应读出 %v，实际为 %v", c.want, got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("应读出 %v，实际为 %v", c.want, got)
				}
			}
		})
	}
}

// TestSourceStringSliceFallsBackWhenAllEmpty 校验配置值全为空元素时回落到默认值，
// 避免把 Origin 白名单这类安全配置误清空成「全部拒绝」。
func TestSourceStringSliceFallsBackWhenAllEmpty(t *testing.T) {
	src := newTestSource(map[string]any{"ws.origins": " , , "})
	got := src.StringSlice("ws.origins", []string{"*"})
	if len(got) != 1 || got[0] != "*" {
		t.Fatalf("全空配置应回落到默认值 [*]，实际为 %v", got)
	}
}

// TestLogDefaultsRetainSevenDays 锁定日志保留期默认值：保留 7 天、启用压缩、
// 不按份数限制。这三项共同决定「超过 7 天的历史日志自动删除」生效，
// 默认值被改回 30 天/不压缩会导致线上磁盘占用回升且清理策略偏离设计。
func TestLogDefaultsRetainSevenDays(t *testing.T) {
	v := viper.New()
	setDefaults(v, "GameLogic", 8081, "logic_db")

	if got := v.GetInt("log.max_age"); got != 7 {
		t.Errorf("log.max_age 默认应为 7 天，实际为 %d", got)
	}
	if got := v.GetInt("log.max_backups"); got != 0 {
		t.Errorf("log.max_backups 默认应为 0（不按份数限制），实际为 %d", got)
	}
	if got := v.GetBool("log.compress"); !got {
		t.Error("log.compress 默认应为 true")
	}
	if got := v.GetInt("log.max_size"); got != 200 {
		t.Errorf("log.max_size 默认应为 200MB，实际为 %d", got)
	}
}
