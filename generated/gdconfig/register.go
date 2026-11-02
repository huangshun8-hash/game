// 此文件由 genconfig 自动生成，请勿手动修改。
// 维护全局配置表 loader 注册表，各 conf_xxx.go 在 init() 中调用 Register 完成注册。
package gdconfig

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// loaderEntry 单张配置表的加载器条目
type loaderEntry struct {
	// tableName 配置表名称，用于错误提示
	tableName string
	// collName MongoDB 集合名
	collName string
	// apply 将全量行数据解码并原子写入 ConfigManager 对应字段
	apply func(rows []bson.Raw, m *ConfigManager) error
}

// registry 全局 loader 注册表，按注册顺序执行
var registry []loaderEntry

// Register 注册一张配置表的 loader，在各 conf_xxx.go 的 init() 中调用。
// tableName: 配置表名称（用于错误提示），MongoDB 集合名自动取其全小写形式
// apply:     接收全量行数据，完成解码后原子写入 ConfigManager 对应字段
func Register(
	tableName string,
	apply func(rows []bson.Raw, m *ConfigManager) error,
) {
	registry = append(registry, loaderEntry{
		tableName: tableName,
		collName:  strings.ToLower(tableName),
		apply:     apply,
	})
}

// loadEntry 执行单张配置表的完整加载流程：Find → 收集全量行 → 一次性 apply
// 全量收集完成后再调用 apply，确保 apply 内部可以安全地原子替换整张表指针。
func loadEntry(ctx context.Context, db *mongo.Database, entry loaderEntry, m *ConfigManager) error {
	cursor, err := db.Collection(entry.collName).Find(ctx, bson.D{})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	// 收集全量行，避免 apply 中途持有游标
	var rows []bson.Raw
	for cursor.Next(ctx) {
		// cursor.Current 在下次 Next 后会被复用，需要显式拷贝
		raw := make(bson.Raw, len(cursor.Current))
		copy(raw, cursor.Current)
		rows = append(rows, raw)
	}
	if err := cursor.Err(); err != nil {
		return err
	}

	return entry.apply(rows, m)
}

// RegisteredTables 返回已注册的所有配置表名列表（按注册顺序）
func RegisteredTables() []string {
	names := make([]string, 0, len(registry))
	for _, e := range registry {
		names = append(names, e.tableName)
	}
	return names
}
