package logic

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"kemiluo/pkg/async"
	"kemiluo/pkg/authx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/gmt/internal/repo"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// 登录失败限流相关常量
const (
	// loginMaxFailures 登录失败次数阈值，达到后暂时锁定账号
	loginMaxFailures = 3
	// loginLockWindow 登录失败计数窗口时长（同时作为锁定时长）
	loginLockWindow = 1 * time.Minute
)

// AuthLogic 认证业务逻辑，负责管理员登录与令牌签发
type AuthLogic struct {
	mongoSvc   *mongox.Service
	jwt        *authx.JWT
	asyncSvc   *async.Service
	tokenStore *authx.TokenStore
	redisSvc   *redisx.Service
}

// NewAuthLogic 创建认证业务逻辑实例
// redisSvc 用于登录失败限流计数，可为 nil（此时限流能力降级关闭）
func NewAuthLogic(mongoSvc *mongox.Service, jwt *authx.JWT, asyncSvc *async.Service, tokenStore *authx.TokenStore, redisSvc *redisx.Service) *AuthLogic {
	return &AuthLogic{
		mongoSvc:   mongoSvc,
		jwt:        jwt,
		asyncSvc:   asyncSvc,
		tokenStore: tokenStore,
		redisSvc:   redisSvc,
	}
}

// Login 管理员登录
// 流程：查询账号 → 校验密码 → 检查状态 → 签发JWT → 更新登录信息 → 记录日志
// req: 登录请求DTO（包含username、password）
// ip: 请求来源IP，用于记录最后登录IP
// 返回 LoginResp（token和有效期）
func (l *AuthLogic) Login(ctx context.Context, req *dto.LoginReq, ip string) (*dto.LoginResp, error) {
	// 登录失败限流：达到阈值后临时锁定，防止暴力破解
	if l.isLoginLocked(ctx, req.Username, ip) {
		return nil, model.ErrLoginLocked
	}

	adminRepo := repo.NewAdminRepo(l.mongoSvc.Client())

	// 按用户名查找管理员账号
	admin, err := adminRepo.FindByUsername(ctx, req.Username)
	if err != nil {
		if err == mongox.ErrNotFound {
			// 账号不存在也计入失败次数，避免通过提示差异枚举账号
			l.recordLoginFailure(ctx, req.Username, ip)
			return nil, model.ErrAdminNotFound
		}
		return nil, fmt.Errorf("查询管理员账号失败: %w", err)
	}

	// bcrypt校验密码
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(req.Password)); err != nil {
		l.recordLoginFailure(ctx, req.Username, ip)
		return nil, model.ErrPasswordWrong
	}

	// 密码校验通过后清除失败计数
	l.clearLoginFailures(ctx, req.Username, ip)

	// 检查账号状态
	if !admin.IsActive() {
		return nil, model.ErrAccountDisabled
	}

	// 签发JWT（以登录账号作为UserID，便于日志与操作审计直接体现账号名）
	// role 按账号是否超级管理员区分：super=超级管理员，admin=普通管理员
	role := "admin"
	if admin.IsSuper {
		role = "super"
	}
	token, err := l.jwt.GenerateToken(admin.Username, role)
	if err != nil {
		return nil, fmt.Errorf("签发JWT失败: %w", err)
	}
	if l.tokenStore != nil {
		if err := l.tokenStore.Save(ctx, admin.Username, token, l.jwt.TokenTTL()); err != nil {
			return nil, fmt.Errorf("保存管理员登录态失败: %w", err)
		}
	}

	// 异步更新最后登录信息（失败不阻断登录）
	l.asyncUpdateLastLogin(admin.ID.Hex(), ip, req.RequestID())

	// 异步记录登录操作日志
	l.logLoginAsync(admin.Username, admin.Username, ip, req.RequestID())

	return &dto.LoginResp{
		Token:  token,
		Expire: int64(l.jwt.TokenTTL().Seconds()),
	}, nil
}

// asyncUpdateLastLogin 异步更新管理员最后登录IP和时间
// 更新失败仅记录警告，不影响登录结果
func (l *AuthLogic) asyncUpdateLastLogin(adminIDHex, ip, requestID string) {
	if l.asyncSvc == nil {
		return
	}
	mongoSvc := l.mongoSvc
	ctx := async.NewContext().
		Set("adminId", adminIDHex).
		Set("ip", ip).
		Set("requestId", requestID)
	l.asyncSvc.SubmitFunc("log", "updateLastLogin", func(c *async.Context) error {
		adminRepo := repo.NewAdminRepo(mongoSvc.Client())
		id, parseErr := parseObjectID(c.GetString("adminId"))
		if parseErr != nil {
			return parseErr
		}
		if updateErr := adminRepo.UpdateLastLogin(context.Background(), id, c.GetString("ip")); updateErr != nil {
			logx.Warn("更新管理员最后登录信息失败",
				zap.String("requestId", c.GetString("requestId")),
				zap.String("adminId", c.GetString("adminId")),
				zap.Error(updateErr),
			)
		}
		return nil
	}, ctx)
}

// logLoginAsync 异步记录登录操作日志
func (l *AuthLogic) logLoginAsync(uid, username, ip, requestID string) {
	if l.asyncSvc == nil {
		return
	}
	mongoSvc := l.mongoSvc
	ctx := async.NewContext().
		Set("uid", uid).
		Set("username", username).
		Set("ip", ip).
		Set("requestId", requestID)
	l.asyncSvc.SubmitFunc("log", "loginLog", func(c *async.Context) error {
		logRepo := repo.NewOperationLogRepo(mongoSvc.Client())
		return logRepo.InsertLog(context.Background(), &model.OperationLog{
			UID:      c.GetString("uid"),
			Username: c.GetString("username"),
			Module:   "Auth",
			Action:   "Login",
			Method:   "POST",
			URI:      "/api/admin/login",
			IP:       c.GetString("ip"),
			Request:  nil, // 登录请求不记录密码字段
		})
	}, ctx)
}

// loginFailKey 生成登录失败计数 key：按用户名+IP 区分，
// 避免攻击者用大量错误密码把账号拖入锁定后影响其它合法来源登录
func (l *AuthLogic) loginFailKey(username, ip string) string {
	return "gmt:login:fail:" + username + ":" + ip
}

// isLoginLocked 判断账号（+IP）是否已因失败次数过多被暂时锁定
func (l *AuthLogic) isLoginLocked(ctx context.Context, username, ip string) bool {
	if l.redisSvc == nil || l.redisSvc.Client() == nil {
		return false
	}
	val, err := l.redisSvc.Client().Get(ctx, l.loginFailKey(username, ip))
	if err != nil {
		// 计数读取失败时降级放行，不阻断正常登录
		return false
	}
	n, _ := strconv.Atoi(val)
	return n >= loginMaxFailures
}

// recordLoginFailure 记录一次登录失败并维护失败计数与锁定窗口
func (l *AuthLogic) recordLoginFailure(ctx context.Context, username, ip string) {
	if l.redisSvc == nil || l.redisSvc.Client() == nil {
		return
	}
	rdb := l.redisSvc.Client().Raw()
	key := l.loginFailKey(username, ip)
	// 首次失败时初始化 key 并设置过期窗口；SetNX 返回 created=false 表示 key 已存在（非 redis.Nil），
	// 属正常情况仍继续自增；INCR 不重置 TTL，因此失败计数窗口固定不滑动
	if _, err := rdb.SetNX(ctx, key, "0", loginLockWindow).Result(); err != nil {
		logx.Warn("初始化登录失败计数失败",
			zap.String("userId", username),
			zap.Error(err),
		)
		return
	}
	if err := rdb.Incr(ctx, key).Err(); err != nil {
		logx.Warn("更新登录失败计数失败",
			zap.String("userId", username),
			zap.Error(err),
		)
	}
}

// clearLoginFailures 登录成功后清除失败计数
func (l *AuthLogic) clearLoginFailures(ctx context.Context, username, ip string) {
	if l.redisSvc == nil || l.redisSvc.Client() == nil {
		return
	}
	rdb := l.redisSvc.Client().Raw()
	key := l.loginFailKey(username, ip)
	if err := rdb.Del(ctx, key).Err(); err != nil {
		logx.Warn("清除登录失败计数失败",
			zap.String("userId", username),
			zap.Error(err),
		)
	}
}
