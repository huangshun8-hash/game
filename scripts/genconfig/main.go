// genconfig 是从 MongoDB config_schema 集合自动生成 Go 配置代码的本地工具。
//
// 用法：
//
//	go run ./scripts/genconfig [flags]
//
// 常用示例：
//
//	# 使用默认参数（连接本地 MongoDB，输出到 generated/gdconfig）
//	go run ./scripts/genconfig
//
//	# 指定 MongoDB URI 和数据库
//	go run ./scripts/genconfig -uri mongodb://localhost:27017 -db gmt_db
//
//	# 指定输出目录
//	go run ./scripts/genconfig -out ./services/gamelogic/generated/gdconfig
//
// 生成文件结构：
//
//	generated/gdconfig/
//	├── conf_<table>.go   # 每张配置表的 Struct + Table 定义
//	├── manager.go        # ConfigManager（聚合所有配置表）
//	├── loader.go         # 统一加载接口
//	└── register.go       # 所有配置表注册代码
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"
	"unicode"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	// 命令行参数
	mongoURI := flag.String("uri", "mongodb://localhost:27017", "MongoDB 连接 URI")
	dbName := flag.String("db", "gconfig_db", "MongoDB 数据库名")
	outDir := flag.String("out", "generated/gdconfig", "Go 代码输出目录")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 连接 MongoDB
	client, err := mongo.Connect(options.Client().ApplyURI(*mongoURI))
	if err != nil {
		fatalf("连接 MongoDB 失败: %v", err)
	}
	defer client.Disconnect(ctx)

	if err := client.Ping(ctx, nil); err != nil {
		fatalf("Ping MongoDB 失败: %v", err)
	}

	// 从 config_schema 集合读取所有配置表结构
	schemas, err := loadSchemas(ctx, client.Database(*dbName))
	if err != nil {
		fatalf("读取 config_schema 失败: %v", err)
	}
	if len(schemas) == 0 {
		fatalf("config_schema 集合为空，请先上传配置")
	}

	fmt.Printf("发现 %d 张配置表，开始生成代码...\n", len(schemas))

	// 确保输出目录存在
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fatalf("创建输出目录失败: %v", err)
	}

	// 为每张配置表生成 conf_<table>.go
	for _, schema := range schemas {
		if err := genTableFile(*outDir, schema); err != nil {
			fatalf("生成 %s 失败: %v", schema.ID, err)
		}
		fmt.Printf("  ✓ conf_%s.go\n", strings.ToLower(schema.ID))
	}

	// 生成 manager.go、loader.go、register.go
	if err := genManagerFile(*outDir, schemas); err != nil {
		fatalf("生成 manager.go 失败: %v", err)
	}
	fmt.Println("  ✓ manager.go")

	if err := genLoaderFile(*outDir, schemas); err != nil {
		fatalf("生成 loader.go 失败: %v", err)
	}
	fmt.Println("  ✓ loader.go")

	if err := genRegisterFile(*outDir, schemas); err != nil {
		fatalf("生成 register.go 失败: %v", err)
	}
	fmt.Println("  ✓ register.go")

	fmt.Printf("\n代码生成完成，输出目录: %s\n", *outDir)
}

// ---  MongoDB Schema 结构体（与 gmt model 保持一致）---

// fieldDef 配置表字段定义
type fieldDef struct {
	Name    string `bson:"name"`
	Type    string `bson:"type"`
	Comment string `bson:"comment"`
}

// configSchema 配置表结构定义
type configSchema struct {
	ID         string        `bson:"_id"`
	Version    bson.ObjectID `bson:"version"`
	SchemaHash string        `bson:"schemaHash"`
	Fields     []fieldDef    `bson:"fields"`
}

// loadSchemas 从 config_schema 集合加载所有表结构，按表名排序（保证生成代码稳定）
func loadSchemas(ctx context.Context, db *mongo.Database) ([]configSchema, error) {
	cursor, err := db.Collection("config_schema").Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []configSchema
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].ID < results[j].ID
	})
	return results, nil
}

// --- 代码生成 ---

// goType 将 Excel 类型字符串映射为 Go 类型
func goType(excelType string) string {
	t := strings.TrimSpace(excelType)
	switch t {
	case "int":
		return "int32"
	case "long":
		return "int64"
	case "float":
		// MongoDB 所有浮点数均以 BSON double（float64）存储，映射为 float64 避免解码截断错误
		return "float64"
	case "double":
		return "float64"
	case "string":
		return "string"
	case "bool":
		return "bool"
	default:
		// 数组类型：[]int → []int32，[]string → []string，[]long → []int64
		if strings.HasPrefix(t, "[]") {
			inner := goType(t[2:])
			return "[]" + inner
		}
		// map 类型：map[int]int → map[int32]int32
		if strings.HasPrefix(t, "map[") {
			return mapGoType(t)
		}
		// 未知类型保留原样（自定义 Struct 由用户手动定义）
		return t
	}
}

// mapGoType 将 map[K]V 类型字符串转换为 Go map 类型
func mapGoType(t string) string {
	// 去掉 "map["，找到对应 "]"
	inner := t[4:]
	depth := 0
	splitIdx := -1
	for i, c := range inner {
		if c == '[' {
			depth++
		} else if c == ']' {
			if depth == 0 {
				splitIdx = i
				break
			}
			depth--
		}
	}
	if splitIdx < 0 {
		return t
	}
	k := goType(inner[:splitIdx])
	v := goType(inner[splitIdx+1:])
	return fmt.Sprintf("map[%s]%s", k, v)
}

// fieldNameToGo 将 Excel 字段名（camelCase 或 snake_case）转换为 Go 导出字段名
// 规则：首字母大写，保留 camelCase，snake_case 转 PascalCase
func fieldNameToGo(name string) string {
	if name == "" {
		return ""
	}
	// 处理 snake_case
	if strings.Contains(name, "_") {
		parts := strings.Split(name, "_")
		var sb strings.Builder
		for _, p := range parts {
			if p == "" {
				continue
			}
			runes := []rune(p)
			runes[0] = unicode.ToUpper(runes[0])
			sb.WriteString(string(runes))
		}
		return sb.String()
	}
	// camelCase → PascalCase（仅首字母大写）
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// idType 根据 Schema 推断 id 字段的 Go 类型，用于 Get(id T) 方法签名
func idType(schema configSchema) string {
	for _, f := range schema.Fields {
		if f.Name == "id" {
			return goType(f.Type)
		}
	}
	return "int32"
}

// structName 返回配置表的 Go Struct 名（如 ActivityConfig）
func structName(tableID string) string {
	return tableID + "Config"
}

// tableName 返回配置表的 Go Table 名（如 ActivityTable）
func tableName(tableID string) string {
	return tableID + "Table"
}

// --- 模板定义 ---

const tmplTableFile = `// 此文件由 genconfig 自动生成，请勿手动修改。
// 配置表：{{ .Schema.ID }}（版本 {{ .Schema.Version.Hex }}）
package gdconfig

import (
	"go.mongodb.org/mongo-driver/v2/bson"
)

func init() {
	Register("{{ .Schema.ID }}", apply{{ .Schema.ID }})
}

// apply{{ .Schema.ID }} 将全量行数据解码为 {{ .TableName }} 并原子写入 ConfigManager。
// 构建完整 table 后一次性替换，热更时读操作始终拿到完整快照，无并发风险。
func apply{{ .Schema.ID }}(rows []bson.Raw, m *ConfigManager) error {
	table := &{{ .TableName }}{data: make(map[{{ .IDType }}]*{{ .StructName }}, len(rows))}
	for _, raw := range rows {
		var cfg {{ .StructName }}
		if err := bson.Unmarshal(raw, &cfg); err != nil {
			return err
		}
		table.data[cfg.{{ .IDGoField }}] = &cfg
	}
	m.{{ .Schema.ID }}.Store(table)
	return nil
}

// {{ .StructName }} {{ .Schema.ID }} 配置表数据结构
type {{ .StructName }} struct {
{{- range .Schema.Fields }}
	{{ fieldNameToGo .Name }} {{ goType .Type }} ` + "`" + `bson:"{{ if eq .Name "id" }}_id{{ else }}{{ .Name }}{{ end }}"` + "`" + ` // {{ if .Comment }}{{ sanitizeComment .Comment }}{{ else }}{{ .Name }}{{ end }}
{{- end }}
}

// {{ .TableName }} {{ .Schema.ID }} 配置表内存访问层
type {{ .TableName }} struct {
	data map[{{ .IDType }}]*{{ .StructName }}
}

// Get 按 ID 获取单条配置，ID 不存在时返回 nil。
// 返回的是内部存储的只读引用，禁止修改其字段（修改会污染共享配置并造成并发数据竞争）。
func (t *{{ .TableName }}) Get(id {{ .IDType }}) *{{ .StructName }} {
	if t == nil || t.data == nil {
		return nil
	}
	return t.data[id]
}

// GetAll 获取所有配置数据的浅拷贝：新建 map 复制键值，调用方对返回 map 的增删不会影响内部数据。
// 注意：map 中的值仍为只读配置结构体指针，禁止修改其字段（修改会污染共享配置并造成并发数据竞争）。
func (t *{{ .TableName }}) GetAll() map[{{ .IDType }}]*{{ .StructName }} {
	if t == nil || t.data == nil {
		return nil
	}
	result := make(map[{{ .IDType }}]*{{ .StructName }}, len(t.data))
	for k, v := range t.data {
		result[k] = v
	}
	return result
}

// Count 返回配置条数
func (t *{{ .TableName }}) Count() int {
	if t == nil || t.data == nil {
		return 0
	}
	return len(t.data)
}
`

const tmplManagerFile = `// 此文件由 genconfig 自动生成，请勿手动修改。
package gdconfig

import (
	"context"
	"fmt"
	"sync/atomic"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// PostLoadHook 配置加载完成后的钩子函数类型
type PostLoadHook func(m *ConfigManager) error

// ConfigManager 配置管理器，聚合所有配置表的内存访问层。
// 每张配置表以 atomic.Pointer 存储，支持热更时无锁原子替换，读操作始终拿到完整快照。
// 仅维护正式配置（prod），热更时通过 SwapTables 将新实例的各表原子写入当前实例。
type ConfigManager struct {
{{- range .Schemas }}
	// {{ .ID }} 配置表（原子指针，支持并发热更）
	{{ .ID }} atomic.Pointer[{{ tableName .ID }}]
{{- end }}

	// postLoadHooks 加载完成后依次执行的钩子列表
	postLoadHooks []PostLoadHook
}

// AddPostLoadHook 注册加载后钩子，LoadAll 完成后按注册顺序依次调用
func (m *ConfigManager) AddPostLoadHook(hook PostLoadHook) {
	m.postLoadHooks = append(m.postLoadHooks, hook)
}

// LoadAll 从 MongoDB 加载所有已注册配置表数据到内存，完成后依次执行 postLoadHooks。
// db: 配置数据所在的 MongoDB Database（通常为 gconfig_db，取决于部署架构）
func (m *ConfigManager) LoadAll(ctx context.Context, db *mongo.Database) error {
	// 遍历注册表，统一执行 Find + apply
	for _, entry := range registry {
		if err := loadEntry(ctx, db, entry, m); err != nil {
			return fmt.Errorf("加载 %s 配置失败: %w", entry.tableName, err)
		}
	}

	// 加载完成，依次执行钩子
	for _, hook := range m.postLoadHooks {
		if err := hook(m); err != nil {
			return fmt.Errorf("执行加载后钩子失败: %w", err)
		}
	}
	return nil
}

// SwapTables 将 src 中所有配置表的快照原子写入 dst。
// 用于热更场景：先将新配置加载到临时 src 实例，再逐表原子替换到正在服务的 dst 实例，
// 整个过程并发安全，持有 dst 指针的调用方无需任何感知。
func SwapTables(dst, src *ConfigManager) {
{{- range .Schemas }}
	dst.{{ .ID }}.Store(src.{{ .ID }}.Load())
{{- end }}
}
`

const tmplLoaderFile = `// 此文件由 genconfig 自动生成，请勿手动修改。
// 提供统一的配置加载接口，供各服务在启动时调用。
package gdconfig

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// NewConfigManager 创建一个空的 ConfigManager 实例
func NewConfigManager() *ConfigManager {
	return &ConfigManager{}
}

// Load 从指定 MongoDB Database 加载所有已注册配置表到内存，完成后执行所有已注册的 postLoadHooks。
//
// 示例：
//
//	cfg := gdconfig.NewConfigManager()
//	cfg.AddPostLoadHook(func(m *gdconfig.ConfigManager) error {
//	    // 加载完成后的初始化逻辑
//	    return nil
//	})
//	if err := gdconfig.Load(ctx, db, cfg); err != nil {
//	    log.Fatal(err)
//	}
//	product := cfg.IapProductGlobal.Load().Get(1)
func Load(ctx context.Context, db *mongo.Database, manager *ConfigManager) error {
	return manager.LoadAll(ctx, db)
}
`

const tmplRegisterFile = `// 此文件由 genconfig 自动生成，请勿手动修改。
// 维护全局配置表 loader 注册表，各 conf_xxx.go 在 init() 中调用 Register 完成注册。
package gdconfig

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// loaderEntry 单张配置表的加载器条目
type loaderEntry struct {
	// tableName 配置表名称，用于错误提示
	tableName string
	// collName MongoDB 集合名
	collName string
	// apply 将全量行数据解码并原子写入 ConfigManager 对应字段
	apply func(rows []bson.Raw, m *ConfigManager) error
}

// registry 全局 loader 注册表，按注册顺序执行
var registry []loaderEntry

// Register 注册一张配置表的 loader，在各 conf_xxx.go 的 init() 中调用。
// tableName: 配置表名称（用于错误提示），MongoDB 集合名自动取其全小写形式
// apply:     接收全量行数据，完成解码后原子写入 ConfigManager 对应字段
func Register(
	tableName string,
	apply func(rows []bson.Raw, m *ConfigManager) error,
) {
	registry = append(registry, loaderEntry{
		tableName: tableName,
		collName:  strings.ToLower(tableName),
		apply:     apply,
	})
}

// loadEntry 执行单张配置表的完整加载流程：Find → 收集全量行 → 一次性 apply
// 全量收集完成后再调用 apply，确保 apply 内部可以安全地原子替换整张表指针。
func loadEntry(ctx context.Context, db *mongo.Database, entry loaderEntry, m *ConfigManager) error {
	cursor, err := db.Collection(entry.collName).Find(ctx, bson.D{})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	// 收集全量行，避免 apply 中途持有游标
	var rows []bson.Raw
	for cursor.Next(ctx) {
		// cursor.Current 在下次 Next 后会被复用，需要显式拷贝
		raw := make(bson.Raw, len(cursor.Current))
		copy(raw, cursor.Current)
		rows = append(rows, raw)
	}
	if err := cursor.Err(); err != nil {
		return err
	}

	return entry.apply(rows, m)
}

// RegisteredTables 返回已注册的所有配置表名列表（按注册顺序）
func RegisteredTables() []string {
	names := make([]string, 0, len(registry))
	for _, e := range registry {
		names = append(names, e.tableName)
	}
	return names
}
`

// --- 生成函数 ---

type tableFileTmplData struct {
	GenTime    string
	Schema     configSchema
	StructName string
	TableName  string
	IDType     string
	IDGoField  string
}

func genTableFile(outDir string, schema configSchema) error {
	funcMap := template.FuncMap{
		"goType":          goType,
		"fieldNameToGo":   fieldNameToGo,
		"toLower":         strings.ToLower,
		"sanitizeComment": sanitizeComment,
	}
	tmpl, err := template.New("table").Funcs(funcMap).Parse(tmplTableFile)
	if err != nil {
		return err
	}

	data := tableFileTmplData{
		GenTime:    time.Now().UTC().Format("2006-01-02 15:04:05"),
		Schema:     schema,
		StructName: structName(schema.ID),
		TableName:  tableName(schema.ID),
		IDType:     idType(schema),
		IDGoField:  fieldNameToGo("id"),
	}

	return writeGoFile(outDir, fmt.Sprintf("conf_%s.go", strings.ToLower(schema.ID)), tmpl, data)
}

type managerFileTmplData struct {
	GenTime string
	Schemas []configSchema
}

func genManagerFile(outDir string, schemas []configSchema) error {
	funcMap := template.FuncMap{
		"tableName": tableName,
	}
	tmpl, err := template.New("manager").Funcs(funcMap).Parse(tmplManagerFile)
	if err != nil {
		return err
	}
	data := managerFileTmplData{
		GenTime: time.Now().UTC().Format("2006-01-02 15:04:05"),
		Schemas: schemas,
	}
	return writeGoFile(outDir, "manager.go", tmpl, data)
}

func genLoaderFile(outDir string, schemas []configSchema) error {
	tmpl, err := template.New("loader").Parse(tmplLoaderFile)
	if err != nil {
		return err
	}
	data := map[string]any{
		"GenTime": time.Now().UTC().Format("2006-01-02 15:04:05"),
		"Schemas": schemas,
	}
	return writeGoFile(outDir, "loader.go", tmpl, data)
}

func genRegisterFile(outDir string, schemas []configSchema) error {
	tmpl, err := template.New("register").Parse(tmplRegisterFile)
	if err != nil {
		return err
	}
	// register.go 内容固定，不依赖 schemas
	return writeGoFile(outDir, "register.go", tmpl, nil)
}

// writeGoFile 渲染模板、格式化 Go 代码并写入文件
func writeGoFile(dir, filename string, tmpl *template.Template, data any) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("渲染模板 %s 失败: %w", filename, err)
	}

	// 使用 go/format 格式化生成的代码
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// 格式化失败说明生成的代码存在语法错误，返回 error 让调用方以非零退出，避免产出无法编译的代码
		return fmt.Errorf("格式化 %s 失败: %w\n原始内容:\n%s", filename, err, buf.String())
	}

	path := filepath.Join(dir, filename)
	return os.WriteFile(path, formatted, 0644)
}

// sanitizeComment 去除注释中的换行符，合并为单行
func sanitizeComment(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimSpace(s)
}

// fatalf 打印错误并退出
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	os.Exit(1)
}
