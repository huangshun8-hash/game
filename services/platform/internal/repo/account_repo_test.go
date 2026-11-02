//go:build integration

package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/platform/internal/model"
	"kemiluo/services/shared/sharedmodel"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// getTestURI 读取 MongoDB 测试连接串，未设置时回退到本地默认地址
func getTestURI() string {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	return uri
}

// setupAccountRepo 建立到独立测试库的连接并返回 AccountRepo 与清理函数
func setupAccountRepo(t *testing.T) (*AccountRepo, func()) {
	t.Helper()
	ctx := context.Background()
	// 库名不能含 '.'，故用毫秒时间戳（同时保证并发运行的多组测试互不干扰）
	dbName := fmt.Sprintf("platform_repo_test_%d", time.Now().UnixMilli())

	client, err := mongox.NewClient(ctx, &mongox.Config{
		URI:            getTestURI(),
		Database:       dbName,
		ConnectTimeout: 5 * time.Second,
		PingTimeout:    3 * time.Second,
		MaxPoolSize:    10,
		MinPoolSize:    1,
		MaxIdleTime:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("创建测试 Mongo 客户端失败: %v", err)
	}
	cleanup := func() {
		_ = client.Database().Drop(ctx)
		_ = client.Close(ctx)
	}
	return NewAccountRepo(client), cleanup
}

// TestDeactivateByPlayerID_Success 校验正常账号注销：
// openId 被改写为 del_ 前缀释放原身份，状态置为已注销，且返回作废前的原始 openId。
func TestDeactivateByPlayerID_Success(t *testing.T) {
	repo, cleanup := setupAccountRepo(t)
	defer cleanup()
	ctx := context.Background()

	const playerID int64 = 1001
	const originalOpenID = "guest_uuid_1001"
	if err := repo.InsertAccount(ctx, &model.Account{
		PlayerID:  playerID,
		OpenID:    originalOpenID,
		LoginType: model.LoginTypeGuest,
		Status:    model.StatusNormal,
	}); err != nil {
		t.Fatalf("插入测试账号失败: %v", err)
	}

	got, err := repo.DeactivateByPlayerID(ctx, playerID)
	if err != nil {
		t.Fatalf("注销失败: %v", err)
	}
	if got != originalOpenID {
		t.Fatalf("返回原始 openId = %q, 期望 %q", got, originalOpenID)
	}

	// 复查落库结果：openId 已作废、状态为已注销
	account, err := repo.FindByPlayerID(ctx, playerID)
	if err != nil {
		t.Fatalf("复查账号失败: %v", err)
	}
	if !sharedmodel.IsDeactivatedOpenID(account.OpenID) {
		t.Fatalf("落库 openId %q 未被作废", account.OpenID)
	}
	if account.Status != model.StatusDeactivated {
		t.Fatalf("落库 status = %d, 期望 %d", account.Status, model.StatusDeactivated)
	}
	// 原身份必须已释放：按原 openId 不可再查到该账号
	if _, err := repo.FindByOpenID(ctx, originalOpenID); !errors.Is(err, mongox.ErrNotFound) {
		t.Fatalf("原 openId 仍可查到，身份未释放: err=%v", err)
	}
}

// TestDeactivateByPlayerID_AccountNotFound 校验账号不存在时返回 ErrNotFound，
// 由上层按幂等成功处理。
func TestDeactivateByPlayerID_AccountNotFound(t *testing.T) {
	repo, cleanup := setupAccountRepo(t)
	defer cleanup()
	ctx := context.Background()

	_, err := repo.DeactivateByPlayerID(ctx, 999999)
	if !errors.Is(err, mongox.ErrNotFound) {
		t.Fatalf("账号不存在应返回 ErrNotFound, got %v", err)
	}
}

// seedPlayerIDSeq 直接写入玩家 ID 计数器的当前序号，用于模拟存量数据。
func seedPlayerIDSeq(t *testing.T, repo *AccountRepo, seq int64) {
	t.Helper()
	coll := repo.Collection().Database().Collection(collectionCounters)
	if _, err := coll.UpdateOne(context.Background(),
		bson.D{{Key: "_id", Value: "playerId"}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "seq", Value: seq}}}},
		options.UpdateOne().SetUpsert(true),
	); err != nil {
		t.Fatalf("写入计数器初始值失败: %v", err)
	}
}

// TestNextPlayerID_StartsAtBase 校验全新计数器（无存量数据）时，
// 首个玩家 ID 为 15000001 且后续严格递增。
func TestNextPlayerID_StartsAtBase(t *testing.T) {
	repo, cleanup := setupAccountRepo(t)
	defer cleanup()
	ctx := context.Background()

	first, err := repo.NextPlayerID(ctx)
	if err != nil {
		t.Fatalf("生成首个玩家ID失败: %v", err)
	}
	if first != 15000001 {
		t.Fatalf("首个玩家ID = %d, 期望 15000001", first)
	}

	second, err := repo.NextPlayerID(ctx)
	if err != nil {
		t.Fatalf("生成第二个玩家ID失败: %v", err)
	}
	if second != 15000002 {
		t.Fatalf("第二个玩家ID = %d, 期望 15000002", second)
	}
}

// TestNextPlayerID_BelowBase 校验存量计数器低于基准值时不会回退：
// 序号被抬升到基准值后自增，玩家 ID 仍从 15000001 起。
func TestNextPlayerID_BelowBase(t *testing.T) {
	repo, cleanup := setupAccountRepo(t)
	defer cleanup()
	ctx := context.Background()

	seedPlayerIDSeq(t, repo, 5)

	got, err := repo.NextPlayerID(ctx)
	if err != nil {
		t.Fatalf("生成玩家ID失败: %v", err)
	}
	if got != 15000001 {
		t.Fatalf("低于基准的计数器应抬升至基准，玩家ID = %d, 期望 15000001", got)
	}
}

// TestNextPlayerID_AboveBase 校验存量计数器高于基准值时顺延不倒退，
// 避免与已发放的玩家 ID 冲突。
func TestNextPlayerID_AboveBase(t *testing.T) {
	repo, cleanup := setupAccountRepo(t)
	defer cleanup()
	ctx := context.Background()

	const existing int64 = 20000000
	seedPlayerIDSeq(t, repo, existing)

	got, err := repo.NextPlayerID(ctx)
	if err != nil {
		t.Fatalf("生成玩家ID失败: %v", err)
	}
	if got != existing+1 {
		t.Fatalf("高于基准的计数器应顺延，玩家ID = %d, 期望 %d", got, existing+1)
	}
}

// TestDeactivateByPlayerID_AlreadyDeactivated 校验重复注销幂等：
// 第二次注销命中已作废记录，返回 ErrNoRowsUpdated 且不再改写 openId。
func TestDeactivateByPlayerID_AlreadyDeactivated(t *testing.T) {
	repo, cleanup := setupAccountRepo(t)
	defer cleanup()
	ctx := context.Background()

	const playerID int64 = 1002
	if err := repo.InsertAccount(ctx, &model.Account{
		PlayerID:  playerID,
		OpenID:    "guest_uuid_1002",
		LoginType: model.LoginTypeGuest,
		Status:    model.StatusNormal,
	}); err != nil {
		t.Fatalf("插入测试账号失败: %v", err)
	}

	if _, err := repo.DeactivateByPlayerID(ctx, playerID); err != nil {
		t.Fatalf("首次注销失败: %v", err)
	}
	first, err := repo.FindByPlayerID(ctx, playerID)
	if err != nil {
		t.Fatalf("复查账号失败: %v", err)
	}

	// 第二次注销应幂等返回 ErrNoRowsUpdated
	_, err = repo.DeactivateByPlayerID(ctx, playerID)
	if !errors.Is(err, model.ErrNoRowsUpdated) {
		t.Fatalf("重复注销应返回 ErrNoRowsUpdated, got %v", err)
	}
	// 已作废的 openId 不应被二次改写
	second, err := repo.FindByPlayerID(ctx, playerID)
	if err != nil {
		t.Fatalf("复查账号失败: %v", err)
	}
	if first.OpenID != second.OpenID {
		t.Fatalf("已注销 openId 被二次改写: %q -> %q", first.OpenID, second.OpenID)
	}
}
