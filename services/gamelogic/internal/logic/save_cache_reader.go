package logic

import (
	"context"
	"encoding/base64"
	"fmt"

	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/pkg/savex"
	"kemiluo/services/gamelogic/internal/cache"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/gamelogic/internal/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/protobuf/proto"
)

// SaveReader 玩家存档读取接口，按存档 key 读取缓存，未命中时回源数据库。
type SaveReader interface {
	// Get 返回玩家指定 key 的存档条目；无存档时返回 nil。
	Get(ctx context.Context, playerID int64, key string) (*cache.Item, error)
	// GetMany 批量返回多个玩家指定 key 的存档条目；无存档玩家不出现在结果中。
	GetMany(ctx context.Context, playerIDs []int64, key string) (map[int64]*cache.Item, error)
}

// SaveCacheReader 存档缓存读取器，封装缓存读取与数据库回源。
type SaveCacheReader struct {
	mongoSvc    *mongox.Service
	playerCache *cache.Manager
}

// NewSaveCacheReader 创建存档缓存读取器。
func NewSaveCacheReader(mongoSvc *mongox.Service, redisSvc *redisx.Service) *SaveCacheReader {
	var client *redisx.Client
	if redisSvc != nil {
		client = redisSvc.Client()
	}
	return &SaveCacheReader{mongoSvc: mongoSvc, playerCache: cache.New(client)}
}

// Get 返回玩家指定 key 的存档条目。
func (r *SaveCacheReader) Get(ctx context.Context, playerID int64, key string) (*cache.Item, error) {
	items, err := r.GetMany(ctx, []int64{playerID}, key)
	if err != nil {
		return nil, err
	}
	return items[playerID], nil
}

// GetMany 批量读取多个玩家指定 key 的存档条目。
func (r *SaveCacheReader) GetMany(ctx context.Context, playerIDs []int64, key string) (map[int64]*cache.Item, error) {
	items := make(map[int64]*cache.Item, len(playerIDs))
	ids := uniqueInt64(playerIDs)
	if len(ids) == 0 {
		return items, nil
	}

	resolved := make(map[int64]struct{}, len(ids))
	cached, err := r.playerCache.GetMany(ctx, ids, key)
	if err == nil {
		for _, id := range ids {
			item, ok := cached[id]
			if !ok {
				continue
			}
			resolved[id] = struct{}{}
			if !item.NotFound {
				items[id] = item
			}
		}
	}

	misses := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, done := resolved[id]; !done {
			misses = append(misses, id)
		}
	}
	if len(misses) == 0 {
		return items, nil
	}
	if err := r.fillItemsFromDB(ctx, misses, key, items); err != nil {
		return items, err
	}
	return items, nil
}

// fillItemsFromDB 批量从数据库加载最新存档并回填缓存。
func (r *SaveCacheReader) fillItemsFromDB(ctx context.Context, playerIDs []int64, key string, items map[int64]*cache.Item) error {
	headRepo, snapRepo := r.repos()
	filled := make(map[int64]struct{}, len(playerIDs))

	heads, err := headRepo.FindManyByPlayerIDs(ctx, playerIDs, key)
	if err != nil {
		return fmt.Errorf("批量查询存档头失败: %w", err)
	}
	if len(heads) > 0 {
		snapshotIDs := make([]bson.ObjectID, 0, len(heads))
		for _, head := range heads {
			if !head.LatestSnapshotID.IsZero() {
				snapshotIDs = append(snapshotIDs, head.LatestSnapshotID)
			}
		}
		snapshots, err := snapRepo.FindBySnapshotIDs(ctx, snapshotIDs)
		if err != nil {
			return fmt.Errorf("批量查询存档快照失败: %w", err)
		}
		snapshotByID := make(map[bson.ObjectID]model.SaveSnapshot, len(snapshots))
		for _, snapshot := range snapshots {
			snapshotByID[snapshot.ID] = snapshot
		}
		for _, head := range heads {
			snapshot, ok := snapshotByID[head.LatestSnapshotID]
			if !ok {
				continue
			}
			items[head.PlayerID] = &cache.Item{Key: key, Version: snapshot.Version, Sign: snapshot.Sign, Content: snapshot.Content}
			filled[head.PlayerID] = struct{}{}
			r.backfill(ctx, head.PlayerID, key, snapshot.Version, snapshot.Sign, snapshot.Content)
		}
	}

	for _, id := range playerIDs {
		if _, ok := filled[id]; !ok {
			_ = r.playerCache.SetNotFound(ctx, id, key)
		}
	}
	return nil
}

// uniqueInt64 去重玩家ID并保持原始顺序。
func uniqueInt64(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

// backfill 回填指定 key 的存档缓存；失败时失效旧缓存避免陈旧读取。
func (r *SaveCacheReader) backfill(ctx context.Context, playerID int64, key string, version int64, sign string, content string) {
	if err := r.playerCache.Set(ctx, playerID, key, version, sign, content); err != nil {
		_ = r.playerCache.Del(ctx, playerID, key)
	}
}

// decodeSaveContent 将 base64 编码的 gzip 存档内容解码为指定 protobuf 消息。
func decodeSaveContent[M proto.Message](content string, newMsg func() M) (M, error) {
	var zero M
	if content == "" {
		return zero, nil
	}
	wire, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return zero, fmt.Errorf("解码存档失败: %w", err)
	}
	msg, err := savex.Decode(wire, newMsg)
	if err != nil {
		return zero, fmt.Errorf("解压存档失败: %w", err)
	}
	return msg, nil
}

// LoadSave 读取玩家指定 key 的存档并解码为指定 protobuf 消息。
func LoadSave[M proto.Message](ctx context.Context, reader SaveReader, playerID int64, key string, newMsg func() M) (M, error) {
	var zero M
	if reader == nil {
		return zero, nil
	}
	item, err := reader.Get(ctx, playerID, key)
	if err != nil || item == nil || item.NotFound {
		return zero, err
	}
	return decodeSaveContent(item.Content, newMsg)
}

// LoadSaves 批量读取多个玩家指定 key 的存档并解码为指定 protobuf 消息。
func LoadSaves[M proto.Message](ctx context.Context, reader SaveReader, playerIDs []int64, key string, newMsg func() M) (map[int64]M, error) {
	if reader == nil {
		return nil, nil
	}
	items, err := reader.GetMany(ctx, playerIDs, key)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]M, len(items))
	for id, item := range items {
		if item == nil || item.NotFound {
			continue
		}
		message, err := decodeSaveContent(item.Content, newMsg)
		if err != nil {
			continue
		}
		result[id] = message
	}
	return result, nil
}

// repos 返回存档头与快照仓库。
func (r *SaveCacheReader) repos() (*repo.SaveHeadRepo, *repo.SaveSnapshotRepo) {
	client := r.mongoSvc.Client()
	return repo.NewSaveHeadRepo(client), repo.NewSaveSnapshotRepo(client)
}
