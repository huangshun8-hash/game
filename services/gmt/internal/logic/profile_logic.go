package logic

import (
	"context"
	"fmt"

	"kemiluo/pkg/async"
	"kemiluo/pkg/authx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/gmt/internal/repo"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// ProfileLogic 个人资料业务逻辑，负责当前登录管理员的信息查询与修改
type ProfileLogic struct {
	mongoSvc   *mongox.Service
	asyncSvc   *async.Service
	tokenStore *authx.TokenStore
}

// NewProfileLogic 创建个人资料业务逻辑实例
func NewProfileLogic(mongoSvc *mongox.Service, asyncSvc *async.Service, tokenStore *authx.TokenStore) *ProfileLogic {
	return &ProfileLogic{
		mongoSvc:   mongoSvc,
		asyncSvc:   asyncSvc,
		tokenStore: tokenStore,
	}
}

// GetProfile 获取当前登录管理员的基本信息
// username: JWT中解析的登录账号
func (l *ProfileLogic) GetProfile(ctx context.Context, username string) (*dto.ProfileResp, error) {
	adminRepo := repo.NewAdminRepo(l.mongoSvc.Client())
	admin, err := adminRepo.FindByUsername(ctx, username)
	if err != nil {
		if err == mongox.ErrNotFound {
			return nil, model.ErrAdminNotFound
		}
		return nil, fmt.Errorf("查询管理员信息失败: %w", err)
	}

	return &dto.ProfileResp{
		ID:       admin.ID.Hex(),
		Username: admin.Username,
		Nickname: admin.Nickname,
		IsSuper:  admin.IsSuper,
	}, nil
}

// UpdateProfile 修改当前登录管理员的昵称
// req: 资料修改请求DTO（包含nickname）
// username: JWT中解析的登录账号
// ip: 请求来源IP（用于操作日志）
func (l *ProfileLogic) UpdateProfile(ctx context.Context, req *dto.UpdateProfileReq, username string, ip string) error {
	if req.Nickname == "" {
		return fmt.Errorf("%w: nickname is required", model.ErrAdminNotFound)
	}

	adminRepo := repo.NewAdminRepo(l.mongoSvc.Client())

	// 确认管理员存在
	admin, err := adminRepo.FindByUsername(ctx, username)
	if err != nil {
		if err == mongox.ErrNotFound {
			return model.ErrAdminNotFound
		}
		return fmt.Errorf("查询管理员信息失败: %w", err)
	}

	if err := adminRepo.UpdateProfile(ctx, admin.ID, req.Nickname); err != nil {
		return fmt.Errorf("更新个人资料失败: %w", err)
	}

	// 异步记录操作日志
	l.logOperationAsync(admin.Username, admin.Username, "Profile", "Update", "PUT", "/api/admin/profile",
		ip, req.RequestID(), map[string]string{"nickname": req.Nickname})

	return nil
}

// ChangePassword 修改当前登录管理员的密码
// req: 密码修改请求DTO（包含oldPassword、newPassword）
// username: JWT中解析的登录账号
// ip: 请求来源IP（用于操作日志）
func (l *ProfileLogic) ChangePassword(ctx context.Context, req *dto.ChangePasswordReq, username string, ip string) error {
	if req.OldPassword == "" || req.NewPassword == "" {
		return fmt.Errorf("old password and new password are required")
	}
	if err := ValidateAdminPassword(req.NewPassword); err != nil {
		return err
	}

	adminRepo := repo.NewAdminRepo(l.mongoSvc.Client())
	admin, err := adminRepo.FindByUsername(ctx, username)
	if err != nil {
		if err == mongox.ErrNotFound {
			return model.ErrAdminNotFound
		}
		return fmt.Errorf("查询管理员信息失败: %w", err)
	}

	// 校验旧密码
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(req.OldPassword)); err != nil {
		return model.ErrPasswordWrong
	}

	// 生成新密码哈希（bcrypt cost=12，兼顾安全与性能）
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	if err != nil {
		return fmt.Errorf("加密新密码失败: %w", err)
	}

	if err := adminRepo.UpdatePassword(ctx, admin.ID, string(hashed)); err != nil {
		return fmt.Errorf("更新密码失败: %w", err)
	}
	// Revoke 失败仅告警不阻断：密码已修改生效，避免前端误判"修改失败"
	if l.tokenStore != nil {
		if err := l.tokenStore.Revoke(ctx, admin.Username); err != nil {
			logx.Warn("撤销管理员旧登录态失败",
				zap.String("requestId", req.RequestID()),
				zap.String("userId", admin.Username),
				zap.Error(err),
			)
		}
	}

	logx.Info("管理员修改密码成功",
		zap.String("requestId", req.RequestID()),
		zap.String("userId", admin.Username),
	)

	// 异步记录操作日志（不记录密码内容）
	l.logOperationAsync(admin.Username, admin.Username, "Profile", "ChangePassword", "POST",
		"/api/admin/changePassword", ip, req.RequestID(), nil)

	return nil
}

// logOperationAsync 异步记录操作日志
// uid/username: 操作管理员标识；module/action/method/uri: 操作描述；ip/requestID: 请求上下文；reqData: 脱敏后的请求参数
func (l *ProfileLogic) logOperationAsync(uid, username, module, action, method, uri, ip, requestID string, reqData any) {
	if l.asyncSvc == nil {
		return
	}
	mongoSvc := l.mongoSvc
	ctx := async.NewContext().
		Set("uid", uid).
		Set("username", username).
		Set("module", module).
		Set("action", action).
		Set("method", method).
		Set("uri", uri).
		Set("ip", ip).
		Set("requestId", requestID).
		Set("reqData", reqData)
	l.asyncSvc.SubmitFunc("log", "operationLog", func(c *async.Context) error {
		logRepo := repo.NewOperationLogRepo(mongoSvc.Client())
		return logRepo.InsertLog(context.Background(), &model.OperationLog{
			UID:      c.GetString("uid"),
			Username: c.GetString("username"),
			Module:   c.GetString("module"),
			Action:   c.GetString("action"),
			Method:   c.GetString("method"),
			URI:      c.GetString("uri"),
			IP:       c.GetString("ip"),
			Request:  c.Get("reqData"),
		})
	}, ctx)
}
