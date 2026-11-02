package repo

// 逻辑服务集合名称常量
const (
	// collectionPlayers 玩家集合
	collectionPlayers = "players"
	// collectionSaveHeads 存档头集合（每玩家唯一一条）
	collectionSaveHeads = "save_heads"
	// collectionSaveSnapshots 存档快照集合（保留最近5条历史版本）
	collectionSaveSnapshots = "save_snapshots"
	// collectionMailTemplates 系统邮件模板集合
	collectionMailTemplates = "mail_templates"
	// collectionPlayerMails 玩家邮件集合
	collectionPlayerMails = "player_mails"
	// collectionPayOrders 业务订单主表
	collectionPayOrders = "pay_orders"
	// collectionBanRecords 封禁/解封操作记录
	collectionBanRecords = "ban_records"
	// collectionUserFriend 好友关系集合（双向冗余存储）
	collectionUserFriend = "user_friend"
	// collectionFriendApply 好友申请集合（单向申请记录，处理（同意/拒绝/撤销）后即删除，不保存历史状态）
	collectionFriendApply = "friend_apply"
)
