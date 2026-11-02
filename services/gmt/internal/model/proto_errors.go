package model

// Proto动态数据管理业务错误码（3700-3799）
const (
	// ErrCodeProtoBadRequest 请求参数格式错误
	ErrCodeProtoBadRequest = 3700
	// ErrCodeProtoSyntaxInvalid Proto语法校验失败
	ErrCodeProtoSyntaxInvalid = 3701
	// ErrCodeProtoImportForbidden Proto文件包含import语句，禁止跨文件依赖
	ErrCodeProtoImportForbidden = 3702
	// ErrCodeProtoNoMessage Proto文件中未定义任何Message
	ErrCodeProtoNoMessage = 3703
	// ErrCodeProtoBreakingChange Proto覆盖上传存在破坏性变更
	ErrCodeProtoBreakingChange = 3704
	// ErrCodeProtoMD5Unchanged Proto源码MD5与当前一致，无需更新
	ErrCodeProtoMD5Unchanged = 3705
	// ErrCodeProtoNotFound Proto模板不存在
	ErrCodeProtoNotFound = 3706
	// ErrCodeProtoMessageNotFound 指定的Message在Proto中不存在
	ErrCodeProtoMessageNotFound = 3707
	// ErrCodeProtoPlayerDataNotFound 玩家分片数据不存在
	ErrCodeProtoPlayerDataNotFound = 3708
	// ErrCodeProtoDeserializeFailed 分片数据反序列化失败
	ErrCodeProtoDeserializeFailed = 3709
	// ErrCodeProtoSerializeFailed 分片数据序列化失败
	ErrCodeProtoSerializeFailed = 3710
	// ErrCodeProtoNameConflict 已存在全局模板，上传的protoName与现有不一致
	ErrCodeProtoNameConflict = 3711
	// ErrCodeProtoInternal Proto服务内部错误
	ErrCodeProtoInternal = 3750
)
