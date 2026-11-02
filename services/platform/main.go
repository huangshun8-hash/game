package main

import (
	"log"

	"kemiluo/pkg/logx"
	"kemiluo/services/platform/internal/config"

	"go.uber.org/zap"
)

func main() {
	// 加载配置
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("配置校验失败: %v", err)
	}

	// 初始化日志
	logx.Init(cfg.Log)
	defer logx.Sync()

	// 替换Fiber内部日志为logx
	logx.SetupFiberLogger()

	// 组装并启动服务
	svc := newPlatformService(cfg)
	if err := svc.start(); err != nil {
		logx.Fatal("server exit", zap.Error(err))
	}
}
