package logic

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kemiluo/pkg/async"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/gmt/internal/repo"
	"kemiluo/services/shared/gmtdto"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// descriptorCacheEntry 描述符缓存条目
type descriptorCacheEntry struct {
	desc       protoreflect.MessageDescriptor
	accessTime atomic.Int64
}

// ProtoLogic Proto动态数据管理业务逻辑
type ProtoLogic struct {
	mongoSvc     *mongox.Service
	asyncSvc     *async.Service
	gamelogicSvc *httpcli.Service
	// cache 描述符缓存，key格式：protoName:md5:messageName
	// 缓存 key 中带入 schema 的 MD5，确保 Schema 更新后旧描述符不会因并发写入竞态而被误用
	cache sync.Map
	// maxCacheSize LRU缓存最大条目数
	maxCacheSize int
	// evictMu 保护缓存淘汰过程，避免并发淘汰导致的计数漂移与重复删除
	evictMu sync.Mutex
	// compileGroup 合并同一 Schema 的并发编译请求，避免缓存击穿时的重复编译
	compileGroup singleflight.Group
}

// NewProtoLogic 创建Proto业务逻辑实例
func NewProtoLogic(mongoSvc *mongox.Service, asyncSvc *async.Service, gamelogicSvc *httpcli.Service) *ProtoLogic {
	return &ProtoLogic{
		mongoSvc:     mongoSvc,
		asyncSvc:     asyncSvc,
		gamelogicSvc: gamelogicSvc,
		maxCacheSize: 500,
	}
}

// Exists 查询全局Proto模板是否存在
// 返回模板基本信息（名称、Message列表、MD5、更新时间），不存在时返回 exists=false
func (l *ProtoLogic) Exists(ctx context.Context) (*dto.ProtoExistsResp, error) {
	schemaRepo := repo.NewProtoSchemaRepo(l.mongoSvc.Client())
	schema, err := schemaRepo.FindLatest(ctx)
	if err == mongox.ErrNotFound {
		return &dto.ProtoExistsResp{Exists: false}, nil
	}
	if err != nil {
		return nil, err
	}

	// 从源码实时派生分片 key，兼容历史模板中保留的旧顶层 Message 元数据。
	messageList, _, err := parseProto(schema.ProtoName, schema.Content)
	if err != nil {
		return nil, fmt.Errorf("parse stored proto schema: %w", err)
	}

	return &dto.ProtoExistsResp{
		Exists:      true,
		ProtoName:   schema.ProtoName,
		MessageList: messageList,
		MD5:         schema.MD5,
		UpdateTime:  schema.UpdateTime,
	}, nil
}

// Upload 上传并覆盖Proto文件
// 执行多层校验：语法合法性、Import禁止、全局唯一性、Schema兼容性差分
func (l *ProtoLogic) Upload(ctx context.Context, protoName, content, requestID, operator string) (*dto.UploadProtoResp, error) {
	// 1. 语法校验并提取Message列表与import依赖（基于AST/描述符，避免字符串扫描的漏检）
	messageList, imports, err := parseProto(protoName, content)
	if err != nil {
		return nil, fmt.Errorf("SYNTAX_INVALID:%s", err.Error())
	}

	// 2. 校验import语句：全局唯一模板禁止跨文件依赖
	if len(imports) > 0 {
		return nil, fmt.Errorf("IMPORT_FORBIDDEN")
	}

	if len(messageList) == 0 {
		return nil, fmt.Errorf("NO_MESSAGE")
	}

	// 3. 计算MD5
	hash := md5.Sum([]byte(content))
	contentMD5 := hex.EncodeToString(hash[:])

	schemaRepo := repo.NewProtoSchemaRepo(l.mongoSvc.Client())

	// 4. 查询现有全局模板（系统设计为全局仅一份Proto模板）
	existing, err := schemaRepo.FindLatest(ctx)
	if err != nil && err != mongox.ErrNotFound {
		return nil, fmt.Errorf("INTERNAL:%s", err.Error())
	}

	// 5. 全局唯一性校验：已存在模板时，protoName 必须与现有一致，禁止创建第二份模板
	//    否则 FindLatest 会在多份模板间漂移，导致兼容性/去重校验形同虚设
	if existing != nil && existing.ProtoName != protoName {
		return nil, fmt.Errorf("PROTO_NAME_CONFLICT:%s", existing.ProtoName)
	}

	// 6. MD5去重校验
	if existing != nil && existing.MD5 == contentMD5 {
		return nil, fmt.Errorf("MD5_UNCHANGED")
	}

	// 7. Schema兼容性差分校验（仅在已有旧模板时执行）
	if existing != nil {
		breakingChanges := l.checkSchemaCompatibility(ctx, existing, content, messageList)
		if len(breakingChanges) > 0 {
			return nil, fmt.Errorf("BREAKING_CHANGE:%s", strings.Join(breakingChanges, "; "))
		}
	}

	// 8. 写入数据库
	doc := model.NewProtoSchema(protoName, content, contentMD5, messageList)
	if err := schemaRepo.Upsert(ctx, doc); err != nil {
		return nil, fmt.Errorf("INTERNAL:%s", err.Error())
	}

	// 9. 清空该Proto下所有Message缓存
	l.invalidateCache(protoName)

	// 10. 异步记录操作日志
	l.logOperationAsync(operator, "ProtoSchema", "Upload", "POST", "/api/proto/upload", requestID, map[string]any{
		"protoName":   protoName,
		"messageList": messageList,
		"md5":         contentMD5,
	})

	logx.Info("Proto模板上传覆盖成功",
		zap.String("requestId", requestID),
		zap.String("protoName", protoName),
		zap.Strings("messageList", messageList),
		zap.String("md5", contentMD5),
	)

	return &dto.UploadProtoResp{
		ProtoName:   protoName,
		MessageList: messageList,
		MD5:         contentMD5,
		UpdateTime:  doc.UpdateTime,
	}, nil
}

// GetPlayerData 查询玩家分片数据
// 通过调用 gamelogic /gmt/save/download 接口获取存档原始数据，再动态解析为JSON
// messageName 非空时返回单个分片，为空时返回该玩家所有分片数据
func (l *ProtoLogic) GetPlayerData(ctx context.Context, req *dto.GetPlayerDataReq, requestID string) (*dto.GetPlayerDataResp, error) {
	// 获取全局唯一Proto模板
	schema, err := l.getGlobalSchema(ctx)
	if err != nil {
		return nil, err
	}

	// 构建调用 gamelogic 的请求
	downloadReq := &gmtdto.GmtSaveDownloadReq{
		PlayerID: req.PlayerID,
	}
	if req.MessageName != "" {
		downloadReq.Keys = []string{req.MessageName}
	}

	// 调用 gamelogic 获取原始存档数据
	downloadResp, err := l.callGamelogicDownload(ctx, downloadReq, requestID)
	if err != nil {
		return nil, fmt.Errorf("INTERNAL:%s", err.Error())
	}

	// 解析各分片数据
	items := make([]*dto.PlayerDataItem, 0, len(downloadResp.Items))
	for _, saveItem := range downloadResp.Items {
		if saveItem.Content == "" {
			// 无存档数据，跳过
			continue
		}
		item, err := l.decodeSaveContent(ctx, schema, saveItem.Key, saveItem.Content)
		if err != nil {
			if req.MessageName != "" {
				// 指定单个分片时，解析失败直接报错
				return nil, err
			}
			// 批量查询时，单个分片解析失败不阻断
			logx.Warn("解析玩家分片数据失败",
				zap.String("requestId", requestID),
				zap.Int64("playerId", req.PlayerID),
				zap.String("messageName", saveItem.Key),
				zap.Error(err),
			)
			continue
		}
		// 回填分片最后修改时间（毫秒时间戳）
		item.UpdateTime = saveItem.UpdateTime
		items = append(items, item)
	}

	if req.MessageName != "" && len(items) == 0 {
		return nil, fmt.Errorf("PLAYER_DATA_NOT_FOUND")
	}

	slices.SortFunc(items, func(a, b *dto.PlayerDataItem) int {
		return strings.Compare(a.MessageName, b.MessageName)
	})

	return &dto.GetPlayerDataResp{
		PlayerID: req.PlayerID,
		Items:    items,
	}, nil
}

// decodeSaveContent 将存档 content 字符串（base64编码的protobuf二进制）解析为JSON
func (l *ProtoLogic) decodeSaveContent(ctx context.Context, schema *model.ProtoSchema, messageName, content string) (*dto.PlayerDataItem, error) {
	// content 为 base64 编码的 protobuf 二进制数据
	binaryData, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		// 尝试 RawStdEncoding（无填充）
		binaryData, err = base64.RawStdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("DESERIALIZE_FAILED:invalid base64 content for %s: %s", messageName, err.Error())
		}
	}

	msgDesc, err := l.getMessageDescriptor(ctx, schema, messageName)
	if err != nil {
		return nil, err
	}

	dynMsg := dynamicpb.NewMessage(msgDesc)
	if err := proto.Unmarshal(binaryData, dynMsg); err != nil {
		return nil, fmt.Errorf("DESERIALIZE_FAILED:%s: %s", messageName, err.Error())
	}

	jsonBytes, err := protojson.MarshalOptions{
		EmitDefaultValues: true,
		UseProtoNames:     true,
	}.Marshal(dynMsg)
	if err != nil {
		return nil, fmt.Errorf("DESERIALIZE_FAILED:%s: %s", messageName, err.Error())
	}

	var jsonData any
	if err := sonic.Unmarshal(jsonBytes, &jsonData); err != nil {
		return nil, fmt.Errorf("DESERIALIZE_FAILED:%s: %s", messageName, err.Error())
	}

	return &dto.PlayerDataItem{
		MessageName: messageName,
		Data:        jsonData,
	}, nil
}

// callGamelogicDownload 调用 gamelogic /gmt/save/download 接口
func (l *ProtoLogic) callGamelogicDownload(ctx context.Context, req *gmtdto.GmtSaveDownloadReq, requestID string) (*gmtdto.GmtSaveDownloadResp, error) {
	cli := l.gamelogicSvc.Client()
	secret := []byte(l.gamelogicSvc.Secret())

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	signHeaders := buildSignHeaders(secret, bodyBytes)

	resp, err := cli.Post(ctx, "/gmt/save/download", bodyBytes,
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("调用GameLogic存档下载接口失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/save/download"),
			zap.Error(err),
		)
		return nil, fmt.Errorf("gamelogic unavailable")
	}
	defer resp.Close()

	var result struct {
		Code    int                         `json:"code"`
		Message string                      `json:"message"`
		Data    *gmtdto.GmtSaveDownloadResp `json:"data"`
	}
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		return nil, fmt.Errorf("解析GameLogic响应失败: %w", jsonErr)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("gamelogic error %d: %s", result.Code, result.Message)
	}
	if result.Data == nil {
		return &gmtdto.GmtSaveDownloadResp{}, nil
	}
	return result.Data, nil
}

// SavePlayerData 批量保存玩家分片数据（JSON输入→Protobuf二进制→转发gamelogic存档）
func (l *ProtoLogic) SavePlayerData(ctx context.Context, req *dto.SavePlayerDataReq, requestID, operator string) (*dto.SavePlayerDataResp, error) {
	// 获取全局唯一Proto模板
	schema, err := l.getGlobalSchema(ctx)
	if err != nil {
		return nil, err
	}

	// 将每个item的JSON数据序列化为Protobuf二进制并base64编码
	uploadItems := make([]*gmtdto.GmtSaveUploadItem, 0, len(req.Items))
	messageNames := make([]string, 0, len(req.Items))

	for _, item := range req.Items {
		// 1. 获取Message描述符
		msgDesc, err := l.getMessageDescriptor(ctx, schema, item.MessageName)
		if err != nil {
			return nil, err
		}

		// 2. 将JSON数据序列化为字节
		jsonBytes, err := sonic.Marshal(item.Data)
		if err != nil {
			return nil, fmt.Errorf("SERIALIZE_FAILED:message %s: invalid json data: %s", item.MessageName, err.Error())
		}

		// 3. JSON反序列化为动态Message
		dynMsg := dynamicpb.NewMessage(msgDesc)
		unmarshalOpts := protojson.UnmarshalOptions{DiscardUnknown: true}
		if err := unmarshalOpts.Unmarshal(jsonBytes, dynMsg); err != nil {
			return nil, fmt.Errorf("SERIALIZE_FAILED:message %s: %s", item.MessageName, err.Error())
		}

		// 4. 序列化为Protobuf二进制
		binaryData, err := proto.Marshal(dynMsg)
		if err != nil {
			return nil, fmt.Errorf("SERIALIZE_FAILED:message %s: %s", item.MessageName, err.Error())
		}

		// 5. base64编码
		content := base64.StdEncoding.EncodeToString(binaryData)
		uploadItems = append(uploadItems, &gmtdto.GmtSaveUploadItem{
			Key:     item.MessageName,
			Content: content,
		})
		messageNames = append(messageNames, item.MessageName)
	}

	// 6. 调用gamelogic保存存档
	uploadResp, err := l.callGamelogicUpload(ctx, &gmtdto.GmtSaveUploadReq{
		PlayerID: req.PlayerID,
		Items:    uploadItems,
	}, requestID)
	if err != nil {
		return nil, fmt.Errorf("INTERNAL:%s", err.Error())
	}

	// 异步记录操作日志
	l.logOperationAsync(operator, "PlayerData", "Save", "POST", "/api/proto/playerData/save", requestID, map[string]any{
		"playerId":     req.PlayerID,
		"messageNames": messageNames,
	})

	logx.Info("玩家分片数据批量保存成功",
		zap.String("requestId", requestID),
		zap.Int64("playerId", req.PlayerID),
		zap.Strings("messageNames", messageNames),
	)

	return &dto.SavePlayerDataResp{
		UpdateTime: uploadResp.UpdateTime,
	}, nil
}

// callGamelogicUpload 调用 gamelogic /gmt/save/upload 接口
func (l *ProtoLogic) callGamelogicUpload(ctx context.Context, req *gmtdto.GmtSaveUploadReq, requestID string) (*gmtdto.GmtSaveUploadResp, error) {
	cli := l.gamelogicSvc.Client()
	secret := []byte(l.gamelogicSvc.Secret())

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	signHeaders := buildSignHeaders(secret, bodyBytes)

	resp, err := cli.Post(ctx, "/gmt/save/upload", bodyBytes,
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("调用GameLogic存档上传接口失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/save/upload"),
			zap.Error(err),
		)
		return nil, fmt.Errorf("gamelogic unavailable")
	}
	defer resp.Close()

	var result struct {
		Code    int                       `json:"code"`
		Message string                    `json:"message"`
		Data    *gmtdto.GmtSaveUploadResp `json:"data"`
	}
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		return nil, fmt.Errorf("解析GameLogic响应失败: %w", jsonErr)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("gamelogic error %d: %s", result.Code, result.Message)
	}
	if result.Data == nil {
		return &gmtdto.GmtSaveUploadResp{}, nil
	}
	return result.Data, nil
}

// getGlobalSchema 获取全局唯一的Proto模板
// 系统设计为全局仅一份Proto模板，此方法查询最新的一条记录
func (l *ProtoLogic) getGlobalSchema(ctx context.Context) (*model.ProtoSchema, error) {
	schemaRepo := repo.NewProtoSchemaRepo(l.mongoSvc.Client())
	schema, err := schemaRepo.FindLatest(ctx)
	if err == mongox.ErrNotFound {
		return nil, fmt.Errorf("PROTO_NOT_FOUND")
	}
	if err != nil {
		return nil, fmt.Errorf("INTERNAL:%s", err.Error())
	}
	return schema, nil
}

// getMessageDescriptor 获取指定Message的描述符（优先从缓存取，未命中则用传入的schema动态编译）
// 传入已加载的schema避免重复查库；缓存以 protoName:messageName 为key
func (l *ProtoLogic) getMessageDescriptor(_ context.Context, schema *model.ProtoSchema, messageName string) (protoreflect.MessageDescriptor, error) {
	protoName := schema.ProtoName
	// 缓存 key 带入 MD5：Schema 更新后 MD5 变化，旧描述符自然不可达，规避读取-编译-回填的竞态污染
	cacheKey := protoName + ":" + schema.MD5 + ":" + messageName

	// 优先从缓存获取
	if val, ok := l.cache.Load(cacheKey); ok {
		entry := val.(*descriptorCacheEntry)
		entry.accessTime.Store(time.Now().UnixMilli())
		return entry.desc, nil
	}

	// 缓存未命中，用schema源码编译获取文件描述符（同一 Schema 的并发编译经 singleflight 合并）
	fileDesc, err := l.compileSchema(protoName, schema.MD5, schema.Content)
	if err != nil {
		return nil, fmt.Errorf("INTERNAL:compile proto failed: %s", err.Error())
	}

	// Player 模板以 Player 直接字段名作为存档 key，并自动解析该字段的消息类型。
	playerDesc := fileDesc.Messages().ByName(protoreflect.Name("Player"))
	var msgDesc protoreflect.MessageDescriptor
	if playerDesc != nil {
		fieldDesc := playerDesc.Fields().ByName(protoreflect.Name(messageName))
		if fieldDesc == nil || fieldDesc.Kind() != protoreflect.MessageKind || fieldDesc.IsMap() {
			return nil, fmt.Errorf("MESSAGE_NOT_FOUND")
		}
		msgDesc = fieldDesc.Message()
	} else {
		// 兼容不包含 Player 根消息的历史通用模板。
		msgDesc = fileDesc.Messages().ByName(protoreflect.Name(messageName))
		if msgDesc == nil {
			return nil, fmt.Errorf("MESSAGE_NOT_FOUND")
		}
	}

	// 写入缓存前执行淘汰
	l.evictCache()

	// 写入缓存
	entry := &descriptorCacheEntry{desc: msgDesc}
	entry.accessTime.Store(time.Now().UnixMilli())
	l.cache.Store(cacheKey, entry)

	return msgDesc, nil
}

// compileSchema 编译指定 Schema 源码为文件描述符，使用 singleflight 合并同一 Schema 的并发编译请求
// key 由 protoName 与 MD5 组合而成，保证不同版本的 Schema 各自独立编译
func (l *ProtoLogic) compileSchema(protoName, md5, content string) (protoreflect.FileDescriptor, error) {
	v, err, _ := l.compileGroup.Do(protoName+":"+md5, func() (any, error) {
		return compileProto(protoName+".proto", content)
	})
	if err != nil {
		return nil, err
	}
	return v.(protoreflect.FileDescriptor), nil
}

// evictCache 当缓存条目数超过上限时，淘汰最久未访问的条目
// 通过 evictMu 串行化淘汰过程，避免并发淘汰导致的计数漂移与重复删除
func (l *ProtoLogic) evictCache() {
	l.evictMu.Lock()
	defer l.evictMu.Unlock()

	var count int
	l.cache.Range(func(_, _ any) bool {
		count++
		return true
	})

	if count < l.maxCacheSize {
		return
	}

	// 淘汰最久未访问的 25% 条目
	type cacheItem struct {
		key        string
		accessTime int64
	}
	items := make([]cacheItem, 0, count)
	l.cache.Range(func(key, val any) bool {
		entry := val.(*descriptorCacheEntry)
		items = append(items, cacheItem{key: key.(string), accessTime: entry.accessTime.Load()})
		return true
	})

	// 按 accessTime 升序排序，淘汰前 25%
	evictCount := len(items) / 4
	if evictCount == 0 {
		evictCount = 1
	}

	// 简单选择排序找到最小的 evictCount 个
	for i := 0; i < evictCount && i < len(items); i++ {
		minIdx := i
		for j := i + 1; j < len(items); j++ {
			if items[j].accessTime < items[minIdx].accessTime {
				minIdx = j
			}
		}
		items[i], items[minIdx] = items[minIdx], items[i]
		l.cache.Delete(items[i].key)
	}
}

// invalidateCache 清空指定Proto下所有Message缓存
func (l *ProtoLogic) invalidateCache(protoName string) {
	prefix := protoName + ":"
	l.cache.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
			l.cache.Delete(key)
		}
		return true
	})
}

// checkSchemaCompatibility 执行Schema兼容性差分校验
// 返回所有破坏性变更的描述列表，空列表表示兼容
func (l *ProtoLogic) checkSchemaCompatibility(_ context.Context, existing *model.ProtoSchema, newContent string, _ []string) []string {
	var breaking []string

	// 编译旧Proto
	oldFileDesc, err := compileProto(existing.ProtoName+".proto", existing.Content)
	if err != nil {
		// 旧Proto编译失败，允许覆盖
		return nil
	}

	// 编译新Proto
	newFileDesc, err := compileProto(existing.ProtoName+".proto", newContent)
	if err != nil {
		breaking = append(breaking, fmt.Sprintf("new proto compile failed: %s", err.Error()))
		return breaking
	}

	// 构建旧Message映射
	oldMessages := make(map[string]protoreflect.MessageDescriptor)
	for i := range oldFileDesc.Messages().Len() {
		msg := oldFileDesc.Messages().Get(i)
		oldMessages[string(msg.Name())] = msg
	}

	// 构建新Message映射
	newMsgMap := make(map[string]protoreflect.MessageDescriptor)
	for i := range newFileDesc.Messages().Len() {
		msg := newFileDesc.Messages().Get(i)
		newMsgMap[string(msg.Name())] = msg
	}

	// 检查已删除的Message（保守策略：删除已有Message视为破坏性变更）
	for oldName := range oldMessages {
		if _, exists := newMsgMap[oldName]; !exists {
			breaking = append(breaking, fmt.Sprintf("cannot delete message %s: message removal is not allowed", oldName))
		}
	}

	// 对仍然存在的Message进行字段级差分校验
	for oldName, oldMsg := range oldMessages {
		newMsg, exists := newMsgMap[oldName]
		if !exists {
			continue
		}
		fieldBreaking := checkMessageFieldCompatibility(oldName, oldMsg, newMsg)
		breaking = append(breaking, fieldBreaking...)
	}

	return breaking
}

// checkMessageFieldCompatibility 校验单个Message的字段兼容性
func checkMessageFieldCompatibility(msgName string, oldMsg, newMsg protoreflect.MessageDescriptor) []string {
	var breaking []string

	// 构建旧字段映射（按tag号）
	oldFields := make(map[protoreflect.FieldNumber]protoreflect.FieldDescriptor)
	for i := range oldMsg.Fields().Len() {
		f := oldMsg.Fields().Get(i)
		oldFields[f.Number()] = f
	}

	// 检查旧字段是否被删除或修改
	for num, oldField := range oldFields {
		newField := newMsg.Fields().ByNumber(num)
		if newField == nil {
			breaking = append(breaking, fmt.Sprintf("message %s: field %s (tag=%d) deleted", msgName, oldField.Name(), num))
			continue
		}
		// 检查字段名是否变更
		if oldField.Name() != newField.Name() {
			breaking = append(breaking, fmt.Sprintf("message %s: field tag=%d name changed from %s to %s", msgName, num, oldField.Name(), newField.Name()))
		}
		// 检查字段类型是否变更
		if oldField.Kind() != newField.Kind() {
			breaking = append(breaking, fmt.Sprintf("message %s: field %s type changed from %s to %s", msgName, oldField.Name(), oldField.Kind(), newField.Kind()))
		}

		// 检查repeated/非repeated互改
		if oldField.IsList() != newField.IsList() {
			breaking = append(breaking, fmt.Sprintf("message %s: field %s list cardinality changed", msgName, oldField.Name()))
		}
		// 检查map类型变更
		if oldField.IsMap() != newField.IsMap() {
			breaking = append(breaking, fmt.Sprintf("message %s: field %s map type changed", msgName, oldField.Name()))
		}
		// 如果都是map，检查key和value类型
		if oldField.IsMap() && newField.IsMap() {
			if oldField.MapKey().Kind() != newField.MapKey().Kind() {
				breaking = append(breaking, fmt.Sprintf("message %s: field %s map key type changed", msgName, oldField.Name()))
			}
			if oldField.MapValue().Kind() != newField.MapValue().Kind() {
				breaking = append(breaking, fmt.Sprintf("message %s: field %s map value type changed", msgName, oldField.Name()))
			}
		}
		// 检查消息类型引用是否变更
		if oldField.Kind() == protoreflect.MessageKind && newField.Kind() == protoreflect.MessageKind {
			if oldField.Message().FullName() != newField.Message().FullName() {
				breaking = append(breaking, fmt.Sprintf("message %s: field %s message type changed from %s to %s", msgName, oldField.Name(), oldField.Message().FullName(), newField.Message().FullName()))
			}
		}
	}

	// 检查Enum兼容性（嵌套在Message内部的Enum）
	for i := range oldMsg.Enums().Len() {
		oldEnum := oldMsg.Enums().Get(i)
		newEnum := newMsg.Enums().ByName(oldEnum.Name())
		if newEnum == nil {
			breaking = append(breaking, fmt.Sprintf("message %s: enum %s deleted", msgName, oldEnum.Name()))
			continue
		}
		enumBreaking := checkEnumCompatibility(msgName, oldEnum, newEnum)
		breaking = append(breaking, enumBreaking...)
	}

	// 检查Oneof兼容性
	for i := range oldMsg.Oneofs().Len() {
		oldOneof := oldMsg.Oneofs().Get(i)
		newOneof := newMsg.Oneofs().ByName(oldOneof.Name())
		if newOneof == nil {
			breaking = append(breaking, fmt.Sprintf("message %s: oneof %s deleted", msgName, oldOneof.Name()))
			continue
		}
		// 检查oneof内字段数量是否减少
		if oldOneof.Fields().Len() > newOneof.Fields().Len() {
			breaking = append(breaking, fmt.Sprintf("message %s: oneof %s fields reduced", msgName, oldOneof.Name()))
		}
	}

	// 递归校验嵌套Message的字段兼容性（跳过 map 隐式生成的 entry 消息）
	for i := range oldMsg.Messages().Len() {
		oldNested := oldMsg.Messages().Get(i)
		if oldNested.IsMapEntry() {
			continue
		}
		newNested := newMsg.Messages().ByName(oldNested.Name())
		if newNested == nil {
			breaking = append(breaking, fmt.Sprintf("message %s: nested message %s deleted", msgName, oldNested.Name()))
			continue
		}
		nestedName := msgName + "." + string(oldNested.Name())
		breaking = append(breaking, checkMessageFieldCompatibility(nestedName, oldNested, newNested)...)
	}

	return breaking
}

// checkEnumCompatibility 校验Enum兼容性
func checkEnumCompatibility(msgName string, oldEnum, newEnum protoreflect.EnumDescriptor) []string {
	var breaking []string

	// 检查名称是否变更
	if oldEnum.Name() != newEnum.Name() {
		breaking = append(breaking, fmt.Sprintf("message %s: enum name changed from %s to %s", msgName, oldEnum.Name(), newEnum.Name()))
	}

	// 检查已有枚举值是否被删除或编号变更
	for i := range oldEnum.Values().Len() {
		oldVal := oldEnum.Values().Get(i)
		newVal := newEnum.Values().ByNumber(oldVal.Number())
		if newVal == nil {
			breaking = append(breaking, fmt.Sprintf("message %s: enum %s value %s (number=%d) deleted", msgName, oldEnum.Name(), oldVal.Name(), oldVal.Number()))
			continue
		}
		if oldVal.Name() != newVal.Name() {
			breaking = append(breaking, fmt.Sprintf("message %s: enum %s value number=%d name changed from %s to %s", msgName, oldEnum.Name(), oldVal.Number(), oldVal.Name(), newVal.Name()))
		}
	}

	return breaking
}

// parseProto 解析 Proto 源码，返回玩家存档 key 列表与 import 依赖列表。
// 存在 Player 根消息时，列表取其直接 message 字段名；否则兼容使用顶层 Message 名称。
// 先用 parser 检查语法与 import，再用动态编译描述符识别字段类型，避免 AST 中未解析类型导致漏项。
func parseProto(protoName, content string) (messages []string, imports []string, err error) {
	filename := protoName + ".proto"
	handler := reporter.NewHandler(nil)
	fileNode, parseErr := parser.Parse(filename, strings.NewReader(content), handler)
	if parseErr != nil {
		return nil, nil, parseErr
	}

	result, resErr := parser.ResultFromAST(fileNode, true, handler)
	if resErr != nil {
		return nil, nil, resErr
	}

	imports = result.FileDescriptorProto().GetDependency()
	if len(imports) > 0 {
		return nil, imports, nil
	}

	fileDesc, compileErr := compileProto(filename, content)
	if compileErr != nil {
		return nil, nil, compileErr
	}

	return messageListFromDescriptor(fileDesc), nil, nil
}

// messageListFromDescriptor 从动态描述符提取可用于玩家存档的 key 列表。
func messageListFromDescriptor(fileDesc protoreflect.FileDescriptor) []string {
	playerDesc := fileDesc.Messages().ByName(protoreflect.Name("Player"))
	if playerDesc != nil {
		messages := make([]string, 0, playerDesc.Fields().Len())
		for i := range playerDesc.Fields().Len() {
			fieldDesc := playerDesc.Fields().Get(i)
			if fieldDesc.Kind() != protoreflect.MessageKind || fieldDesc.IsMap() {
				continue
			}
			messages = append(messages, string(fieldDesc.Name()))
		}
		return messages
	}

	// 兼容不包含 Player 根消息的历史通用模板。
	messages := make([]string, 0, fileDesc.Messages().Len())
	for i := range fileDesc.Messages().Len() {
		messages = append(messages, string(fileDesc.Messages().Get(i).Name()))
	}
	return messages
}

// compileProto 编译Proto源码为文件描述符
// 使用 bufbuild/protocompile 实现纯Go动态编译，无需protoc
func compileProto(filename, content string) (protoreflect.FileDescriptor, error) {
	compiler := protocompile.Compiler{
		Resolver: &protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(map[string]string{
				filename: content,
			}),
		},
	}
	files, err := compiler.Compile(context.Background(), filename)
	if err != nil {
		return nil, err
	}
	return files[0], nil
}

// logOperationAsync 异步记录操作日志
func (l *ProtoLogic) logOperationAsync(uid, module, action, method, uri, requestID string, reqData any) {
	if l.asyncSvc == nil {
		return
	}
	mongoSvc := l.mongoSvc
	ctx := async.NewContext().
		Set("uid", uid).
		Set("module", module).
		Set("action", action).
		Set("method", method).
		Set("uri", uri).
		Set("requestId", requestID).
		Set("reqData", reqData)
	l.asyncSvc.SubmitFunc("log", "operationLog", func(c *async.Context) error {
		logRepo := repo.NewOperationLogRepo(mongoSvc.Client())
		return logRepo.InsertLog(context.Background(), &model.OperationLog{
			UID:      c.GetString("uid"),
			Username: c.GetString("uid"),
			Module:   c.GetString("module"),
			Action:   c.GetString("action"),
			Method:   c.GetString("method"),
			URI:      c.GetString("uri"),
			Request:  c.Get("reqData"),
		})
	}, ctx)
}
