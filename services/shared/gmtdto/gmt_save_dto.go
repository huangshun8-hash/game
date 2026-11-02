package gmtdto

// GmtSaveDownloadReq GMT 查询玩家存档请求
type GmtSaveDownloadReq struct {
	// PlayerID 玩家唯一ID（必填）
	PlayerID int64 `json:"playerId"`
	// Keys 存档分类标识列表（即 MessageName），为空时返回该玩家所有存档
	Keys []string `json:"keys,omitempty"`
}

// GmtSaveDownloadResp GMT 查询玩家存档响应
type GmtSaveDownloadResp struct {
	// Items 各 key 对应的存档数据
	Items []*GmtSaveDownloadItem `json:"items"`
}

// GmtSaveDownloadItem 单个存档数据项
type GmtSaveDownloadItem struct {
	// Key 存档分类标识（即 MessageName）
	Key string `json:"key"`
	// Version 当前版本号，0表示无存档
	Version int64 `json:"version"`
	// Content 存档内容（明文 protobuf 线格式字节的 base64 编码；gamelogic 内部已完成格式转换，GMT 无需感知客户端存档格式）
	Content string `json:"content"`
	// UpdateTime 存档最后更新时间（毫秒时间戳），0表示无存档
	UpdateTime int64 `json:"updateTime"`
}

// GmtSaveUploadReq GMT 批量保存玩家存档请求
type GmtSaveUploadReq struct {
	// PlayerID 玩家唯一ID（必填）
	PlayerID int64 `json:"playerId"`
	// Items 待保存的存档数据列表
	Items []*GmtSaveUploadItem `json:"items"`
}

// GmtSaveUploadItem 单个待保存的存档数据项
type GmtSaveUploadItem struct {
	// Key 存档分类标识（即 MessageName）
	Key string `json:"key"`
	// Content 存档内容（明文 protobuf 线格式字节的 base64 编码；gamelogic 内部使用玩家存档秘钥加密后落库，GMT 无需感知加密格式）
	Content string `json:"content"`
}

// GmtSaveUploadResp GMT 批量保存玩家存档响应
type GmtSaveUploadResp struct {
	// UpdateTime 操作完成时间（毫秒时间戳）
	UpdateTime int64 `json:"updateTime"`
}
