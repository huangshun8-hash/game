package gmtdto

// 本文件定义 GameLogic 侧业务订单的 GMT 查询 DTO。
// 与 Platform 侧的 GmtOrderItem（渠道支付视角）不同，
// GameLogic 订单关注业务状态机（WAIT_PAY/PAID/DELIVERED/CLOSED）与发奖情况，
// 供运营后台展示订单的“游戏侧履约”全貌。

// GmtQueryGameOrderReq GMT 查询 GameLogic 业务订单请求
// 支持按 orderId 精确查询，或按 playerId / 业务状态 / 渠道 / 时间范围分页查询
type GmtQueryGameOrderReq struct {
	// OrderID 业务订单号（精确匹配，非空时忽略其他筛选条件）
	OrderID string `json:"orderId" form:"orderId"`
	// PlayerID 玩家唯一标识（0 表示不过滤）
	PlayerID int64 `json:"playerId" form:"playerId"`
	// Status 业务状态过滤：WAIT_PAY / PAID / DELIVERED / CLOSED（空表示不过滤）
	Status string `json:"status" form:"status"`
	// Channel 支付渠道过滤：apple / google（空表示不过滤）
	Channel string `json:"channel" form:"channel"`
	// StartTime 创建时间下限（Unix 毫秒，0 表示不过滤）
	StartTime int64 `json:"startTime" form:"startTime"`
	// EndTime 创建时间上限（Unix 毫秒，0 表示不过滤）
	EndTime int64 `json:"endTime" form:"endTime"`
	// Page 页码，从 1 开始（默认 1）
	Page int64 `json:"page" form:"page"`
	// PageSize 每页条数（默认 20，最大 100）
	PageSize int64 `json:"pageSize" form:"pageSize"`
}

// GmtGameOrderItem GMT 视角的 GameLogic 业务订单数据（供运营后台展示，不含 bson 标签）
type GmtGameOrderItem struct {
	// OrderID 业务订单号
	OrderID string `json:"orderId"`
	// PlayerID 玩家唯一标识
	PlayerID int64 `json:"playerId"`
	// ProductID 商品ID
	ProductID string `json:"productId"`
	// Channel 支付渠道：apple / google
	Channel string `json:"channel"`
	// Status 业务状态：WAIT_PAY 待支付 / PAID 已支付待领取 / DELIVERED 已发奖 / CLOSED 已关闭
	Status string `json:"status"`
	// AwardTag 奖项标识，客户端创建订单时传入（原样回传）
	AwardTag string `json:"awardTag"`
	// Award 奖励内容，客户端创建订单时传入（原样回传）
	Award string `json:"award"`
	// NotifyID 最后一次收到的 Platform 通知唯一ID
	NotifyID string `json:"notifyId"`
	// Amount 支付金额（分），由 Platform 支付成功回调写入，0 表示未支付
	Amount int64 `json:"amount"`
	// PaidAt 支付成功时间（Unix 毫秒），0 表示未支付
	PaidAt int64 `json:"paidAt"`
	// DeliveredAt 发奖成功时间（Unix 毫秒），0 表示未发奖
	DeliveredAt int64 `json:"deliveredAt"`
	// ExpireTime 订单过期时间（Unix 毫秒）
	ExpireTime int64 `json:"expireTime"`
	// CreateTime 订单创建时间（Unix 毫秒）
	CreateTime int64 `json:"createTime"`
	// UpdateTime 订单最后更新时间（Unix 毫秒）
	UpdateTime int64 `json:"updateTime"`
}

// GmtQueryGameOrderResp GMT 业务订单查询响应
type GmtQueryGameOrderResp struct {
	// Items 订单列表
	Items []*GmtGameOrderItem `json:"items"`
	// Total 满足条件的订单总数
	Total int64 `json:"total"`
	// Page 当前页码
	Page int64 `json:"page"`
	// PageSize 每页条数
	PageSize int64 `json:"pageSize"`
	// TotalPages 总页数
	TotalPages int64 `json:"totalPages"`
}
