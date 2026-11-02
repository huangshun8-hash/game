package logic

import "go.mongodb.org/mongo-driver/v2/bson"

// parseObjectID 将十六进制字符串解析为 bson.ObjectID
// 解析失败时返回错误，供 logic 层内部使用
func parseObjectID(hex string) (bson.ObjectID, error) {
	return bson.ObjectIDFromHex(hex)
}
