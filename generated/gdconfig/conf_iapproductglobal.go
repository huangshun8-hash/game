// 此文件由 genconfig 自动生成，请勿手动修改。
// 配置表：IAPProductGlobal（版本 6ae9bf5d1f4128837ccf59be）
package gdconfig

import (
	"go.mongodb.org/mongo-driver/v2/bson"
)

func init() {
	Register("IAPProductGlobal", applyIAPProductGlobal)
}

// applyIAPProductGlobal 将全量行数据解码为 IAPProductGlobalTable 并原子写入 ConfigManager。
// 构建完整 table 后一次性替换，热更时读操作始终拿到完整快照，无并发风险。
func applyIAPProductGlobal(rows []bson.Raw, m *ConfigManager) error {
	table := &IAPProductGlobalTable{data: make(map[int32]*IAPProductGlobalConfig, len(rows))}
	for _, raw := range rows {
		var cfg IAPProductGlobalConfig
		if err := bson.Unmarshal(raw, &cfg); err != nil {
			return err
		}
		table.data[cfg.Id] = &cfg
	}
	m.IAPProductGlobal.Store(table)
	return nil
}

// IAPProductGlobalConfig IAPProductGlobal 配置表数据结构
type IAPProductGlobalConfig struct {
	Id     int32   `bson:"_id"`    // 编号
	PayID  string  `bson:"payID"`  // 商品ID
	Value  float64 `bson:"value"`  // 展示价格
	Symbol string  `bson:"symbol"` // 货币缩写
	Corre1 string  `bson:"corre1"` // ios内购ID
	Corre2 string  `bson:"corre2"` // google内购ID
}

// IAPProductGlobalTable IAPProductGlobal 配置表内存访问层
type IAPProductGlobalTable struct {
	data map[int32]*IAPProductGlobalConfig
}

// Get 按 ID 获取单条配置，ID 不存在时返回 nil。
// 返回的是内部存储的只读引用，禁止修改其字段（修改会污染共享配置并造成并发数据竞争）。
func (t *IAPProductGlobalTable) Get(id int32) *IAPProductGlobalConfig {
	if t == nil || t.data == nil {
		return nil
	}
	return t.data[id]
}

// GetAll 获取所有配置数据的浅拷贝：新建 map 复制键值，调用方对返回 map 的增删不会影响内部数据。
// 注意：map 中的值仍为只读配置结构体指针，禁止修改其字段（修改会污染共享配置并造成并发数据竞争）。
func (t *IAPProductGlobalTable) GetAll() map[int32]*IAPProductGlobalConfig {
	if t == nil || t.data == nil {
		return nil
	}
	result := make(map[int32]*IAPProductGlobalConfig, len(t.data))
	for k, v := range t.data {
		result[k] = v
	}
	return result
}

// Count 返回配置条数
func (t *IAPProductGlobalTable) Count() int {
	if t == nil || t.data == nil {
		return 0
	}
	return len(t.data)
}
