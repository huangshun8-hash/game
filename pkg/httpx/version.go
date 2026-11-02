package httpx

// Version 服务版本号，编译时通过 ldflags 注入，例如：
//
//	go build -ldflags "-X kemiluo/pkg/httpx.Version=20260821_002" ./services/gamelogic
//
// 默认值为 dev；该值由 /version 接口返回，用于发布后核对实际运行的版本。
var Version = "dev"
