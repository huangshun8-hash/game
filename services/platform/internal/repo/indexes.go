package repo

import (
	"context"

	"kemiluo/pkg/mongox"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// EnsureIndexes 确保平台服务所需的数据库索引存在
func EnsureIndexes(ctx context.Context, client *mongox.Client) error {
	return client.EnsureIndexes(ctx, []mongox.IndexDef{
		// accounts 集合：playerId 唯一索引
		{
			Collection: collectionAccounts,
			Keys:       bson.D{{Key: "playerId", Value: 1}},
			Unique:     true,
			Name:       "ukPlayerId",
		},
		// accounts 集合：openId 部分唯一索引（仅对非空值强制唯一）
		// 游客账号初始 openId 为 UUID（非空），升级为正式账号后改为三方 openID
		// partial filter 确保未来如有 openId 为空的记录不受唯一约束影响
		{
			Collection:              collectionAccounts,
			Keys:                    bson.D{{Key: "openId", Value: 1}},
			Unique:                  true,
			Name:                    "ukOpenId",
			PartialFilterExpression: bson.D{{Key: "openId", Value: bson.D{{Key: "$gt", Value: ""}}}},
		},

		// payment_orders 集合：orderId 唯一索引（核心幂等键）
		{
			Collection: collectionPaymentOrders,
			Keys:       bson.D{{Key: "orderId", Value: 1}},
			Unique:     true,
			Name:       "ukOrderId",
		},
		// payment_orders 集合：thirdTransactionId 唯一索引（三方订单号防重放）
		// 原索引为 {channel, thirdTransactionId} 复合唯一索引，现收敛为 thirdTransactionId 单字段唯一：
		//   1) channel 为前缀时无法支撑「仅按三方订单号查询」（非索引前缀），单字段索引可直接支撑
		//      GMT 按订单号查询中 thirdTransactionId 的 $or 分支，故无需再单建普通索引；
		//   2) channel（apple/google）现由同一运营方接入、已不具备区分意义，收敛后语义等价；
		//   3) 唯一性仍是充值回调 / GMT 补单防重复入账的最终兜底（并发下重复渠道交易号返回「重复的订单号」）。
		// 部分索引仅对非空 thirdTransactionId 强制唯一，避免未支付订单（该字段为空串）相互冲突。
		{
			Collection:              collectionPaymentOrders,
			Keys:                    bson.D{{Key: "thirdTransactionId", Value: 1}},
			Unique:                  true,
			Name:                    "ukThirdTxId",
			PartialFilterExpression: bson.D{{Key: "thirdTransactionId", Value: bson.D{{Key: "$gt", Value: ""}}}},
		},
		// payment_orders 集合：playerId 索引（按玩家查账）
		{
			Collection: collectionPaymentOrders,
			Keys:       bson.D{{Key: "playerId", Value: 1}},
			Name:       "idxPlayerId",
		},
		// payment_orders 集合：paymentStatus + notifyStatus 复合索引（定时重通知扫描）
		{
			Collection: collectionPaymentOrders,
			Keys:       bson.D{{Key: "paymentStatus", Value: 1}, {Key: "notifyStatus", Value: 1}},
			Name:       "idxPaymentNotifyStatus",
		},
		// payment_orders 集合：createTime 索引（按时间范围对账）
		{
			Collection: collectionPaymentOrders,
			Keys:       bson.D{{Key: "createTime", Value: 1}},
			Name:       "idxCreateTime",
		},

		// pay_whitelist 集合：openId 唯一索引（核心幂等键，一个账号只允许一条白名单）
		{
			Collection:              collectionPayWhitelist,
			Keys:                    bson.D{{Key: "openId", Value: 1}},
			Unique:                  true,
			Name:                    "ukOpenId",
			PartialFilterExpression: bson.D{{Key: "openId", Value: bson.D{{Key: "$gt", Value: ""}}}},
		},
		// pay_whitelist 集合：createdAt 索引（分页排序）
		{
			Collection: collectionPayWhitelist,
			Keys:       bson.D{{Key: "createdAt", Value: 1}},
			Name:       "idxCreatedAt",
		},
	})
}
