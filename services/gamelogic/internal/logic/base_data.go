package logic

import (
	"context"

	"kemiluo/pb/cspb"
	"kemiluo/services/gamelogic/internal/cache"
	"kemiluo/services/gamelogic/internal/model"
)

// newBase 返回一个新的 Base 实例，供存档解码复用。
func newBase() *cspb.Base { return &cspb.Base{} }

// loadBase 读取玩家 base 存档并解码为 cspb.Base；读取器未就绪或玩家无存档时返回 nil。
func loadBase(ctx context.Context, reader SaveReader, playerID int64) (*cspb.Base, error) {
	return LoadSave(ctx, reader, playerID, cache.BaseKey, newBase)
}

// loadBases 批量读取多个玩家的 base 存档并解码为 cspb.Base；无存档玩家不出现在返回 map 中。
func loadBases(ctx context.Context, reader SaveReader, playerIDs []int64) (map[int64]*cspb.Base, error) {
	return LoadSaves(ctx, reader, playerIDs, cache.BaseKey, newBase)
}

// playerNickname 返回玩家昵称；未设置昵称时回退默认昵称。
func playerNickname(playerID int64, base *cspb.Base) string {
	if base != nil && base.GetNickname() != "" {
		return base.GetNickname()
	}
	return model.DefaultNickname(playerID)
}
