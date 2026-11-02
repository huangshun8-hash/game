// Package idx 提供分布式唯一ID生成能力。
// 当前实现基于 Twitter 雪花算法（Snowflake），生成 64 位、趋势递增的唯一ID。
package idx

import (
	"fmt"
	"hash/fnv"
	"os"
	"sync"
	"time"
)

// 雪花ID位分配：1 位符号位（恒为0）+ 41 位毫秒时间戳 + 10 位节点ID + 12 位序列号
const (
	// epochMillis 自定义纪元（2025-01-01 00:00:00 UTC，单位毫秒），41 位时间戳可用约 69 年
	epochMillis int64 = 1735689600000
	// nodeBits 节点ID位数，支持最多 1024 个节点
	nodeBits uint8 = 10
	// seqBits 同一毫秒内的序列号位数，单节点每毫秒最多生成 4096 个ID
	seqBits uint8 = 12

	// maxNode 节点ID最大值（1023）
	maxNode int64 = -1 ^ (-1 << nodeBits)
	// maxSeq 序列号最大值（4095）
	maxSeq int64 = -1 ^ (-1 << seqBits)

	// nodeShift 节点ID左移位数
	nodeShift = seqBits
	// timeShift 时间戳左移位数
	timeShift = seqBits + nodeBits
)

// Snowflake 雪花ID生成器，并发安全。
// 结构：41 位毫秒时间戳 + 10 位节点ID + 12 位序列号，保证分布式环境下全局唯一且趋势递增。
type Snowflake struct {
	mu       sync.Mutex
	lastTime int64 // 上次生成ID的毫秒时间戳
	node     int64 // 节点ID，取值范围 [0, maxNode]
	seq      int64 // 当前毫秒内的序列号
}

// NewSnowflake 创建指定节点ID的雪花生成器。
// node: 节点ID，取值范围 [0, 1023]，多实例部署时需保证各实例取值不同以避免ID冲突。
// 返回值：生成器实例；当 node 超出范围时返回错误。
func NewSnowflake(node int64) (*Snowflake, error) {
	if node < 0 || node > maxNode {
		return nil, fmt.Errorf("idx: 节点ID %d 超出有效范围 [0, %d]", node, maxNode)
	}
	return &Snowflake{node: node}, nil
}

// NextID 生成下一个全局唯一ID，并发安全。
// 同一毫秒序列号耗尽时自旋等待至下一毫秒；检测到时钟回拨时等待追平，避免生成重复或更小的ID。
func (s *Snowflake) NextID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	// 时钟回拨：等待追平上次时间，保证单调
	if now < s.lastTime {
		now = s.lastTime
	}

	if now == s.lastTime {
		// 同一毫秒内递增序列号
		s.seq = (s.seq + 1) & maxSeq
		if s.seq == 0 {
			// 序列号溢出，自旋至下一毫秒
			for now <= s.lastTime {
				now = time.Now().UnixMilli()
			}
		}
	} else {
		s.seq = 0
	}

	s.lastTime = now
	return ((now - epochMillis) << timeShift) | (s.node << nodeShift) | s.seq
}

var (
	defaultOnce sync.Once
	defaultSF   *Snowflake
)

// Default 返回进程级共享的默认雪花生成器。
// 节点ID由主机名与进程ID哈希派生，无需额外配置即可在多实例部署下降低冲突概率。
func Default() *Snowflake {
	defaultOnce.Do(func() {
		// deriveNodeID 始终返回合法范围内的节点ID，此处不会出错
		defaultSF, _ = NewSnowflake(deriveNodeID())
	})
	return defaultSF
}

// deriveNodeID 由主机名与进程ID派生节点ID，结果落在 [0, maxNode] 区间。
func deriveNodeID() int64 {
	h := fnv.New32a()
	if host, err := os.Hostname(); err == nil {
		_, _ = h.Write([]byte(host))
	}
	// 混入进程ID，降低同主机多实例的节点ID碰撞概率
	pid := os.Getpid()
	_, _ = h.Write([]byte{byte(pid), byte(pid >> 8), byte(pid >> 16), byte(pid >> 24)})
	return int64(h.Sum32()) & maxNode
}
