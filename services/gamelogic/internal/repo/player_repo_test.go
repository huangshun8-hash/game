//go:build integration

package repo

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/shared/sharedmodel"
)

// getTestURI 读取 MongoDB 测试连接串，未设置时回退到本地默认地址
func getTestURI() string {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	return uri
}

// setupPlayerRepo 建立到独立测试库的连接并返回 PlayerRepo 与清理函数
func setupPlayerRepo(t *testing.T) (*PlayerRepo, func()) {
	t.Helper()
	ctx := context.Background()
	// 库名不能含 '.'（MongoDB 会判定为非法命名空间），用毫秒时间戳保证各用例库名唯一
	dbName := "gamelogic_repo_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)

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
	return NewPlayerRepo(client), cleanup
}

// TestDeactivateByPlayerID_Success 校验正常玩家注销：
// openId 被改写为 del_ 前缀释放原身份，状态置为已注销，且返回作废前的原始 openId。
func TestDeactivateByPlayerID_Success(t *testing.T) {
	repo, cleanup := setupPlayerRepo(t)
	defer cleanup()
	ctx := context.Background()

	const playerID int64 = 2001
	const originalOpenID = "guest_uuid_2001"
	if err := repo.InsertPlayer(ctx, &model.Player{
		PlayerID:  playerID,
		OpenID:    originalOpenID,
		LoginType: model.LoginTypeGuest,
		ServerID:  model.DefaultServerID,
		Status:    model.StatusNormal,
	}); err != nil {
		t.Fatalf("插入测试玩家失败: %v", err)
	}

	got, err := repo.DeactivateByPlayerID(ctx, playerID)
	if err != nil {
		t.Fatalf("注销失败: %v", err)
	}
	if got != originalOpenID {
		t.Fatalf("返回原始 openId = %q, 期望 %q", got, originalOpenID)
	}

	// 复查落库结果：openId 已作废、状态为已注销、Player 数据仍在
	player, err := repo.FindByPlayerID(ctx, playerID)
	if err != nil {
		t.Fatalf("复查玩家失败: %v", err)
	}
	if !sharedmodel.IsDeactivatedOpenID(player.OpenID) {
		t.Fatalf("落库 openId %q 未被作废", player.OpenID)
	}
	if player.Status != model.StatusDeactivated {
		t.Fatalf("落库 status = %d, 期望 %d", player.Status, model.StatusDeactivated)
	}
	// 原身份必须已释放：按原 openId 不可再查到该玩家
	if _, err := repo.FindByOpenID(ctx, originalOpenID); !errors.Is(err, mongox.ErrNotFound) {
		t.Fatalf("原 openId 仍可查到，身份未释放: err=%v", err)
	}
}

// TestDeactivateByPlayerID_PlayerNotFound 校验玩家不存在时返回 ErrNotFound，
// 由上层按幂等成功处理。
func TestDeactivateByPlayerID_PlayerNotFound(t *testing.T) {
	repo, cleanup := setupPlayerRepo(t)
	defer cleanup()
	ctx := context.Background()

	_, err := repo.DeactivateByPlayerID(ctx, 888888)
	if !errors.Is(err, mongox.ErrNotFound) {
		t.Fatalf("玩家不存在应返回 ErrNotFound, got %v", err)
	}
}

// TestDeactivateByPlayerID_AlreadyDeactivated 校验重复注销幂等：
// 第二次注销命中已作废记录，返回 ErrNoRowsUpdated 且不再改写 openId。
func TestDeactivateByPlayerID_AlreadyDeactivated(t *testing.T) {
	repo, cleanup := setupPlayerRepo(t)
	defer cleanup()
	ctx := context.Background()

	const playerID int64 = 2002
	if err := repo.InsertPlayer(ctx, &model.Player{
		PlayerID:  playerID,
		OpenID:    "guest_uuid_2002",
		LoginType: model.LoginTypeGuest,
		ServerID:  model.DefaultServerID,
		Status:    model.StatusNormal,
	}); err != nil {
		t.Fatalf("插入测试玩家失败: %v", err)
	}

	if _, err := repo.DeactivateByPlayerID(ctx, playerID); err != nil {
		t.Fatalf("首次注销失败: %v", err)
	}
	first, err := repo.FindByPlayerID(ctx, playerID)
	if err != nil {
		t.Fatalf("复查玩家失败: %v", err)
	}

	// 第二次注销应幂等返回 ErrNoRowsUpdated
	_, err = repo.DeactivateByPlayerID(ctx, playerID)
	if !errors.Is(err, model.ErrNoRowsUpdated) {
		t.Fatalf("重复注销应返回 ErrNoRowsUpdated, got %v", err)
	}
	// 已作废的 openId 不应被二次改写
	second, err := repo.FindByPlayerID(ctx, playerID)
	if err != nil {
		t.Fatalf("复查玩家失败: %v", err)
	}
	if first.OpenID != second.OpenID {
		t.Fatalf("已注销 openId 被二次改写: %q -> %q", first.OpenID, second.OpenID)
	}
}

// TestFindByAccounts_FiltersEmptyAndDeactivated 校验按账号批量查询的过滤语义：
// 空账号不参与查询；同一账号的多个角色全部返回；已注销玩家被排除（注销保留 account 原值）。
func TestFindByAccounts_FiltersEmptyAndDeactivated(t *testing.T) {
	repo, cleanup := setupPlayerRepo(t)
	defer cleanup()
	ctx := context.Background()

	const sharedAccount = "channel_account_3001"
	// 同一 account 在多个区服的两个有效角色
	if err := repo.InsertPlayer(ctx, &model.Player{
		PlayerID:  3001,
		OpenID:    "openid_3001",
		Account:   sharedAccount,
		LoginType: "channel1",
		ServerID:  1,
		Status:    model.StatusNormal,
	}); err != nil {
		t.Fatalf("插入测试玩家失败: %v", err)
	}
	if err := repo.InsertPlayer(ctx, &model.Player{
		PlayerID:  3002,
		OpenID:    "openid_3002",
		Account:   sharedAccount,
		LoginType: "channel1",
		ServerID:  2,
		Status:    model.StatusNormal,
	}); err != nil {
		t.Fatalf("插入测试玩家失败: %v", err)
	}
	// 已注销角色：account 原值保留，但不应被解析出来
	if err := repo.InsertPlayer(ctx, &model.Player{
		PlayerID:  3003,
		OpenID:    "openid_3003",
		Account:   sharedAccount,
		LoginType: "channel1",
		ServerID:  3,
		Status:    model.StatusNormal,
	}); err != nil {
		t.Fatalf("插入测试玩家失败: %v", err)
	}
	if _, err := repo.DeactivateByPlayerID(ctx, 3003); err != nil {
		t.Fatalf("注销玩家失败: %v", err)
	}
	// 游客玩家 account 为空，不应被解析出来
	if err := repo.InsertPlayer(ctx, &model.Player{
		PlayerID:  3004,
		OpenID:    "openid_3004",
		LoginType: model.LoginTypeGuest,
		ServerID:  model.DefaultServerID,
		Status:    model.StatusNormal,
	}); err != nil {
		t.Fatalf("插入测试玩家失败: %v", err)
	}

	players, err := repo.FindByAccounts(ctx, []string{sharedAccount, ""})
	if err != nil {
		t.Fatalf("按账号查询失败: %v", err)
	}

	got := make(map[int64]bool, len(players))
	for _, p := range players {
		got[p.PlayerID] = true
	}
	if len(got) != 2 || !got[3001] || !got[3002] {
		t.Fatalf("按账号查询结果 = %v, 期望仅含同一账号的两个有效角色 {3001,3002}", got)
	}
	if got[3003] {
		t.Fatalf("已注销玩家 3003 不应被解析出来")
	}
	if got[3004] {
		t.Fatalf("空账号玩家 3004 不应被解析出来")
	}

	// 空列表与全空账号输入均不应触发查询，直接返回 nil
	if players, err := repo.FindByAccounts(ctx, nil); err != nil || players != nil {
		t.Fatalf("空列表查询 = %v, err = %v, 期望 nil, nil", players, err)
	}
	if players, err := repo.FindByAccounts(ctx, []string{"", ""}); err != nil || players != nil {
		t.Fatalf("全空账号查询 = %v, err = %v, 期望 nil, nil", players, err)
	}
}

// TestFindExistingPlayerIDs_FiltersMissingAndDeactivated 校验排行榜幽灵过滤所用批量校验的语义：
// 仅返回"存在且未注销"的玩家ID，不存在与已注销的玩家均被排除；
// 返回结果只含 playerId（由投影保证），无需整文档字段。
func TestFindExistingPlayerIDs_FiltersMissingAndDeactivated(t *testing.T) {
	repo, cleanup := setupPlayerRepo(t)
	defer cleanup()
	ctx := context.Background()

	for _, playerID := range []int64{4001, 4002} {
		if err := repo.InsertPlayer(ctx, &model.Player{
			PlayerID:  playerID,
			OpenID:    "openid_" + strconv.FormatInt(playerID, 10),
			LoginType: model.LoginTypeGuest,
			ServerID:  model.DefaultServerID,
			Status:    model.StatusNormal,
		}); err != nil {
			t.Fatalf("插入测试玩家失败: %v", err)
		}
	}
	// 已注销玩家：Player 保留但不可再登录，不应出现在存在列表中
	if err := repo.InsertPlayer(ctx, &model.Player{
		PlayerID:  4003,
		OpenID:    "openid_4003",
		LoginType: model.LoginTypeGuest,
		ServerID:  model.DefaultServerID,
		Status:    model.StatusNormal,
	}); err != nil {
		t.Fatalf("插入测试玩家失败: %v", err)
	}
	if _, err := repo.DeactivateByPlayerID(ctx, 4003); err != nil {
		t.Fatalf("注销玩家失败: %v", err)
	}

	// 混合输入：存在的正常玩家、已注销玩家、不存在的玩家
	ids, err := repo.FindExistingPlayerIDs(ctx, []int64{4001, 4002, 4003, 4999})
	if err != nil {
		t.Fatalf("批量查询存在玩家失败: %v", err)
	}
	got := make(map[int64]bool, len(ids))
	for _, id := range ids {
		got[id] = true
	}
	if len(got) != 2 || !got[4001] || !got[4002] {
		t.Fatalf("存在玩家查询结果 = %v, 期望仅含 {4001,4002}", got)
	}
	if got[4003] {
		t.Fatalf("已注销玩家 4003 不应出现在存在列表中")
	}
	if got[4999] {
		t.Fatalf("不存在的玩家 4999 不应出现在存在列表中")
	}

	// 空列表不应触发查询，直接返回 nil
	if ids, err := repo.FindExistingPlayerIDs(ctx, nil); err != nil || ids != nil {
		t.Fatalf("空列表查询 = %v, err = %v, 期望 nil, nil", ids, err)
	}
}
