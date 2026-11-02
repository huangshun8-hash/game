package repo

import (
	"context"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// AdminRepo 管理员账号数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力
type AdminRepo struct {
	mongox.BaseRepo[model.Admin]
}

// NewAdminRepo 创建管理员仓库实例
func NewAdminRepo(client *mongox.Client) *AdminRepo {
	return &AdminRepo{BaseRepo: mongox.NewBaseRepo[model.Admin](client, collectionAdmins)}
}

// FindByUsername 根据登录账号查找管理员
func (r *AdminRepo) FindByUsername(ctx context.Context, username string) (*model.Admin, error) {
	return r.FindOne(ctx, bson.D{{Key: "username", Value: username}})
}

// InsertAdmin 创建新管理员，自动填充时间戳
func (r *AdminRepo) InsertAdmin(ctx context.Context, admin *model.Admin) error {
	now := time.Now()
	admin.CreatedAt = now
	admin.UpdatedAt = now
	id, err := r.Insert(ctx, admin)
	if err != nil {
		return err
	}
	admin.ID = id
	return nil
}

// UpdateLastLogin 更新管理员最后登录时间和IP
func (r *AdminRepo) UpdateLastLogin(ctx context.Context, adminID bson.ObjectID, ip string) error {
	now := time.Now()
	_, err := r.UpdateByID(ctx, adminID, bson.D{{Key: "$set", Value: bson.D{
		{Key: "lastLoginAt", Value: now},
		{Key: "lastLoginIp", Value: ip},
		{Key: "updatedAt", Value: now},
	}}})
	return err
}

// UpdateProfile 更新管理员昵称
// 仅允许更新非敏感字段（昵称），密码通过专用方法修改
func (r *AdminRepo) UpdateProfile(ctx context.Context, adminID bson.ObjectID, nickname string) error {
	_, err := r.UpdateByID(ctx, adminID, bson.D{{Key: "$set", Value: bson.D{
		{Key: "nickname", Value: nickname},
		{Key: "updatedAt", Value: time.Now()},
	}}})
	return err
}

// UpdatePassword 更新管理员密码（已加密的哈希值）
func (r *AdminRepo) UpdatePassword(ctx context.Context, adminID bson.ObjectID, hashedPassword string) error {
	_, err := r.UpdateByID(ctx, adminID, bson.D{{Key: "$set", Value: bson.D{
		{Key: "password", Value: hashedPassword},
		{Key: "updatedAt", Value: time.Now()},
	}}})
	return err
}

// UpdateAdminInfo 更新管理员基本信息（昵称、状态）
func (r *AdminRepo) UpdateAdminInfo(ctx context.Context, adminID bson.ObjectID, nickname string, status int) error {
	_, err := r.UpdateByID(ctx, adminID, bson.D{{Key: "$set", Value: bson.D{
		{Key: "nickname", Value: nickname},
		{Key: "status", Value: status},
		{Key: "updatedAt", Value: time.Now()},
	}}})
	return err
}

// UpdateStatus 更新管理员账号状态（启用/禁用）
func (r *AdminRepo) UpdateStatus(ctx context.Context, adminID bson.ObjectID, status int) error {
	_, err := r.UpdateByID(ctx, adminID, bson.D{{Key: "$set", Value: bson.D{
		{Key: "status", Value: status},
		{Key: "updatedAt", Value: time.Now()},
	}}})
	return err
}

// ListAdmins 查询所有管理员列表
func (r *AdminRepo) ListAdmins(ctx context.Context) ([]model.Admin, error) {
	return r.FindMany(ctx, bson.D{})
}
