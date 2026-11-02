package logic

import (
	"testing"

	"kemiluo/pb/cspb"
)

// TestIsDeviceChanged 校验严格单端策略下的换端判定：
// 只有两次都上报了设备标识且完全一致才视为同一端；任一为空都按换端处理。
func TestIsDeviceChanged(t *testing.T) {
	cases := []struct {
		name     string
		previous string
		current  string
		want     bool
	}{
		{name: "同一设备", previous: "device-a", current: "device-a", want: false},
		{name: "不同设备", previous: "device-a", current: "device-b", want: true},
		{name: "上次未上报", previous: "", current: "device-b", want: true},
		{name: "本次未上报", previous: "device-a", current: "", want: true},
		{name: "两次都未上报", previous: "", current: "", want: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDeviceChanged(c.previous, c.current); got != c.want {
				t.Fatalf("isDeviceChanged(%q, %q) = %v，期望 %v", c.previous, c.current, got, c.want)
			}
		})
	}
}

// TestShouldKickReplacedSession 校验登录链路是否触发顶号的判定：
// 新注册玩家必然没有旧连接，不应触发；已有玩家按设备标识是否变化决定。
func TestShouldKickReplacedSession(t *testing.T) {
	cases := []struct {
		name     string
		isNew    bool
		previous string
		current  string
		want     bool
	}{
		{name: "新玩家不踢线", isNew: true, previous: "", current: "device-b", want: false},
		{name: "同设备重登不踢线", isNew: false, previous: "device-a", current: "device-a", want: false},
		{name: "换端登录踢线", isNew: false, previous: "device-a", current: "device-b", want: true},
		{name: "历史设备未知按换端处理", isNew: false, previous: "", current: "device-b", want: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldKickReplacedSession(c.isNew, c.previous, c.current); got != c.want {
				t.Fatalf("shouldKickReplacedSession(%v, %q, %q) = %v，期望 %v",
					c.isNew, c.previous, c.current, got, c.want)
			}
		})
	}
}

// TestBuildKickedOfflinePush 校验顶号通知负载包含新登录端的完整信息（平台/设备名/IP/设备ID）。
func TestBuildKickedOfflinePush(t *testing.T) {
	req := &cspb.LoginReq{
		Platform: "ios",
		ClientInfo: &cspb.ClientInfo{
			DeviceId:   "device-b",
			DeviceName: "iPhone 15",
		},
	}

	got := buildKickedOfflinePush(req, "203.0.113.7", 1730000000000)
	if got.LoginAt != 1730000000000 {
		t.Errorf("loginAt = %d，期望 %d", got.LoginAt, 1730000000000)
	}
	if got.Platform != "ios" {
		t.Errorf("platform = %q，期望 ios", got.Platform)
	}
	if got.DeviceName != "iPhone 15" {
		t.Errorf("deviceName = %q，期望 iPhone 15", got.DeviceName)
	}
	if got.DeviceId != "device-b" {
		t.Errorf("deviceId = %q，期望 device-b", got.DeviceId)
	}
	if got.Ip != "203.0.113.7" {
		t.Errorf("ip = %q，期望 203.0.113.7", got.Ip)
	}

	// req 为 nil 时不应 panic（防御内部调用方绕过 handler 校验）
	if empty := buildKickedOfflinePush(nil, "198.51.100.1", 1); empty.LoginAt != 1 || empty.Ip != "198.51.100.1" {
		t.Fatalf("nil 请求应只填充登录时间与 IP，实际为 %+v", empty)
	}
}
