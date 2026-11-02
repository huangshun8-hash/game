// Package orderid 提供订单号生成工具。
// 基于雪花算法生成全局唯一、趋势递增的订单号字符串。
package orderid

import (
	"strconv"

	"kemiluo/pkg/idx"
)

// New 生成全局唯一订单号
// 底层使用雪花算法，返回 int64 ID 的十进制字符串表示。
func New() string {
	return strconv.FormatInt(idx.Default().NextID(), 10)
}
