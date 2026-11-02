package wsx

import (
	"time"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/logx"
)

// PushPayload 定向推送负载（供进程内推送与外部内部接口复用）
type PushPayload struct {
	// Type 推送帧类型（缺省按 WsMsgTypePush 处理）
	Type cspb.WsMsgType
	// Data 数据负载（可为 proto.Message、[]byte 或任意可 JSON 序列化结构）
	Data any
	// BizID 业务消息ID（可选，用于客户端幂等/去重）
	BizID string
}

// PushResult 单玩家定向推送结果
type PushResult struct {
	// Delivered 实际送达的连接数
	Delivered int `json:"delivered"`
	// Online 推送前目标玩家在线连接数
	Online int `json:"online"`
}

// BatchPushResult 批量推送结果
type BatchPushResult struct {
	// Total 目标玩家总数
	Total int `json:"total"`
	// Delivered 实际送达的连接数
	Delivered int `json:"delivered"`
	// Offline 未在线的目标玩家数
	Offline int `json:"offline"`
}

// Stats 在线统计结果
type Stats struct {
	// TotalConnections 当前总连接数
	TotalConnections int `json:"totalConnections"`
	// OnlinePlayers 当前在线玩家数
	OnlinePlayers int `json:"onlinePlayers"`
}

// encodePayload 将推送负载编码为消息帧内字节：
//   - 实现 proto.Message 接口：protobuf 编码；
//   - []byte：原样返回；
//   - 其他任意可序列化对象：sonic JSON 编码（AGENTS.md 规定 JSON 统一使用 sonic）。
func encodePayload(payload any) []byte {
	switch v := payload.(type) {
	case nil:
		return nil
	case []byte:
		return v
	}
	if pm, ok := payload.(proto.Message); ok {
		b, err := proto.Marshal(pm)
		if err != nil {
			logx.Warn("WebSocket 推送负载 proto 序列化失败", zap.Error(err))
			return nil
		}
		return b
	}
	b, err := sonic.Marshal(payload)
	if err != nil {
		logx.Warn("WebSocket 推送负载 JSON 序列化失败", zap.Error(err))
		return nil
	}
	return b
}

// payloadMsgID 从推送负载类型推导业务推送消息标识 msgId，
// 供客户端识别 WsMessage.data 内的具体业务消息（与 bizId 的幂等去重职责相互独立）。
// 命中已注册的 proto 消息类型时返回对应标识；未注册类型（JSON / []byte / 新类型未同步）返回 WsMsgIdNone。
func payloadMsgID(data any) cspb.WsMsgId {
	switch data.(type) {
	case *cspb.PaySuccessPush:
		return cspb.WsMsgId_WsMsgIdPaySuccessPush
	case *cspb.KickedOfflinePush:
		return cspb.WsMsgId_WsMsgIdKickedOfflinePush
	default:
		return cspb.WsMsgId_WsMsgIdNone
	}
}

// buildErrorFrameBytes 构建错误帧（负载为 WsErrorInfo）的编码字节。
// 供会话内的错误应答（如上行消息处理失败）使用；建连阶段的拒绝改为只下发 Close 码，
// 不再使用本函数。序列化失败返回 nil。
//
// 参数：code 错误码（复用 httpx 错误码体系）；message 英文错误描述（面向客户端）
// 返回：可直接写入连接的 protobuf 帧字节，nil 表示构建失败
func buildErrorFrameBytes(code int32, message string) []byte {
	now := time.Now().UnixMilli()
	payload, err := proto.Marshal(&cspb.WsErrorInfo{Code: code, Message: message})
	if err != nil {
		logx.Warn("WebSocket 错误帧负载序列化失败", zap.Error(err))
		return nil
	}
	frame, err := proto.Marshal(&cspb.WsMessage{
		Type:      cspb.WsMsgType_WsMsgTypeError,
		Data:      payload,
		Timestamp: now,
	})
	if err != nil {
		logx.Warn("WebSocket 错误帧序列化失败", zap.Error(err))
		return nil
	}
	return frame
}

// buildPushFrame 构建业务推送消息帧。
// 帧类型缺省（WsMsgTypeNone）时自动使用 WsMsgTypePush。
// msgId 依据负载类型自动推导（见 payloadMsgID），客户端据此识别 data 内的具体业务消息。
// 参数 payload 为 nil 时返回 nil，调用方须自行处理空负载（见 Hub.PushToPlayer / PushToPlayers）。
func buildPushFrame(payload *PushPayload) *cspb.WsMessage {
	if payload == nil {
		return nil
	}
	msgType := payload.Type
	if msgType == cspb.WsMsgType_WsMsgTypeNone {
		msgType = cspb.WsMsgType_WsMsgTypePush
	}
	return &cspb.WsMessage{
		Type:      msgType,
		Data:      encodePayload(payload.Data),
		Timestamp: time.Now().UnixMilli(),
		BizId:     payload.BizID,
		MsgId:     payloadMsgID(payload.Data),
	}
}
