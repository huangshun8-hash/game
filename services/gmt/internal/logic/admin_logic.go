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

// AdminLogic 管理员账号管理业务逻辑
// 负责管理员的增删改查及账号状态控制
type AdminLogic struct {
	mongoSvc   *mongox.Service
	asyncSvc   *async.Service
	tokenStore *authx.TokenStore
}

// NewAdminLogic 创建管理员账号管理业务逻辑实例
func NewAdminLogic(mongoSvc *mongox.Service, asyncSvc *async.Service, tokenStore *authx.TokenStore) *AdminLogic {
	return &AdminLogic{
		mongoSvc:   mongoSvc,
		asyncSvc:   asyncSvc,
		tokenStore: tokenStore,
	}
}

// ListAdmins 获取所有管理员账号列表
// operatorID: 当前操作管理员ID（用于日志）
func (l *AdminLogic) ListAdmins(ctx context.Context) ([]*dto.AdminItem, error) {
	adminRepo := repo.NewAdminRepo(l.mongoSvc.Client())
	admins, err := adminRepo.ListAdmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询管理员列表失败: %w", err)
	}

	items := make([]*dto.AdminItem, 0, len(admins))
	for i := range admins {
		a := &admins[i]
		item := &dto.AdminItem{
			ID:          a.ID.Hex(),
			Username:    a.Username,
			Nickname:    a.Nickname,
			Status:      a.Status,
			IsSuper:     a.IsSuper,
			LastLoginIP: a.LastLoginIP,
			CreatedAt:   a.CreatedAt.UnixMilli(),
		}
		if a.LastLoginAt != nil {
			ts := a.LastLoginAt.UnixMilli()
			item.LastLoginAt = &ts
		}
		items = append(items, item)
	}
	return items, nil
}

// CreateAdmin 新增管理员账号（仅超级管理员可操作）
// req: 创建请求DTO；operator: 执行操作的管理员登录账号（用于日志）；isOperatorSuper: 是否超级管理员；ip: 来源IP
func (l *AdminLogic) CreateAdmin(ctx context.Context, req *dto.CreateAdminReq, operator string, isOperatorSuper bool, ip string) (*dto.AdminItem, error) {
	// 仅超级管理员可创建账号
	if !isOperatorSuper {
		return nil, model.ErrForbidden
	}
	if req.Username == "" || req.Password == "" {
		return nil, fmt.Errorf("username and password are required")
	}
	if err := ValidateAdminPassword(req.Password); err != nil {
		return nil, err
	}

	adminRepo := repo.NewAdminRepo(l.mongoSvc.Client())

	// 检查用户名是否已存在
	existing, err := adminRepo.FindByUsername(ctx, req.Username)
	if err != nil && err != mongox.ErrNotFound {
		return nil, fmt.Errorf("检查用户名重复失败: %w", err)
	}
	if existing != nil {
		return nil, model.ErrAdminExists
	}

	// 加密密码
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		return nil, fmt.Errorf("加密密码失败: %w", err)
	}

	nickname := req.Nickname
	if nickname == "" {
		nickname = req.Username
	}

	admin := &model.Admin{
		Username: req.Username,
		Nickname: nickname,
		Password: string(hashed),
		Status:   model.AdminStatusNormal,
		IsSuper:  false,
	}

	if err := adminRepo.InsertAdmin(ctx, admin); err != nil {
		if mongox.IsDuplicateKeyError(err) {
			return nil, model.ErrAdminExists
		}
		return nil, fmt.Errorf("创建管理员账号失败: %w", err)
	}

	logx.Info("管理员账号创建成功",
		zap.String("requestId", req.RequestID()),
		zap.String("userId", operator),
		zap.String("newUsername", admin.Username),
	)

	// 异步记录操作日志（不含密码字段）
	l.logOperationAsync(operator, operator, "Admin", "Create", "POST", "/api/admins",
		ip, req.RequestID(), map[string]any{"username": req.Username, "nickname": nickname})

	return &dto.AdminItem{
		ID:        admin.ID.Hex(),
		Username:  admin.Username,
		Nickname:  admin.Nickname,
		Status:    admin.Status,
		IsSuper:   admin.IsSuper,
		CreatedAt: admin.CreatedAt.UnixMilli(),
	}, nil
}

// UpdateAdmin 编辑管理员账号信息（昵称、状态，仅超级管理员可操作）
// req: 编辑请求DTO；operator: 执行操作的管理员登录账号；isOperatorSuper: 是否超级管理员；ip: 来源IP
func (l *AdminLogic) UpdateAdmin(ctx context.Context, req *dto.UpdateAdminReq, operator string, isOperatorSuper bool, ip string) error {
	// 仅超级管理员可编辑账号
	if !isOperatorSuper {
		return model.ErrForbidden
	}

	targetID, err := parseObjectID(req.ID)
	if err != nil {
		return model.ErrAdminNotFound
	}

	adminRepo := repo.NewAdminRepo(l.mongoSvc.Client())
	if _, err := adminRepo.FindByID(ctx, targetID); err != nil {
		if err == mongox.ErrNotFound {
			return model.ErrAdminNotFound
		}
		return fmt.Errorf("查询目标管理员失败: %w", err)
	}

	if err := adminRepo.UpdateAdminInfo(ctx, targetID, req.Nickname, req.Status); err != nil {
		return fmt.Errorf("更新管理员信息失败: %w", err)
	}

	logx.Info("管理员账号信息已更新",
		zap.String("requestId", req.RequestID()),
		zap.String("userId", operator),
		zap.String("targetId", req.ID),
	)

	// 异步记录操作日志
	l.logOperationAsync(operator, operator, "Admin", "Update", "PUT",
		"/api/admins/"+req.ID, ip, req.RequestID(),
		map[string]any{"id": req.ID, "nickname": req.Nickname, "status": req.Status})

	return nil
}

// DisableAdmin 禁用管理员账号（仅超级管理员可操作，系统不支持物理删除账号）
// req: 目标账号请求DTO；operator: 执行操作的管理员登录账号；isOperatorSuper: 是否超级管理员；ip: 来源IP
func (l *AdminLogic) DisableAdmin(ctx context.Context, req *dto.AdminIDReq, operator string, isOperatorSuper bool, ip string) error {
	// 仅超级管理员可禁用账号
	if !isOperatorSuper {
		return model.ErrForbidden
	}

	targetID, err := parseObjectID(req.ID)
	if err != nil {
		return model.ErrAdminNotFound
	}

	adminRepo := repo.NewAdminRepo(l.mongoSvc.Client())
	target, err := adminRepo.FindByID(ctx, targetID)
	if err != nil {
		if err == mongox.ErrNotFound {
			return model.ErrAdminNotFound
		}
		return fmt.Errorf("查询目标管理员失败: %w", err)
	}

	// 不允许禁用自己（按登录账号比对）
	if target.Username == operator {
		return model.ErrForbidden
	}

	if err := adminRepo.UpdateStatus(ctx, targetID, model.AdminStatusDisabled); err != nil {
		return fmt.Errorf("禁用管理员账号失败: %w", err)
	}
	// Revoke 失败仅告警不阻断：禁用已在数据库中生效，避免前端误判"禁用失败"
	if l.tokenStore != nil {
		if err := l.tokenStore.Revoke(ctx, target.Username); err != nil {
			logx.Warn("撤销被禁用管理员登录态失败",
				zap.String("requestId", req.RequestID()),
				zap.String("userId", operator),
				zap.String("targetUsername", target.Username),
				zap.Error(err),
			)
		}
	}

	logx.Info("管理员账号已禁用",
		zap.String("requestId", req.RequestID()),
		zap.String("userId", operator),
		zap.String("targetId", req.ID),
		zap.String("targetUsername", target.Username),
	)

	// 异步记录操作日志
	l.logOperationAsync(operator, operator, "Admin", "Disable", "DELETE",
		"/api/admins/"+req.ID, ip, req.RequestID(),
		map[string]any{"id": req.ID, "username": target.Username})

	return nil
}

// ResetPassword 超级管理员重置其他管理员账号密码（仅超级管理员可操作）
// req: 重置密码请求DTO；operator: 执行操作的管理员登录账号；isOperatorSuper: 是否超级管理员；ip: 来源IP
func (l *AdminLogic) ResetPassword(ctx context.Context, req *dto.ResetPasswordReq, operator string, isOperatorSuper bool, ip string) error {
	// 仅超级管理员可重置他人密码
	if !isOperatorSuper {
		return model.ErrForbidden
	}
	if err := ValidateAdminPassword(req.NewPassword); err != nil {
		return err
	}

	targetID, err := parseObjectID(req.ID)
	if err != nil {
		return model.ErrAdminNotFound
	}

	adminRepo := repo.NewAdminRepo(l.mongoSvc.Client())
	target, err := adminRepo.FindByID(ctx, targetID)
	if err != nil {
		if err == mongox.ErrNotFound {
			return model.ErrAdminNotFound
		}
		return fmt.Errorf("查询目标管理员失败: %w", err)
	}

	// 生成新密码哈希（bcrypt cost=12，兼顾安全与性能）
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	if err != nil {
		return fmt.Errorf("加密新密码失败: %w", err)
	}

	if err := adminRepo.UpdatePassword(ctx, targetID, string(hashed)); err != nil {
		return fmt.Errorf("重置密码失败: %w", err)
	}
	// Revoke 失败仅告警不阻断：密码已重置生效，避免前端误判"重置失败"
	if l.tokenStore != nil {
		if err := l.tokenStore.Revoke(ctx, target.Username); err != nil {
			logx.Warn("撤销目标管理员旧登录态失败",
				zap.String("requestId", req.RequestID()),
				zap.String("userId", operator),
				zap.String("targetUsername", target.Username),
				zap.Error(err),
			)
		}
	}

	logx.Info("超级管理员重置账号密码成功",
		zap.String("requestId", req.RequestID()),
		zap.String("userId", operator),
		zap.String("targetId", req.ID),
		zap.String("targetUsername", target.Username),
	)

	// 异步记录操作日志（不记录密码内容）
	l.logOperationAsync(operator, operator, "Admin", "ResetPassword", "PUT",
		"/api/admins/"+req.ID+"/password", ip, req.RequestID(),
		map[string]any{"id": req.ID, "username": target.Username})

	return nil
}

// logOperationAsync 异步记录操作日志
func (l *AdminLogic) logOperationAsync(uid, username, module, action, method, uri, ip, requestID string, reqData any) {
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
