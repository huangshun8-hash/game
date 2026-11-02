package model

import "errors"

// 配置管理业务错误码（3600-3699）
const (
	// ErrCodeConfigBadRequest 请求参数格式错误
	ErrCodeConfigBadRequest = 3600
	// ErrCodeConfigSchemaInvalid Schema校验失败
	ErrCodeConfigSchemaInvalid = 3601
	// ErrCodeConfigDataInvalid 数据深度校验失败
	ErrCodeConfigDataInvalid = 3602
	// ErrCodeConfigNothingToPublish 任务内没有已上传的配置表
	ErrCodeConfigNothingToPublish = 3603
	// ErrCodeConfigNotFound 配置资源不存在
	ErrCodeConfigNotFound = 3604
	// ErrCodeConfigReleaseStatusConflict 任务状态不允许此操作
	ErrCodeConfigReleaseStatusConflict = 3605
	// ErrCodeConfigActiveReleaseExists 已存在进行中的发布任务，同一时刻只允许一个未完成任务
	ErrCodeConfigActiveReleaseExists = 3606
	// ErrCodeConfigInternal 配置服务内部错误
	ErrCodeConfigInternal = 3650
)

var (
	// ErrConfigSchemaInvalid Schema结构不合法
	ErrConfigSchemaInvalid = errors.New("config schema invalid")
	// ErrConfigDataInvalid 配置数据校验失败
	ErrConfigDataInvalid = errors.New("config data invalid")
	// ErrConfigIDDuplicate 配置数据中存在重复ID
	ErrConfigIDDuplicate = errors.New("config data has duplicate IDs")
	// ErrConfigIDMissing 配置数据缺少 id 字段
	ErrConfigIDMissing = errors.New("config data missing 'id' field")
	// ErrConfigNothingToPublish 任务内没有可发布的内容
	ErrConfigNothingToPublish = errors.New("no tables uploaded in this release")
	// ErrConfigReleaseNotFound 发布任务不存在
	ErrConfigReleaseNotFound = errors.New("config release not found")
	// ErrConfigReleaseTerminated 任务已处于终态，不可再操作
	ErrConfigReleaseTerminated = errors.New("config release is already terminated")
	// ErrConfigCannotReview 任务当前状态不允许发起验收
	ErrConfigCannotReview = errors.New("config release cannot start review in current status")
	// ErrConfigCannotPublish 任务未完成验收，不允许发布
	ErrConfigCannotPublish = errors.New("config release must be reviewed before publishing")
	// ErrConfigActiveReleaseExists 已存在进行中的发布任务，同一时刻只允许一个未完成任务
	ErrConfigActiveReleaseExists = errors.New("an active release already exists, please complete or cancel it first")
)
