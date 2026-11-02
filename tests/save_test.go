// Package tests 模拟客户端对 GameLogic 服务存档接口的集成测试。
// 测试目标：POST /api/save/upload、POST /api/save/download
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// 协议层 content 为原始线格式字节（gzip 压缩的 protobuf，非 base64）；存储层内部为 base64 编码。
// 签名使用线格式字节的 SHA256 小写十六进制；存档秘钥（原始 32 字节二进制）在登录响应中下发。
// 运行方式（需服务已启动）：
//
//	go test ./tests/... -v -run TestSave
package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/savex"
)

// ---- 路径常量 ----

const (
	saveUploadPath   = "/api/save/upload"
	saveDownloadPath = "/api/save/download"
)

// ---- 辅助函数 ----

// loginAsGuestWithKey 创建一个新游客账号并返回 token、playerID 与收发包加密秘钥，失败则 Fatal。
// 收发包秘钥由服务器在注册时生成、登录时下发（原始 32 字节二进制），客户端用于对 protobuf 收发包做 AES 加解密（packetx）。
// 连接失败时调用 t.Skipf 跳过，只能在测试主 goroutine 中调用。
func loginAsGuestWithKey(t *testing.T) (token string, playerID int64, packetKey []byte) {
	t.Helper()
	req := &cspb.LoginReq{
		LoginType:  "guest",
		Platform:   "ios",
		Uuid:       uuid.NewString(),
		ClientInfo: newTestClientInfo(),
	}
	_, resp := doLogin(t, req, fmt.Sprintf("save-login-%s", uuid.NewString()[:8]))
	if resp.Code != 0 || resp.Data == nil {
		t.Fatalf("游客登录失败：code=%d, message=%q", resp.Code, resp.Message)
	}
	// 记录 token 对应的收发包秘钥，供后续认证请求加解密使用
	rememberPacketKey(resp.Data.Token, resp.Data.SaveKey)
	return resp.Data.Token, resp.Data.PlayerId, resp.Data.SaveKey
}

// loginAsGuest 创建一个新游客账号并返回 token 与 playerID，供无需操作存档秘钥的测试使用，
// 返回值为 loginAsGuestWithKey 的子集。
func loginAsGuest(t *testing.T) (token string, playerID int64) {
	t.Helper()
	token, playerID, _ = loginAsGuestWithKey(t)
	return token, playerID
}

// encodeSave 模拟客户端编码存档：message 经 gzip 压缩后得到原始线格式字节作为 content
// （协议 bytes 字段，非 base64），并对该线格式字节计算 SHA256 小写十六进制签名。
// 存档已取消加解密，仅做压缩；返回 content（原始二进制）与 sign，与服务器存储/校验格式一致。
func encodeSave(t *testing.T, message proto.Message) (content []byte, sign string) {
	t.Helper()
	wire, err := savex.Encode(message)
	if err != nil {
		t.Fatalf("存档编码失败: %v", err)
	}
	return wire, savex.ComputeSign(wire)
}

// unmarshalBase 将原始线格式字节 content（gzip 压缩的 protobuf）解压后反序列化为 Base message。
// 存档已取消加解密，仅做解压。
func unmarshalBase(t *testing.T, content []byte) *cspb.Base {
	t.Helper()
	msg, err := savex.Decode(content, func() *cspb.Base { return new(cspb.Base) })
	if err != nil {
		t.Fatalf("解压反序列化 Base 失败: %v", err)
	}
	return msg
}

// unmarshalDefaultData 将原始线格式字节 content（gzip 压缩的 protobuf）解压后反序列化为 DefaultData message。
func unmarshalDefaultData(t *testing.T, content []byte) *cspb.DefaultData {
	t.Helper()
	msg, err := savex.Decode(content, func() *cspb.DefaultData { return new(cspb.DefaultData) })
	if err != nil {
		t.Fatalf("解压反序列化 DefaultData 失败: %v", err)
	}
	return msg
}

// doUpload 以 protobuf 二进制格式发送存档上传请求，返回 HTTP 状态码和响应。
// 请求体经收发包加密（marshalProtoReq），响应体经收发包解密（unmarshalProtoResp）。
// 连接失败时调用 t.Skipf 跳过，只能在测试主 goroutine 中调用。
func doUpload(t *testing.T, token string, req *cspb.SaveUploadReq, requestID string) (int, *cspb.SaveUploadResp) {
	t.Helper()

	bodyBytes := marshalProtoReq(t, token, req)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + saveUploadPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	if requestID != "" {
		httpReq.Header.Set("X-Request-Id", requestID)
	}

	httpClient := &http.Client{Timeout: requestTimeout}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		t.Skipf("跳过测试：无法连接到服务 %s（%v），请先启动 gamelogic 服务", url, err)
		return 0, nil
	}
	defer resp.Body.Close()

	// 校验响应 Content-Type
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-protobuf" {
		t.Errorf("上传响应 Content-Type = %q，期望 application/x-protobuf", ct)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取上传响应体失败: %v", err)
	}
	var result cspb.SaveUploadResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return resp.StatusCode, &result
}

// doDownload 以 protobuf 二进制格式发送存档批量下载请求，返回 HTTP 状态码和响应。
// 请求体经收发包加密（marshalProtoReq），响应体经收发包解密（unmarshalProtoResp）。
// 连接失败时调用 t.Skipf 跳过，只能在测试主 goroutine 中调用。
func doDownload(t *testing.T, token string, keys []string, requestID string) (int, *cspb.SaveDownloadResp) {
	t.Helper()

	bodyBytes := marshalProtoReq(t, token, &cspb.SaveDownloadReq{Keys: keys})

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + saveDownloadPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	if requestID != "" {
		httpReq.Header.Set("X-Request-Id", requestID)
	}

	httpClient := &http.Client{Timeout: requestTimeout}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		t.Skipf("跳过测试：无法连接到服务 %s（%v），请先启动 gamelogic 服务", url, err)
		return 0, nil
	}
	defer resp.Body.Close()

	// 校验响应 Content-Type
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-protobuf" {
		t.Errorf("下载响应 Content-Type = %q，期望 application/x-protobuf", ct)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取下载响应体失败: %v", err)
	}
	var result cspb.SaveDownloadResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return resp.StatusCode, &result
}

// ---- 存档测试 ----

// TestSave_Upload_FirstTime 首次上传存档：baseVersion=0，期望返回 code=0 且 version 与请求一致
// 使用 player.proto 中的 Base message 作为存档数据
func TestSave_Upload_FirstTime(t *testing.T) {
	token, playerID := loginAsGuest(t)

	baseData := &cspb.Base{Lv: 1, Nickname: "测试", Avatar: "头像", AvatarFrame: "头像框", Exp: 0}
	content, sign := encodeSave(t, baseData)
	req := &cspb.SaveUploadReq{
		Key:         "base",
		BaseVersion: 0,
		NewVersion:  1,
		Sign:        sign,
		Content:     content,
	}
	httpStatus, resp := doUpload(t, token, req, fmt.Sprintf("save-upload-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil")
	}
	if resp.Data.Version != req.NewVersion {
		t.Errorf("返回 version = %d，期望 %d", resp.Data.Version, req.NewVersion)
	}
	t.Logf("首次上传成功：playerId=%d, key=%q, version=%d", playerID, req.Key, resp.Data.Version)
}

// TestSave_Upload_FirstTime_NonZeroBaseVersion 首次上传携带非 0 baseVersion：
// 服务端无该 key 存档时 baseVersion 不代表服务端状态（客户端可能携带本地版本号），
// 期望忽略该字段正常写入，返回 code=0 且 version=newVersion，而非返回冲突 2409。
func TestSave_Upload_FirstTime_NonZeroBaseVersion(t *testing.T) {
	token, playerID := loginAsGuest(t)

	baseData := &cspb.Base{Lv: 3, Nickname: "首次非零基线"}
	content, sign := encodeSave(t, baseData)
	req := &cspb.SaveUploadReq{
		Key:         "base",
		BaseVersion: 7,
		NewVersion:  8,
		Sign:        sign,
		Content:     content,
	}
	httpStatus, resp := doUpload(t, token, req, fmt.Sprintf("save-first-nonzero-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil || resp.Data.Version != req.NewVersion {
		t.Fatalf("返回 version = %v，期望 %d", resp.Data, req.NewVersion)
	}

	// 下载验证首次写入的版本与内容正确落库
	_, downResp := doDownload(t, token, []string{"base"}, fmt.Sprintf("save-first-nonzero-down-%s", uuid.NewString()[:8]))
	if downResp.Code != 0 || downResp.Data == nil || len(downResp.Data.Items) != 1 {
		t.Fatalf("下载失败：response=%+v", downResp)
	}
	item := downResp.Data.Items[0]
	if item.Version != req.NewVersion {
		t.Errorf("下载 version = %d，期望 %d", item.Version, req.NewVersion)
	}
	if decoded := unmarshalBase(t, item.Content); decoded.Lv != baseData.Lv {
		t.Errorf("下载数据 lv = %d，期望 %d", decoded.Lv, baseData.Lv)
	}
	t.Logf("首次上传非零 baseVersion 成功：playerId=%d, version=%d", playerID, item.Version)
}

// TestSave_Upload_MultiVersion 连续上传多个版本：验证每次上传版本号正确递增
// 使用 player.proto 中的 Base message，模拟玩家等级提升
func TestSave_Upload_MultiVersion(t *testing.T) {
	token, playerID := loginAsGuest(t)

	type step struct {
		baseVersion int64
		newVersion  int64
		data        *cspb.Base
	}
	steps := []step{
		{0, 1, &cspb.Base{Lv: 1}},
		{1, 2, &cspb.Base{Lv: 2}},
		{2, 5, &cspb.Base{Lv: 5}},
	}

	for _, s := range steps {
		content, sign := encodeSave(t, s.data)
		req := &cspb.SaveUploadReq{
			Key:         "base",
			BaseVersion: s.baseVersion,
			NewVersion:  s.newVersion,
			Sign:        sign,
			Content:     content,
		}
		_, resp := doUpload(t, token, req, fmt.Sprintf("save-multi-%s", uuid.NewString()[:8]))
		if resp.Code != 0 {
			t.Fatalf("上传版本 %d 失败：code=%d, message=%q", s.newVersion, resp.Code, resp.Message)
		}
		if resp.Data == nil || resp.Data.Version != s.newVersion {
			t.Errorf("上传版本 %d：返回 version=%v，期望 %d", s.newVersion, resp.Data, s.newVersion)
		}
	}
	t.Logf("多版本上传成功：playerId=%d, 共 %d 步", playerID, len(steps))
}

// TestSave_Download_AfterUpload 上传后下载：验证返回内容与最后一次上传一致
// 验证 content 可正确反序列化为 Base message
func TestSave_Download_AfterUpload(t *testing.T) {
	token, playerID := loginAsGuest(t)

	baseData := &cspb.Base{Lv: 99}
	content, sign := encodeSave(t, baseData)

	uploadReq := &cspb.SaveUploadReq{
		Key:         "base",
		BaseVersion: 0,
		NewVersion:  1,
		Sign:        sign,
		Content:     content,
	}
	_, upResp := doUpload(t, token, uploadReq, fmt.Sprintf("save-up-%s", uuid.NewString()[:8]))
	if upResp.Code != 0 {
		t.Fatalf("上传失败：code=%d, message=%q", upResp.Code, upResp.Message)
	}

	httpStatus, downResp := doDownload(t, token, []string{"base"}, fmt.Sprintf("save-down-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if downResp.Code != 0 {
		t.Fatalf("下载失败：code=%d, message=%q", downResp.Code, downResp.Message)
	}
	if downResp.Data == nil || len(downResp.Data.Items) == 0 {
		t.Fatal("data.items 为空")
	}

	item := downResp.Data.Items[0]
	if item.Key != "base" {
		t.Errorf("item.key = %q，期望 %q", item.Key, "base")
	}
	if item.Version != uploadReq.NewVersion {
		t.Errorf("item.version = %d，期望 %d", item.Version, uploadReq.NewVersion)
	}
	if item.Sign != uploadReq.Sign {
		t.Errorf("item.sign = %q，期望 %q", item.Sign, uploadReq.Sign)
	}
	if !bytes.Equal(item.Content, uploadReq.Content) {
		t.Errorf("item.content 不匹配，期望原始线格式字节一致")
	}

	// 反序列化验证数据完整性（存档已取消加密，仅需解压）
	decoded := unmarshalBase(t, item.Content)
	if decoded.Lv != baseData.Lv {
		t.Errorf("decoded.Lv = %d，期望 %d", decoded.Lv, baseData.Lv)
	}
	t.Logf("下载成功：playerId=%d, key=%q, version=%d, lv=%d",
		playerID, item.Key, item.Version, decoded.Lv)
}

// TestSave_Download_NoSave 下载不存在的 key：期望返回 code=0，version=0，内容为空
func TestSave_Download_NoSave(t *testing.T) {
	token, playerID := loginAsGuest(t)

	// 新账号从未上传，直接下载应返回空存档
	_, resp := doDownload(t, token, []string{"base"}, fmt.Sprintf("save-nosave-%s", uuid.NewString()[:8]))

	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil || len(resp.Data.Items) == 0 {
		t.Fatal("data.items 为空")
	}

	item := resp.Data.Items[0]
	if item.Key != "base" {
		t.Errorf("item.key = %q，期望 %q", item.Key, "base")
	}
	if item.Version != 0 {
		t.Errorf("item.version = %d，期望 0（无存档）", item.Version)
	}
	if len(item.Content) != 0 {
		t.Errorf("item.content = %q，期望为空", item.Content)
	}
	t.Logf("无存档下载符合预期：playerId=%d, key=%q, version=0", playerID, item.Key)
}

// playerSaveField 表示 Player 直接字段对应的存档 key、payload 与编码后的断言数据。
type playerSaveField struct {
	key     string
	payload proto.Message
	content []byte
	sign    string
}

// assertPlayerSaveFieldCoverage 校验用例表覆盖 Player 的全部直接非 map message 字段。
func assertPlayerSaveFieldCoverage(t *testing.T, fields []playerSaveField) {
	t.Helper()

	playerDesc := (&cspb.Player{}).ProtoReflect().Descriptor()
	expectedKeys := make(map[string]struct{}, playerDesc.Fields().Len())
	for i := range playerDesc.Fields().Len() {
		fieldDesc := playerDesc.Fields().Get(i)
		if fieldDesc.IsMap() || fieldDesc.Message() == nil {
			continue
		}
		expectedKeys[string(fieldDesc.Name())] = struct{}{}
	}

	coveredKeys := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field.key == "" {
			t.Error("Player 存档字段测试表包含空 key")
			continue
		}
		if field.payload == nil {
			t.Errorf("Player 存档字段 %q 缺少 payload", field.key)
		}
		if _, exists := coveredKeys[field.key]; exists {
			t.Errorf("Player 存档字段测试表包含重复 key=%q", field.key)
		}
		coveredKeys[field.key] = struct{}{}
	}

	if len(coveredKeys) != len(expectedKeys) {
		t.Errorf("Player 存档字段测试表数量 = %d，协议直接 message 字段数量 = %d", len(coveredKeys), len(expectedKeys))
	}
	for key := range expectedKeys {
		if _, exists := coveredKeys[key]; !exists {
			t.Errorf("Player 存档字段测试表遗漏协议字段 key=%q", key)
		}
	}
	for key := range coveredKeys {
		if _, exists := expectedKeys[key]; !exists {
			t.Errorf("Player 存档字段测试表包含非协议字段 key=%q", key)
		}
	}
}

// TestSave_PlayerFields_RoundTrip 验证 Player 的全部直接 message 字段均可作为独立存档 key 回环。
// base 对应 Base；其余字段对应 DefaultData；下载结果必须保持请求 key 的顺序。
func TestSave_PlayerFields_RoundTrip(t *testing.T) {
	token, playerID := loginAsGuest(t)

	baseData := &cspb.Base{
		Lv:             10,
		Nickname:       "save-player-fields",
		Avatar:         "avatar-10",
		AvatarFrame:    "frame-10",
		Exp:            1000,
		Gold:           200,
		Gem:            30,
		CurrentTask:    "1_1",
		SyncTime:       1730000000000,
		InstallationId: "installation-save-test",
		PlayerId:       playerID,
		Energy:         15,
	}
	defaultData := func(key string) *cspb.DefaultData {
		return &cspb.DefaultData{Data: fmt.Sprintf(`{"saveKey":%q,"payload":"all-player-fields"}`, key)}
	}

	fields := []playerSaveField{
		{key: "base", payload: baseData},
		{key: "item", payload: defaultData("item")},
		{key: "board", payload: defaultData("board")},
		{key: "cacheItem", payload: defaultData("cacheItem")},
		{key: "inventory", payload: defaultData("inventory")},
		{key: "orderMeta", payload: defaultData("orderMeta")},
		{key: "orders", payload: defaultData("orders")},
		{key: "shop", payload: defaultData("shop")},
		{key: "shopItem", payload: defaultData("shopItem")},
		{key: "slot", payload: defaultData("slot")},
		{key: "extra_skin", payload: defaultData("extra_skin")},
		{key: "activity_data", payload: defaultData("activity_data")},
		{key: "wedding_day", payload: defaultData("wedding_day")},
		{key: "bundle", payload: defaultData("bundle")},
		{key: "bundle_meta", payload: defaultData("bundle_meta")},
		{key: "buffproperty", payload: defaultData("buffproperty")},
		{key: "buffproperty_meta", payload: defaultData("buffproperty_meta")},
		{key: "mainTask", payload: defaultData("mainTask")},
		{key: "itemUnlock", payload: defaultData("itemUnlock")},
		{key: "tutorial", payload: defaultData("tutorial")},
		{key: "misc", payload: defaultData("misc")},
		{key: "ad_status", payload: defaultData("ad_status")},
		{key: "profile", payload: defaultData("profile")},
		{key: "character_intimacy", payload: defaultData("character_intimacy")},
		{key: "return_user", payload: defaultData("return_user")},
		{key: "energy", payload: defaultData("energy")},
		{key: "pakcet_progress", payload: defaultData("pakcet_progress")},
	}
	assertPlayerSaveFieldCoverage(t, fields)

	for index := range fields {
		fields[index].content, fields[index].sign = encodeSave(t, fields[index].payload)
		_, upResp := doUpload(t, token, &cspb.SaveUploadReq{
			Key:         fields[index].key,
			BaseVersion: 0,
			NewVersion:  1,
			Sign:        fields[index].sign,
			Content:     fields[index].content,
		}, fmt.Sprintf("save-player-fields-up-%s", uuid.NewString()[:8]))
		if upResp.Code != 0 || upResp.Data == nil || upResp.Data.Version != 1 {
			t.Fatalf("上传 key=%q 失败：response=%+v", fields[index].key, upResp)
		}
	}

	downloadKeys := make([]string, len(fields))
	for index := range fields {
		downloadKeys[len(fields)-1-index] = fields[index].key
	}
	_, downResp := doDownload(t, token, downloadKeys, fmt.Sprintf("save-player-fields-down-%s", uuid.NewString()[:8]))
	if downResp.Code != 0 || downResp.Data == nil {
		t.Fatalf("批量下载失败：response=%+v", downResp)
	}
	if len(downResp.Data.Items) != len(downloadKeys) {
		t.Fatalf("返回 items 数量 = %d，期望 %d", len(downResp.Data.Items), len(downloadKeys))
	}

	for index, item := range downResp.Data.Items {
		expected := fields[len(fields)-1-index]
		if item.Key != expected.key {
			t.Errorf("items[%d].key = %q，期望 %q", index, item.Key, expected.key)
		}
		if item.Version != 1 {
			t.Errorf("key=%q version = %d，期望 1", expected.key, item.Version)
		}
		if item.Sign != expected.sign {
			t.Errorf("key=%q sign 不匹配", expected.key)
		}
		if !bytes.Equal(item.Content, expected.content) {
			t.Errorf("key=%q content 不匹配", expected.key)
		}
		if actualSign := savex.ComputeSign(item.Content); actualSign != item.Sign {
			t.Errorf("key=%q content 签名 = %q，期望 %q", expected.key, actualSign, item.Sign)
		}

		switch payload := expected.payload.(type) {
		case *cspb.Base:
			if decoded := unmarshalBase(t, item.Content); !proto.Equal(decoded, payload) {
				t.Errorf("key=%q 的 Base 解码数据不匹配：got=%+v, want=%+v", expected.key, decoded, payload)
			}
		case *cspb.DefaultData:
			if decoded := unmarshalDefaultData(t, item.Content); !proto.Equal(decoded, payload) {
				t.Errorf("key=%q 的 DefaultData 解码数据不匹配：got=%+v, want=%+v", expected.key, decoded, payload)
			}
		default:
			t.Errorf("key=%q 使用了未覆盖的 payload 类型 %T", expected.key, payload)
		}
	}
	t.Logf("Player 全字段存档回环成功：playerId=%d, fieldCount=%d, keys=%v", playerID, len(fields), downloadKeys)
}

// TestSave_Upload_ForceOverwrite 强制覆盖：跳过 CAS 检测，直接写入更高版本
func TestSave_Upload_ForceOverwrite(t *testing.T) {
	token, playerID := loginAsGuest(t)

	// 先正常上传 version=1
	v1Data := &cspb.Base{Lv: 1}
	v1Content, v1Sign := encodeSave(t, v1Data)
	_, upResp := doUpload(t, token, &cspb.SaveUploadReq{
		Key: "base", BaseVersion: 0, NewVersion: 1,
		Sign: v1Sign, Content: v1Content,
	}, fmt.Sprintf("save-force-init-%s", uuid.NewString()[:8]))
	if upResp.Code != 0 {
		t.Fatalf("初始上传失败：code=%d", upResp.Code)
	}

	// 强制覆盖写入 version=10（跳过 CAS）
	v10Data := &cspb.Base{Lv: 10}
	v10Content, v10Sign := encodeSave(t, v10Data)
	_, forceResp := doUpload(t, token, &cspb.SaveUploadReq{
		Key: "base", BaseVersion: 0, NewVersion: 10,
		Sign: v10Sign, Content: v10Content,
		ForceOverwrite: true,
	}, fmt.Sprintf("save-force-%s", uuid.NewString()[:8]))
	if forceResp.Code != 0 {
		t.Fatalf("强制覆盖失败：code=%d, message=%q", forceResp.Code, forceResp.Message)
	}
	if forceResp.Data == nil || forceResp.Data.Version != 10 {
		t.Errorf("强制覆盖返回 version=%v，期望 10", forceResp.Data)
	}

	// 下载验证内容已被覆盖
	_, downResp := doDownload(t, token, []string{"base"}, fmt.Sprintf("save-force-down-%s", uuid.NewString()[:8]))
	if downResp.Code != 0 || len(downResp.Data.Items) == 0 {
		t.Fatalf("覆盖后下载失败：code=%d", downResp.Code)
	}
	item := downResp.Data.Items[0]
	if item.Version != 10 {
		t.Errorf("覆盖后 version=%d，期望 10", item.Version)
	}
	if !bytes.Equal(item.Content, v10Content) {
		t.Errorf("覆盖后 content 不匹配")
	}
	// 反序列化验证覆盖后的数据
	decoded := unmarshalBase(t, item.Content)
	if decoded.Lv != 10 {
		t.Errorf("覆盖后数据异常：lv=%d", decoded.Lv)
	}
	t.Logf("强制覆盖成功：playerId=%d, version=%d, lv=%d", playerID, item.Version, decoded.Lv)
}

// TestSave_Upload_ForceOverwrite_IgnoresBaseVersion 强制覆盖时 baseVersion 不参与校验：
// 即使 baseVersion 大于 newVersion，只要 newVersion 超过服务端当前版本即可覆盖写入。
func TestSave_Upload_ForceOverwrite_IgnoresBaseVersion(t *testing.T) {
	token, playerID := loginAsGuest(t)

	v1Content, v1Sign := encodeSave(t, &cspb.Base{Lv: 1})
	_, upResp := doUpload(t, token, &cspb.SaveUploadReq{
		Key: "base", BaseVersion: 0, NewVersion: 1,
		Sign: v1Sign, Content: v1Content,
	}, fmt.Sprintf("save-force-ignore-init-%s", uuid.NewString()[:8]))
	if upResp.Code != 0 {
		t.Fatalf("初始上传失败：code=%d, message=%q", upResp.Code, upResp.Message)
	}

	// baseVersion=100 大于 newVersion=5，但强制覆盖只要求 newVersion 超过服务端当前版本 1
	v5Content, v5Sign := encodeSave(t, &cspb.Base{Lv: 5})
	_, forceResp := doUpload(t, token, &cspb.SaveUploadReq{
		Key: "base", BaseVersion: 100, NewVersion: 5,
		Sign: v5Sign, Content: v5Content, ForceOverwrite: true,
	}, fmt.Sprintf("save-force-ignore-%s", uuid.NewString()[:8]))
	if forceResp.Code != 0 {
		t.Fatalf("强制覆盖失败：code=%d, message=%q", forceResp.Code, forceResp.Message)
	}
	if forceResp.Data == nil || forceResp.Data.Version != 5 {
		t.Errorf("强制覆盖返回 version=%v，期望 5", forceResp.Data)
	}
	t.Logf("强制覆盖忽略 baseVersion 成功：playerId=%d, version=5", playerID)
}

// TestSave_PlayerFields_IndependentVersions 验证 Player 各直接字段的版本独立且同类型分片不会互相覆盖。
func TestSave_PlayerFields_IndependentVersions(t *testing.T) {
	token, playerID := loginAsGuest(t)

	baseV1 := &cspb.Base{Lv: 1, Nickname: "independent-base"}
	itemV1 := &cspb.DefaultData{Data: `{"items":[{"id":"item-v1"}]}`}
	boardV1 := &cspb.DefaultData{Data: `{"board":{"stage":1}}`}
	baseContent, baseSign := encodeSave(t, baseV1)
	itemContent, itemSign := encodeSave(t, itemV1)
	boardV1Content, boardV1Sign := encodeSave(t, boardV1)

	initialUploads := []struct {
		key     string
		content []byte
		sign    string
	}{
		{key: "base", content: baseContent, sign: baseSign},
		{key: "item", content: itemContent, sign: itemSign},
		{key: "board", content: boardV1Content, sign: boardV1Sign},
	}
	for _, upload := range initialUploads {
		_, resp := doUpload(t, token, &cspb.SaveUploadReq{
			Key:         upload.key,
			BaseVersion: 0,
			NewVersion:  1,
			Sign:        upload.sign,
			Content:     upload.content,
		}, fmt.Sprintf("save-ind-init-%s", uuid.NewString()[:8]))
		if resp.Code != 0 || resp.Data == nil || resp.Data.Version != 1 {
			t.Fatalf("上传 %s v1 失败：response=%+v", upload.key, resp)
		}
	}

	boardV2 := &cspb.DefaultData{Data: `{"board":{"stage":2,"score":800}}`}
	boardV2Content, boardV2Sign := encodeSave(t, boardV2)
	_, boardResp := doUpload(t, token, &cspb.SaveUploadReq{
		Key:         "board",
		BaseVersion: 1,
		NewVersion:  2,
		Sign:        boardV2Sign,
		Content:     boardV2Content,
	}, fmt.Sprintf("save-ind-board-v2-%s", uuid.NewString()[:8]))
	if boardResp.Code != 0 || boardResp.Data == nil || boardResp.Data.Version != 2 {
		t.Fatalf("更新 board v2 失败：response=%+v", boardResp)
	}

	downloadKeys := []string{"base", "item", "board"}
	_, downResp := doDownload(t, token, downloadKeys, fmt.Sprintf("save-ind-down-%s", uuid.NewString()[:8]))
	if downResp.Code != 0 || downResp.Data == nil || len(downResp.Data.Items) != len(downloadKeys) {
		t.Fatalf("下载失败：response=%+v", downResp)
	}

	expectedItems := []struct {
		key     string
		version int64
		content []byte
		sign    string
	}{
		{key: "base", version: 1, content: baseContent, sign: baseSign},
		{key: "item", version: 1, content: itemContent, sign: itemSign},
		{key: "board", version: 2, content: boardV2Content, sign: boardV2Sign},
	}
	for index, item := range downResp.Data.Items {
		expected := expectedItems[index]
		if item.Key != expected.key {
			t.Errorf("items[%d].key = %q，期望 %q", index, item.Key, expected.key)
		}
		if item.Version != expected.version {
			t.Errorf("key=%q version = %d，期望 %d", expected.key, item.Version, expected.version)
		}
		if item.Sign != expected.sign || !bytes.Equal(item.Content, expected.content) {
			t.Errorf("key=%q 未保留预期版本的存档内容", expected.key)
		}

		switch expected.key {
		case "base":
			if decoded := unmarshalBase(t, item.Content); !proto.Equal(decoded, baseV1) {
				t.Errorf("base 解码数据不匹配：got=%+v, want=%+v", decoded, baseV1)
			}
		case "item":
			if decoded := unmarshalDefaultData(t, item.Content); decoded.Data != itemV1.Data {
				t.Errorf("item 解码数据 = %q，期望 %q", decoded.Data, itemV1.Data)
			}
		case "board":
			if decoded := unmarshalDefaultData(t, item.Content); decoded.Data != boardV2.Data {
				t.Errorf("board 解码数据 = %q，期望 %q", decoded.Data, boardV2.Data)
			}
		}
	}
	t.Logf("Player 字段版本隔离验证通过：playerId=%d, base=1, item=1, board=2", playerID)
}

// TestSave_Upload_Conflict_PreservesLatest 验证版本冲突不会覆盖服务端最新存档。
func TestSave_Upload_Conflict_PreservesLatest(t *testing.T) {
	token, playerID := loginAsGuest(t)

	initialBase := &cspb.Base{Lv: 1}
	initialContent, initialSign := encodeSave(t, initialBase)
	_, initialResp := doUpload(t, token, &cspb.SaveUploadReq{
		Key:         "base",
		BaseVersion: 0,
		NewVersion:  1,
		Sign:        initialSign,
		Content:     initialContent,
	}, fmt.Sprintf("save-conflict-init-%s", uuid.NewString()[:8]))
	if initialResp.Code != 0 {
		t.Fatalf("初始上传失败：code=%d, message=%q", initialResp.Code, initialResp.Message)
	}

	conflictBase := &cspb.Base{Lv: 2}
	conflictContent, conflictSign := encodeSave(t, conflictBase)
	httpStatus, conflictResp := doUpload(t, token, &cspb.SaveUploadReq{
		Key:         "base",
		BaseVersion: 0,
		NewVersion:  2,
		Sign:        conflictSign,
		Content:     conflictContent,
	}, fmt.Sprintf("save-conflict-write-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if conflictResp.Code != 2409 {
		t.Fatalf("业务错误码 = %d，期望 2409（存档冲突），message=%q", conflictResp.Code, conflictResp.Message)
	}
	if conflictResp.Data == nil || conflictResp.Data.CurrentVersion != 1 {
		t.Fatalf("冲突响应 currentVersion = %v，期望 1", conflictResp.Data)
	}

	_, downloadResp := doDownload(t, token, []string{"base"}, fmt.Sprintf("save-conflict-down-%s", uuid.NewString()[:8]))
	if downloadResp.Code != 0 || downloadResp.Data == nil || len(downloadResp.Data.Items) != 1 {
		t.Fatalf("冲突后下载失败：response=%+v", downloadResp)
	}
	item := downloadResp.Data.Items[0]
	if item.Version != 1 {
		t.Errorf("冲突后版本 = %d，期望 1", item.Version)
	}
	if !bytes.Equal(item.Content, initialContent) {
		t.Error("冲突后存档内容被错误覆盖")
	}
	decoded := unmarshalBase(t, item.Content)
	if decoded.Lv != initialBase.Lv {
		t.Errorf("冲突后等级 = %d，期望 %d", decoded.Lv, initialBase.Lv)
	}
	t.Logf("存档冲突保护验证通过：playerId=%d, currentVersion=%d", playerID, item.Version)
}

// TestSave_Download_DeduplicatesKeys 验证重复请求 key 仅返回首次出现的一条存档。
func TestSave_Download_DeduplicatesKeys(t *testing.T) {
	token, playerID := loginAsGuest(t)

	httpStatus, resp := doDownload(t, token, []string{"base", "base"}, fmt.Sprintf("save-dedup-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("批量下载失败：code=%d, message=%q", resp.Code, resp.Message)
	}
	if resp.Data == nil || len(resp.Data.Items) != 1 {
		t.Fatalf("去重后 items 数量 = %d，期望 1", len(resp.GetData().GetItems()))
	}
	if resp.Data.Items[0].Key != "base" {
		t.Errorf("去重后 key = %q，期望 %q", resp.Data.Items[0].Key, "base")
	}
	t.Logf("重复 key 去重验证通过：playerId=%d", playerID)
}
