package httpx

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"testing"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/authx"
	"kemiluo/pkg/packetx"

	"github.com/gofiber/fiber/v3"
	"google.golang.org/protobuf/proto"
)

// testPacketKey 测试用固定收发包秘钥（32 字节 base64），模拟登录时下发给玩家的秘钥。
const testPacketKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

// newPacketTestApp 构造一个带收发包加解密语义的测试应用：
// 携带 X-Test-SaveKey 头的请求会被注入含该 base64 秘钥的 JWT Claims，
// 从而让 Bind/OK 走加解密分支；无此头时 Claims 为 nil，走明文（模拟登录）。
// POST /echo 绑定 PlayerQueryReq，并把其 playerId 回填到 ServerTimeResp.data.serverTime 后返回，便于断言请求被正确解密。
func newPacketTestApp(t *testing.T) *fiber.App {
	t.Helper()
	app := fiber.NewWithCustomCtx(func(app *fiber.App) fiber.CustomCtx {
		return NewCtx(app)
	}, fiber.Config{ErrorHandler: DefaultErrorHandler})

	app.Use(func(c fiber.Ctx) error {
		if sk := c.Get("X-Test-SaveKey"); sk != "" {
			c.Locals("claims", &authx.Claims{PacketKey: sk})
		}
		return c.Next()
	})

	app.Post("/echo", ToFiberHandler(func(c Context) error {
		req, err := Bind[cspb.PlayerQueryReq](c)
		if err != nil {
			return NewAppError(400, "bad request")
		}
		return OK(c, &cspb.ServerTimeResp{
			Data: &cspb.ServerTimeData{ServerTime: req.GetPlayerId()},
		})
	}))

	return app
}

// TestBindOK_ProtoEncrypted 验证携带收发包秘钥时：请求体需加密、响应体被加密，且明文往返正确。
func TestBindOK_ProtoEncrypted(t *testing.T) {
	app := newPacketTestApp(t)
	key, err := base64.StdEncoding.DecodeString(testPacketKey)
	if err != nil {
		t.Fatalf("解码测试秘钥失败: %v", err)
	}

	// 客户端加密请求体
	wireReq, err := packetx.Encode(&cspb.PlayerQueryReq{PlayerId: 123456}, key)
	if err != nil {
		t.Fatalf("加密请求失败: %v", err)
	}

	httpReq := newProtoRequest(t, "/echo", wireReq, testPacketKey)
	resp, err := app.Test(httpReq)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// 响应体应为密文：直接 proto.Unmarshal 应拿不到正确的 serverTime（明文比对失败）
	var plainAttempt cspb.ServerTimeResp
	if proto.Unmarshal(body, &plainAttempt) == nil && plainAttempt.GetData().GetServerTime() == 123456 {
		t.Fatal("响应体未加密：明文直接反序列化即得到正确值")
	}

	// 用秘钥解密后应还原正确的 serverTime
	decoded, err := packetx.Decode(body, key, func() *cspb.ServerTimeResp { return new(cspb.ServerTimeResp) })
	if err != nil {
		t.Fatalf("解密响应失败: %v", err)
	}
	if got := decoded.GetData().GetServerTime(); got != 123456 {
		t.Fatalf("解密后 serverTime = %d，期望 123456（请求未被正确解密）", got)
	}
}

// TestBindOK_ProtoPlaintextWhenNoKey 验证无收发包秘钥时（如登录）请求/响应保持明文，行为不变。
func TestBindOK_ProtoPlaintextWhenNoKey(t *testing.T) {
	app := newPacketTestApp(t)

	plainReq, err := proto.Marshal(&cspb.PlayerQueryReq{PlayerId: 777})
	if err != nil {
		t.Fatalf("序列化请求失败: %v", err)
	}

	// 不携带 X-Test-SaveKey，Claims 为 nil，走明文分支
	httpReq := newProtoRequest(t, "/echo", plainReq, "")
	resp, err := app.Test(httpReq)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result cspb.ServerTimeResp
	if err := proto.Unmarshal(body, &result); err != nil {
		t.Fatalf("明文响应反序列化失败: %v", err)
	}
	if got := result.GetData().GetServerTime(); got != 777 {
		t.Fatalf("明文 serverTime = %d，期望 777", got)
	}
}

// TestFail_ProtoEncrypted 验证错误响应（Fail）在携带秘钥时同样被加密。
func TestFail_ProtoEncrypted(t *testing.T) {
	app := fiber.NewWithCustomCtx(func(app *fiber.App) fiber.CustomCtx {
		return NewCtx(app)
	}, fiber.Config{ErrorHandler: DefaultErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		if sk := c.Get("X-Test-SaveKey"); sk != "" {
			c.Locals("claims", &authx.Claims{PacketKey: sk})
		}
		return c.Next()
	})
	app.Post("/boom", ToFiberHandler(func(c Context) error {
		return NewAppError(2001, "boom")
	}))

	key, _ := base64.StdEncoding.DecodeString(testPacketKey)
	httpReq := newProtoRequest(t, "/boom", nil, testPacketKey)
	resp, err := app.Test(httpReq)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	// 密文直接反序列化不应得到 code=2001
	var plainAttempt cspb.ErrorResp
	if proto.Unmarshal(body, &plainAttempt) == nil && plainAttempt.GetCode() == 2001 {
		t.Fatal("错误响应未加密")
	}
	decoded, err := packetx.Decode(body, key, func() *cspb.ErrorResp { return new(cspb.ErrorResp) })
	if err != nil {
		t.Fatalf("解密错误响应失败: %v", err)
	}
	if decoded.GetCode() != 2001 {
		t.Fatalf("解密后 code = %d，期望 2001", decoded.GetCode())
	}
}

// newProtoRequest 构造 protobuf 请求；saveKey 非空时注入 X-Test-SaveKey 头以启用加解密分支。
func newProtoRequest(t *testing.T, target string, body []byte, saveKey string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}
	req.Header.Set(fiber.HeaderContentType, MIMEApplicationProtobuf)
	if saveKey != "" {
		req.Header.Set("X-Test-SaveKey", saveKey)
	}
	return req
}
