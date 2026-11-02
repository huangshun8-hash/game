package sharedmodel

import (
	"strconv"
	"strings"
	"time"
)

// DeactivatedOpenIDPrefix 已注销账号/玩家 openId 的作废前缀。
// 注销时将原 openId 改写为 "del_<unixMilli>_<原openId>"，释放原登录身份并避免二次注销的唯一索引冲突。
// platform 的 Account 与 gamelogic 的 Player 共用同一套作废约定，故统一在此声明，避免字段漂移。
const DeactivatedOpenIDPrefix = "del_"

// IsDeactivatedOpenID 判断 openId 是否已被作废（以 del_ 前缀开头）。
// 入参 openID 为待判定的 openId；返回 true 表示该身份已注销。
func IsDeactivatedOpenID(openID string) bool {
	return strings.HasPrefix(openID, DeactivatedOpenIDPrefix)
}

// BuildDeactivatedOpenID 构造作废后的 openId："del_<unixMilli>_<原openId>"。
// 追加毫秒时间戳避免同一身份多次注销（注销→重登→再注销）时撞上 openId 唯一索引。
// 入参 originalOpenID 为作废前的原始 openId；返回作废后的新 openId。
func BuildDeactivatedOpenID(originalOpenID string) string {
	return DeactivatedOpenIDPrefix + strconv.FormatInt(time.Now().UnixMilli(), 10) + "_" + originalOpenID
}
