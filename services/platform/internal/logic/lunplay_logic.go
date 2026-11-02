package logic

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/signx"
	"kemiluo/services/shared/shareddto"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// 角色查询内部接口路径（gamelogic 侧）
const (
	// roleQueryByPassportPath 按平台用户ID查询角色内部接口路径
	roleQueryByPassportPath = "/internal/role/queryByPassport"
	// roleQueryByRoleIDPath 按角色ID查询角色内部接口路径
	roleQueryByRoleIDPath = "/internal/role/queryByRoleId"
	// roleServerListPath 查询区服列表内部接口路径
	roleServerListPath = "/internal/role/serverList"
)

// LunPlay 角色接口签名容忍窗口（秒级时间戳，防重放）
const lunPlayTimeTolerance = 5 * time.Minute

// ErrLunPlaySignInvalid 签名验证失败
var ErrLunPlaySignInvalid = errors.New("lunplay signature verification failed")

// ErrLunPlayRoleNotFound 角色不存在
var ErrLunPlayRoleNotFound = errors.New("lunplay role not found")

// ErrLunPlayInternal 跨服查询内部错误
var ErrLunPlayInternal = errors.New("lunplay internal error")

// LunPlayLogic LunPlay 平台角色查询业务逻辑
// 负责：请求签名校验、跨服调用 GameLogic 查询角色数据
type LunPlayLogic struct {
	gamelogicSvc   *httpcli.Service
	internalSecret string
	thirdPartyKey  string
}

// NewLunPlayLogic 创建 LunPlay 角色查询业务逻辑
// gamelogicSvc: 用于调用 GameLogic 内部接口的 HTTP 客户端
// internalSecret: 内部服务间验签密钥
// thirdPartyKey: 三方平台接口共用签名密钥（与充值回调共用同一密钥）
func NewLunPlayLogic(gamelogicSvc *httpcli.Service, internalSecret, thirdPartyKey string) *LunPlayLogic {
	return &LunPlayLogic{
		gamelogicSvc:   gamelogicSvc,
		internalSecret: internalSecret,
		thirdPartyKey:  thirdPartyKey,
	}
}

// VerifySignByPassport 校验按区服+passport 查询角色接口的签名
// 规则：ck = md5(passport + t + key + serverCode)
// 返回验证是否通过；t 为秒级时间戳，超出容忍窗口视为无效
func (l *LunPlayLogic) VerifySignByPassport(passport, t, serverCode, ck string) bool {
	raw := passport + t + l.thirdPartyKey + serverCode
	return l.verifySign(raw, t, ck)
}

// VerifySignByRoleID 校验按 roleid 查询角色/区服列表接口的签名
// 规则：ck = md5(roleid + t + key)
// 返回验证是否通过；t 为秒级时间戳，超出容忍窗口视为无效
func (l *LunPlayLogic) VerifySignByRoleID(roleID, t, ck string) bool {
	raw := roleID + t + l.thirdPartyKey
	return l.verifySign(raw, t, ck)
}

// verifySign 校验签名值并检查秒级时间戳容忍窗口
func (l *LunPlayLogic) verifySign(raw, t, ck string) bool {
	// 未配置三方签名密钥时一律拒绝：密钥为空会退化为可被攻击者自行计算的签名原文，存在伪造风险
	if l.thirdPartyKey == "" {
		return false
	}

	// 解析秒级时间戳并检查容忍窗口（防重放）
	ts, err := strconv.ParseInt(t, 10, 64)
	if err != nil || !withinLunPlayTimeWindow(ts) {
		return false
	}

	sum := md5.Sum([]byte(raw))
	expected := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(expected), []byte(ck)) == 1
}

// withinLunPlayTimeWindow 判断秒级时间戳是否落在容忍窗口内
func withinLunPlayTimeWindow(ts int64) bool {
	reqTime := time.Unix(ts, 0)
	diff := time.Since(reqTime)
	if diff < 0 {
		diff = -diff
	}
	return diff <= lunPlayTimeTolerance
}

// QueryByPassport 按平台用户ID（passport=openId）查询角色列表（跨服调用 GameLogic）
// passport 为 LunPlay 平台用户ID，与登录游戏接口一致，映射为玩家 openId
//
// 参数：
//   - req: 查询条件，包含平台用户ID（passport）与可选的区服代码
//   - requestID: 请求追踪ID
//
// 返回：角色信息列表
func (l *LunPlayLogic) QueryByPassport(ctx context.Context, req *shareddto.RoleQueryByPassportReq, requestID string) (*shareddto.RoleQueryResp, error) {
	result := &shareddto.RoleQueryResp{}
	err := l.callGamelogic(ctx, roleQueryByPassportPath, req, result, requestID)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// QueryByRoleID 按角色ID查询角色信息（跨服调用 GameLogic）
//
// 参数：
//   - req: 查询条件，包含角色ID
//   - requestID: 请求追踪ID
//
// 返回：角色信息列表
func (l *LunPlayLogic) QueryByRoleID(ctx context.Context, req *shareddto.RoleQueryByRoleIDReq, requestID string) (*shareddto.RoleQueryResp, error) {
	result := &shareddto.RoleQueryResp{}
	err := l.callGamelogic(ctx, roleQueryByRoleIDPath, req, result, requestID)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ServerList 按角色ID查询区服列表（跨服调用 GameLogic）
//
// 参数：
//   - req: 查询条件，包含角色ID
//   - requestID: 请求追踪ID
//
// 返回：区服列表
func (l *LunPlayLogic) ServerList(ctx context.Context, req *shareddto.RoleQueryByRoleIDReq, requestID string) (*shareddto.ServerListResp, error) {
	result := &shareddto.ServerListResp{}
	err := l.callGamelogic(ctx, roleServerListPath, req, result, requestID)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// callGamelogic 携带内部签名调用 GameLogic 内部接口并解析统一响应信封
// 参数：
//   - path: 内部接口路径
//   - reqBody: 请求体，可为 nil（无请求体的接口）
//   - respBody: 响应体解析目标（须为指针，解析 data 字段）
//   - requestID: 请求追踪ID
func (l *LunPlayLogic) callGamelogic(ctx context.Context, path string, reqBody, respBody any, requestID string) error {
	client := l.gamelogicSvc.Client()
	if client == nil {
		logx.Error("GameLogic 服务客户端未就绪",
			zap.String("requestId", requestID),
			zap.String("path", path),
		)
		return ErrLunPlayInternal
	}

	// 序列化请求体
	var bodyBytes []byte
	var err error
	if reqBody != nil {
		bodyBytes, err = sonic.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("序列化角色查询请求失败: %w", err)
		}
	}

	// 内部服务间签名（HMAC-SHA256 + nonce 防重放）
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.NewString()
	signature := signx.Sign([]byte(l.internalSecret), timestamp, nonce, bodyBytes)

	// 发送 HTTP 请求
	resp, err := client.Post(ctx, path, bodyBytes,
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Signature", signature),
		httpcli.WithHeader("X-Timestamp", timestamp),
		httpcli.WithHeader("X-Nonce", nonce),
	)
	if err != nil {
		logx.Error("调用 GameLogic 角色查询接口失败",
			zap.String("requestId", requestID),
			zap.String("path", path),
			zap.Error(err),
		)
		return ErrLunPlayInternal
	}
	defer resp.Close()

	if resp.StatusCode() != 200 {
		logx.Error("调用 GameLogic 角色查询接口返回非200",
			zap.String("requestId", requestID),
			zap.String("path", path),
			zap.Int("statusCode", resp.StatusCode()),
			zap.String("respBody", resp.String()),
		)
		return ErrLunPlayInternal
	}

	// 解析统一响应信封 {code, message, data}
	// data 用 json.RawMessage 承接原始字节：[]byte 会按 base64 字符串解码导致对象解析失败。
	// 编解码仍由 sonic 完成（与 pkg/httpx/middleware.go 中 RawMessage 用法一致），
	// encoding/json 仅用于引用类型定义。
	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"message"`
		Data json.RawMessage `json:"data"`
	}
	if err := sonic.Unmarshal(resp.Body(), &envelope); err != nil {
		logx.Error("解析 GameLogic 角色查询响应失败",
			zap.String("requestId", requestID),
			zap.String("path", path),
			zap.String("respBody", resp.String()),
			zap.Error(err),
		)
		return ErrLunPlayInternal
	}
	if envelope.Code != 0 {
		logx.Error("GameLogic 角色查询返回业务错误",
			zap.String("requestId", requestID),
			zap.String("path", path),
			zap.Int("code", envelope.Code),
			zap.String("message", envelope.Msg),
		)
		return ErrLunPlayInternal
	}

	// 解析 data 字段到目标结构体
	if len(envelope.Data) > 0 {
		if err := sonic.Unmarshal(envelope.Data, respBody); err != nil {
			logx.Error("解析 GameLogic 角色查询响应数据失败",
				zap.String("requestId", requestID),
				zap.String("path", path),
				zap.Error(err),
			)
			return ErrLunPlayInternal
		}
	}
	return nil
}
