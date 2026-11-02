# 传输加解密说明

> 适用范围：客户端以 protobuf 模式调用 HTTP 接口时的请求体与响应体传输。

---

## 适用范围

- 仅适用于 `Content-Type: application/x-protobuf` 或 `application/protobuf`
- JSON 模式下，请求体与响应体均按普通 JSON 传输，不使用本规则
- 该机制是**全局协议能力**，不属于某个单独业务接口

---

## 基本规则

- 登录成功后，服务端会在 `LoginData.saveKey` 中下发玩家专属 `saveKey`
- `saveKey` 为 `bytes` 字段：
  - protobuf 模式下为原始 32 字节二进制
  - JSON 模式下按 `bytes` 规则表现为 base64 字符串
- 客户端在 protobuf 模式下发送请求或解析响应时，应使用该 `saveKey` 做传输层加解密

---

## 编码流程

protobuf 模式下，请求体与响应体使用以下流程：

1. 将业务消息序列化为 protobuf 线格式字节
2. 使用 `saveKey` 进行 AES-CBC 加密
3. 最终传输内容为二进制字节流

### 说明

- 每次加密都会带随机 IV
- 因为使用随机 IV，同一业务数据多次加密后的二进制结果可能不同，这是正常现象
- 客户端无需额外做 base64；只有在 JSON 中展示 `bytes` 字段时，才按 JSON 规则显示为 base64 字符串

---

## 与业务数据的关系

- 该机制只负责 **HTTP protobuf 请求 / 响应 body** 的传输保护
- 业务字段本身的含义、签名、压缩格式，仍以各接口文档为准
- 例如存档接口中的 `content` 字段含义，见 `docs/save_api.md`

---

## 客户端接入建议

- 登录成功后缓存当前玩家的 `saveKey`
- 使用 protobuf 模式请求接口时，统一在网络层处理加解密
- 当玩家重新登录或切换账号时，使用最新返回的 `saveKey`
