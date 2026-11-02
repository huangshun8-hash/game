# genconfig — 配置代码生成工具

从 MongoDB `config_schema` 集合自动生成 Go 配置 Struct、Table、Manager 代码。

## 用法

```bash
# 使用默认参数（连接本地 MongoDB，输出到 generated/gdconfig）
go run ./scripts/genconfig

# 指定参数
go run ./scripts/genconfig \
  -uri mongodb://localhost:27017 \
  -db  gconfig_db \
  -out ./generated/gdconfig
```

## 命令行参数

| 参数   | 默认值                        | 说明                         |
|--------|-------------------------------|------------------------------|
| `-uri` | `mongodb://localhost:27017`   | MongoDB 连接 URI             |
| `-db`  | `gconfig_db`                  | config_schema 所在的数据库名  |
| `-out` | `generated/gdconfig`          | Go 代码输出目录              |

## 生成文件说明

```
generated/gdconfig/
├── conf_activity.go   # 活动配置：Struct + Table + init() 注册 loader + applyXxx 函数
├── conf_item.go       # 物品配置：同上
├── ...                # 每张配置表一个文件
├── manager.go         # ConfigManager — 聚合所有配置表，LoadAll 遍历注册表加载，支持 PostLoadHook
├── loader.go          # NewConfigManager + Load 工具函数
└── register.go        # 全局 loader 注册表（Register / RegisteredTables）
```

## 架构说明

采用**注册-加载-钩子**模式：

1. **注册**：每个 `conf_xxx.go` 在 `init()` 中调用 `Register(tableName, loadFn)` 完成自注册
2. **加载**：启动时调用 `Load(ctx, db, cfg)`，内部遍历注册表逐表加载
3. **钩子**：通过 `cfg.AddPostLoadHook(fn)` 注册加载完成后的回调，`LoadAll` 完成后按注册顺序依次调用

## 生成代码用法

```go
import (
    "kemiluo/generated/gdconfig"
    "go.mongodb.org/mongo-driver/v2/mongo"
)

// 服务启动时加载
cfg := gdconfig.NewConfigManager()

// 可选：注册加载后钩子
cfg.AddPostLoadHook(func(m *gdconfig.ConfigManager) error {
    // 例如：基于配置构建辅助索引
    return nil
})

if err := gdconfig.Load(ctx, db, cfg); err != nil {
    log.Fatal(err)
}

// 业务代码访问
activity := cfg.Activity.Get(1)
if activity == nil {
    // 配置不存在
}

allItems := cfg.Item.GetAll()
```

## 触发时机

| 场景                 | 是否需要重新生成 | 是否需要重新编译 |
|----------------------|:----------------:|:----------------:|
| 仅修改配置数值       | ✗                | ✗ （热更新）     |
| 新增/删除/修改字段   | ✓                | ✓                |

Schema 变更（`isSchemaChanged=true`）后，需要：
1. 运行本工具重新生成代码
2. 重新编译并部署服务端
