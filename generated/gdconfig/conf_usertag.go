// 此文件由 genconfig 自动生成，请勿手动修改。
// 配置表：Usertag（版本 6a59dc91a8da973d80d4eae9）
package gdconfig

import (
	"go.mongodb.org/mongo-driver/v2/bson"
)

func init() {
	Register("Usertag", applyUsertag)
}

// applyUsertag 将全量行数据解码为 UsertagTable 并原子写入 ConfigManager。
// 构建完整 table 后一次性替换，热更时读操作始终拿到完整快照，无并发风险。
func applyUsertag(rows []bson.Raw, m *ConfigManager) error {
	table := &UsertagTable{data: make(map[int32]*UsertagConfig, len(rows))}
	for _, raw := range rows {
		var cfg UsertagConfig
		if err := bson.Unmarshal(raw, &cfg); err != nil {
			return err
		}
		table.data[cfg.Id] = &cfg
	}
	m.Usertag.Store(table)
	return nil
}

// UsertagConfig Usertag 配置表数据结构
type UsertagConfig struct {
	Id        int32   `bson:"_id"`       // 编号
	Enum      string  `bson:"enum"`      // 礼包/活动枚举值
	Tag       string  `bson:"tag"`       // 返回的分组tag
	Channel   string  `bson:"channel"`   // 渠道
	AbGroup   []int32 `bson:"abGroup"`   // AB分组
	SLv       int32   `bson:"sLv"`       // 玩家等级下限
	ELv       int32   `bson:"eLv"`       // 玩家等级 上限
	SLastPay  int32   `bson:"sLastPay"`  // 最近1笔付费,分
	ELastPay  int32   `bson:"eLastPay"`  // 最近1笔付费,分
	SLast4Pay int32   `bson:"sLast4Pay"` // 最近4笔付费,分
	ELast4Pay int32   `bson:"eLast4Pay"` // 最近4笔付费,分
	SMonthPay int32   `bson:"sMonthPay"` // 最近30天付费,分
	EMonthPay int32   `bson:"eMonthPay"` // 最近30天付费,分
	SPay      int32   `bson:"sPay"`      // 累积付费 下限,分
	EPay      int32   `bson:"ePay"`      // 累积付费 上限,分
	Dec       string  `bson:"dec"`       // 中文备注/描述
}

// UsertagTable Usertag 配置表内存访问层
type UsertagTable struct {
	data map[int32]*UsertagConfig
}

// Get 按 ID 获取单条配置，ID 不存在时返回 nil
func (t *UsertagTable) Get(id int32) *UsertagConfig {
	if t == nil || t.data == nil {
		return nil
	}
	return t.data[id]
}

// GetAll 获取所有配置数据（map 副本引用，不可修改）
func (t *UsertagTable) GetAll() map[int32]*UsertagConfig {
	if t == nil {
		return nil
	}
	return t.data
}

// Count 返回配置条数
func (t *UsertagTable) Count() int {
	if t == nil || t.data == nil {
		return 0
	}
	return len(t.data)
}
