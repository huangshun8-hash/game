# Go 服务生产部署方案：软链接原子切换 + Docker Compose

> 核心思路：不把二进制打包进 Docker 镜像，使用宿主机版本目录 + `current` 软链接做版本原子切换；容器通过只读 volume 挂载软链接目录。
> 优点：版本隔离、快速回滚、不会覆盖正在运行中的程序文件；支持脚本自动化发布。
> 注意：该模式为开发/发布技巧，镜像仅使用 alpine 基础系统，业务二进制存宿主机。

---

## 一、方案可行性评估

### 1.1 总体结论

**方案可行，适合本项目的多服务微服务架构。** 原始方案针对单服务设计，本仓库包含三个服务（platform / gamelogic / gmt），已按「releases/<svc>/<version> + current/<svc>」结构适配，并配套实现了 `build.sh` / `deploy.sh` / `rollback.sh` 三个脚本。

### 1.2 逐项评估

| 评估维度 | 结论 | 说明 |
| --- | --- | --- |
| 原子切换 | ✅ 可行 | `ln -sfn` 是原子的，切换瞬间完成，新旧版本互不干扰 |
| 运行中覆盖风险 | ✅ 已规避 | 新版本存放在独立 release 目录，不覆盖正在运行的文件 |
| 失败回滚 | ✅ 快 | 只需切换软链接 + 重启容器，秒级完成 |
| Docker 重新加载 | ✅ 可行 | 容器每次启动（含 `restart`）都会重新执行 bind mount，内核挂载时解析软链接当前指向，加载新版本二进制 |
| 版本核对 | ✅ 已补齐 | 本方案要求 `GET /version`，已在 `pkg/httpx` 增加该端点（ldflags 注入版本号），并加入 gamelogic/gmt 的 JWT 跳过列表 |
| 配置管理 | ✅ 适配 | 本项目配置走环境变量（viper 自动绑定）+ YAML 文件（`CONFIG_DIR` + `APP_ENV`），无需 `--config` 参数 |
| 日志目录 | ⚠️ 需适配 | 容器内 `/app` 为只读，日志默认目录 `./logs` 需改为映射到可写卷（`LOG_DIR=/logs`） |
| 多服务隔离 | ✅ 适配 | 每个服务独立版本目录 + 独立软链接，互不影响 |

### 1.3 关键设计确认

1. **为什么 `docker compose restart` 能加载新版本**：bind mount 的源路径在容器每次启动时由内核重新解析，软链接指向哪个目录，容器就挂载哪个目录。切换软链接后重启容器即生效，无需重建镜像或容器。
2. **为什么用 `restart` 而不是 `up -d`**：`restart` 只重新启动容器进程，不重建容器，速度快；bind mount 重新建立时自动跟随软链接新指向。
3. **为什么配置不随版本发布**：配置文件独立于二进制，发布脚本只切换二进制版本，配置变更单独修改后重启服务，避免「新版本回滚时配置也被回滚」的混乱。

### 1.4 已评估的风险点

- **二进制不可执行**：脚本先校验 `-x` 再切换，防止发布失败。
- **健康检查误判**：健康检查轮询 + 版本核对双重校验；新版本无 `/version` 接口时自动跳过版本核对，不阻塞发布。
- **回滚不健康**：脚本在回滚后再次健康检查，异常时告警人工介入。
- **日志目录只读**：compose 必须为日志单独挂载可写目录，否则服务启动即失败（已在本文档 compose 中处理）。

---

## 二、目录结构（服务器）

```
/data/
├── servers/                         # 服务部署根目录（GAME_SERVER_DIR）
│   ├── compose.yaml                 # Docker Compose 编排（三服务）
│   ├── deploy.log                   # 发布日志（由部署脚本自动追加）
│   ├── current/                     # 当前生效版本软链接目录
│   │   ├── platform -> releases/platform/<version>
│   │   ├── gamelogic -> releases/gamelogic/<version>
│   │   └── gmt -> releases/gmt/<version>
│   ├── releases/                    # 所有历史版本二进制（编译后可执行程序存放处）
│   │   ├── platform/
│   │   │   └── 20260821_001/
│   │   │       └── platform
│   │   ├── gamelogic/
│   │   │   └── 20260821_001/
│   │   │       └── gamelogic
│   │   └── gmt/
│   │       └── 20260821_001/
│   │           └── gmt
│   ├── config/                      # 生产配置文件目录（只读挂载）
│   │   ├── platform_prod.yaml
│   │   ├── gamelogic_prod.yaml
│   │   ├── gmt_prod.yaml
│   │   └── server.env               # 环境变量（密钥等敏感信息；与三个 *_prod.yaml 均设权限 600）
│   └── scripts/                     # 发布脚本（安装脚本会把 deploy.sh/rollback.sh 同步到此处；install.sh 与 install_docker.sh 为首装一次性脚本，不会同步）
│       ├── deploy.sh
│       └── rollback.sh
├── web/                             # GMT 前端 Vue 打包产物（只读挂载到 gmt 容器 /data/web）
│   ├── index.html
│   └── assets/
└── logs/                            # 日志目录（容器可写挂载，挂到宿主机 /data/logs）
    ├── platform/
    ├── gamelogic/
    └── gmt/
```

版本目录命名规则：`YYYYMMDD_序号`，如 `20260821_001`、`20260821_002`。同一服务内版本号须递增，保证自动回滚时能选中正确的上一版本。

---

## 三、compose.yaml（本仓库多服务适配版）

> 可直接使用的正式模板已放到仓库 `scripts/compose.yaml`，`install.sh` 会把它同步为服务器的 `/data/servers/compose.yaml`；下述示例与其保持一致（三个服务均声明 `pull_policy: if_not_present`，本地已有镜像即不再联网拉取，部署与网络解耦）。

```yaml
name: game-server

services:
  platform:
    image: alpine:3.22
    container_name: game-platform
    restart: unless-stopped
    working_dir: /app
    volumes:
      # 只读挂载软链接目录：切换版本只改宿主机软链接，不动容器配置
      - ./current/platform:/app:ro
      # 配置目录只读挂载（{svc}_prod.yaml + server.env）
      - ./config:/config:ro
      # 日志目录可写挂载（按需求挂到宿主机 /data/logs）
      - /data/logs:/logs
    environment:
      APP_ENV: prod
      CONFIG_DIR: /config
      LOG_DIR: /logs
      MONGO_URI: mongodb://<mongo-host>:27017/?replicaSet=rs0
      REDIS_ADDR: <redis-host>:6379
      GAMELOGIC_URL: http://gamelogic:8081
    env_file:
      - ./config/server.env
    command: ["/app/platform"]
    ports:
      # 仅绑本机回环：本方案统一由宿主机 nginx 反代对外；用云 SLB/ALB 直连后端时改为 0.0.0.0（见 5.8）
      - "127.0.0.1:8080:8080"

  gamelogic:
    image: alpine:3.22
    container_name: game-gamelogic
    restart: unless-stopped
    working_dir: /app
    volumes:
      - ./current/gamelogic:/app:ro
      - ./config:/config:ro
      # 日志目录可写挂载（按需求挂到宿主机 /data/logs）
      - /data/logs:/logs
    environment:
      APP_ENV: prod
      CONFIG_DIR: /config
      LOG_DIR: /logs
      MONGO_URI: mongodb://<mongo-host>:27017/?replicaSet=rs0
      REDIS_ADDR: <redis-host>:6379
      PLATFORM_URL: http://platform:8080
    env_file:
      - ./config/server.env
    command: ["/app/gamelogic"]
    ports:
      # 仅绑本机回环：外部网络不可直达，统一经宿主机 nginx 反代；用云 SLB/ALB 直连后端时改为 0.0.0.0（见 5.8）
      - "127.0.0.1:8081:8081"

  gmt:
    image: alpine:3.22
    container_name: game-gmt
    restart: unless-stopped
    working_dir: /app
    volumes:
      - ./current/gmt:/app:ro
      - ./config:/config:ro
      # 日志目录可写挂载（按需求挂到宿主机 /data/logs）
      - /data/logs:/logs
      # GMT 前端 Vue 打包产物只读挂载：宿主机 /data/web -> 容器 /data/web，需与 GMT_WEB_DIST 一致
      - /data/web:/data/web:ro
    environment:
      APP_ENV: prod
      CONFIG_DIR: /config
      LOG_DIR: /logs
      MONGO_URI: mongodb://<mongo-host>:27017/?replicaSet=rs0
      REDIS_ADDR: <redis-host>:6379
      PLATFORM_URL: http://platform:8080
      GAMELOGIC_URL: http://gamelogic:8081
      # Vue 前端静态托管目录（容器内路径，须与上方 volume 的容器侧路径一致）；
      # 为空则 GMT 仅作纯 API 服务，不托管前端页面
      GMT_WEB_DIST: /data/web
    env_file:
      - ./config/server.env
    command: ["/app/gmt"]
    ports:
      # 仅绑本机回环：管理后台切勿裸奔公网，统一经 nginx 反代 + 来源白名单；用云 SLB/ALB 直连后端时改为 0.0.0.0 并务必限制来源
      - "127.0.0.1:8082:8082"
```

> 关键点：
> 1. `./current/<svc>:/app:ro` —— 容器永远读取对应软链接指向的目录，切换版本只修改宿主机软链接。
> 2. `APP_ENV=prod` + `CONFIG_DIR=/config` —— 服务启动时加载 `/config/<svc>_prod.yaml`。
> 3. `LOG_DIR=/logs` —— 日志写入宿主机 `/data/logs`（`/app` 只读，必须单独挂载可写目录）。日志轮转与超期自动删除由进程内 `pkg/logx` 负责，无需宿主机 logrotate/cron（详见十一.12）。
> 4. 服务间地址使用 compose 网络内的服务名（`http://platform:8080` 等），不依赖宿主机端口。
> 5. Mongo / Redis 为外部基础设施（云产品或自建），通过环境变量注入连接串；如需容器化可另写 infra compose，本方案聚焦应用二进制发布。
> 6. **端口暴露与接入拓扑**：三个服务默认均只绑 `127.0.0.1`，外部网络（含云 SLB/ALB）**无法直连**——本方案统一由宿主机 nginx 反代对外（见 5.8，`install.sh --with-nginx`），安全组只放行 nginx 的 80/443。若改用云 SLB/ALB 直连后端，需把对应服务端口改为 `0.0.0.0:<port>:<port>`（或内网网卡 IP）后 `docker compose up -d <svc>` 重建容器；此时 `8082`（gmt 管理后台）务必用 SLB 监听器/安全组限制来源，切勿直接暴露公网。
> 7. **GMT 前端静态托管**：gmt 服务通过 `GMT_WEB_DIST` 指向容器内前端产物目录（本方案为 `/data/web`），并把宿主机 `/data/web` 只读挂载到该路径实现同源托管；根路径 `/` 返回 `index.html`，`/api/*` 走后端路由，其余走 SPA history 回退。`GMT_WEB_DIST` 为空（或不挂载产物）时 gmt 退化为纯 API 服务，访问 `/` 会因 JWT 鉴权返回 `10401 missing authorization token`。前端产物准备见第五章「5.6」。

---

## 四、构建产物（开发机 / CI）

### 4.1 Linux / macOS 开发机

```bash
# 编译全部服务，版本号自动生成 YYYYMMDD_HHMM（与服务器 release 目录命名一致）
./scripts/build.sh

# 编译全部服务并指定版本号（推荐：发布时使用与 release 目录一致的序号版本）
./scripts/build.sh -v 20260821_002

# 只编译部分服务
./scripts/build.sh -v 20260821_002 gamelogic gmt
```

构建产物：`dist/<svc>` 三个静态二进制，以及 `dist/<version>_manifest.txt`（sha256 版本清单）。

> 版本号通过 `-ldflags "-X kemiluo/pkg/httpx.Version=<version>"` 注入，服务 `GET /version` 返回该值，用于发布后核对实际运行版本。

### 4.2 Windows 开发机

```powershell
# 编译全部服务
.\scripts\build.ps1

# 编译全部服务并指定版本号
.\scripts\build.ps1 -Version "20260821_002"

# 只编译部分服务
.\scripts\build.ps1 -Version "20260821_002" -Services gamelogic,gmt
```

### 4.3 本仓库新增内容（本次适配）

- `pkg/httpx/version.go`：定义 `Version` 包级变量，由 ldflags 注入。
- `pkg/httpx/server.go`：新增 `GET /version` 端点，返回 `{service, version, time}`（毫秒时间戳）。
- `services/gamelogic/service.go`、`services/gmt/service.go`：将 `/version` 加入 JWT 中间件跳过列表，保证发布脚本可以免鉴权核对版本。

---

## 五、第一次部署流程

### 5.1 首次部署与后续更新·操作流程速查

以下为可直接照做的端到端流程（命令标注「本机」/「服务器」执行位置）。脚本行为细节见第七、八章，手工步骤细节见后续小节。

#### 首次部署（全新云主机 → 服务上线）

| 步骤 | 位置 | 操作 |
| --- | --- | --- |
| 1. 代码就绪 | 本机 | 代码合并、`go test` 通过 |
| 2. 编译产物 | 本机 | `./scripts/build.sh -v 20260821_001`（产物在 `dist/`） |
| 3. 准备配置 | 本机 | 按 `configs/` 下样例编写 `platform_prod.yaml` / `gamelogic_prod.yaml` / `gmt_prod.yaml` 与 `server.env`（密钥显式设置） |
| 4. 传输 | 本机 | 上传 scripts 五件套（`scp -r scripts root@<服务器IP>:~/`，须同目录）+ 二进制 `scp dist/platform dist/gamelogic dist/gmt root@<IP>:/tmp/`；配置文件随后在服务器上填写（完整清单见 5.2） |
| 5. 云主机预装环境 | 服务器 | `sudo ./scripts/install.sh --mirror aliyun`（装 Docker、建 `/data/servers` 与 `/data/logs`、同步 compose.yaml 与部署脚本、预拉取镜像；非 root 运维追加 `--owner ubuntu`，见 5.5） |
| 6. 放置配置 | 服务器 | 配置放入 `/data/servers/config/`，四个敏感文件执行 `chmod 600`（详见 5.5；方案 B 另需确认 ubuntu 已加入 docker 组） |
| 7. 初始化版本与软链接 | 服务器 | 见 5.4：三个服务分别建版本目录、投放二进制、`ln -sfn` 建立 `current/<svc>` 软链接 |
| 8. 准备 GMT 前端产物（可选） | 服务器 | 见 5.6：如需同源托管后台页面，上传 Vue 产物到宿主机 `/data/web`（须含 `index.html`）；纯 API 模式可跳过 |
| 9. 首次拉起容器 | 服务器 | `cd /data/servers && docker compose up -d`——**首次必须用 `up -d` 创建容器**，后续更新才用 `restart` |
| 10. 验收 | 服务器 | `curl -f http://127.0.0.1:8080/health`（gamelogic 8081 / gmt 8082），再 `curl http://127.0.0.1:8080/version` 核对版本号；启用前端托管时 `curl http://127.0.0.1:8082/` 应返回 HTML |
| 11. 配置反向代理（可选） | 服务器 | 域名直解析到本机时，见 5.8：`sudo ./scripts/install.sh --with-nginx --proxy <name>:<域名>:<端口> [...]`（可重复传多次，为每个服务各生成一个 server 块）装 nginx 并生成反代配置，再用 certbot 签发 HTTPS；用云 SLB/ALB 则跳过 |

#### 版本更新（后续发版）

建议一次只发一个服务，观察稳定后再发下一个：

```bash
# 本机：编译并传输
./scripts/build.sh -v 20260821_002
scp dist/gamelogic root@<服务器IP>:/tmp/

# 服务器：发布（自动完成 投放二进制 → 切软链接 → restart → 健康/版本校验 → 失败自动回滚）
cd /data/servers
./scripts/deploy.sh gamelogic 20260821_002 --binary /tmp/gamelogic

# 服务器：验证
curl -f http://127.0.0.1:8081/health
curl http://127.0.0.1:8081/version     # version 应等于 20260821_002
tail -n 20 deploy.log                  # 发布记录（result=success）
```

同一版本多服务更新时逐个执行：`./scripts/deploy.sh platform 20260821_002 --binary /tmp/platform`、`./scripts/deploy.sh gmt 20260821_002 --binary /tmp/gmt`。

#### 回滚

```bash
# 自动：发布失败时 deploy.sh 自动切回旧版本（deploy.log 记录 result=rollback），无需人工干预
# 手动：回滚到上一可用版本，或指定版本
cd /data/servers
./scripts/rollback.sh gamelogic                # 自动选择 releases 中除当前外最新版本
./scripts/rollback.sh gamelogic 20260821_001   # 回滚到指定版本
# 回滚同样带健康校验；若回滚目标异常，会自动恢复回切换前的版本
```

> 注意：首次发布必须走第 8 步 `docker compose up -d`；若未创建容器就执行 `deploy.sh`，其 `restart` 会因容器不存在而失败。首次发布失败时无旧版本可回滚，脚本会报错提示人工排查。若更新时修改了 `compose.yaml` 本身（端口、挂载等），需用 `docker compose up -d` 重建容器使配置生效（见十一.8）。

### 5.2 云主机环境预装（scripts/install.sh）

云主机首次部署前需预装 Docker 与 docker compose 插件（V2）、创建部署目录与日志目录。仓库提供了一键脚本 scripts/install.sh（Docker 相关安装逻辑独立在 scripts/install_docker.sh：install.sh 会调用它并透传 Docker 参数，也可单独执行只预装 Docker 环境）：

```bash
# 首次部署需上传到目标服务器的文件（A 组必须同目录上传，install.sh 运行时依赖兄弟文件）：
#   A. 仓库 scripts/ 五件套：install.sh、install_docker.sh、compose.yaml、deploy.sh、rollback.sh
#      上传到服务器任意目录，如：scp -r scripts root@<服务器IP>:~/
#   B. 生产配置：config/<svc>_prod.yaml ×3 + server.env
#      随后写入 /data/servers/config/，四份均 chmod 600（见 5.3 / 5.5）
#   C. 服务二进制：dist/ 构建产物 platform / gamelogic / gmt
#      scp dist/platform dist/gamelogic dist/gmt root@<服务器IP>:/tmp/（deploy.sh --binary 投放）
#   D. GMT 前端产物（可选）：解压到宿主机 /data/web，须含 index.html（见 5.6）

# 在 scripts/ 所在目录执行（install.sh 仅复制同目录文件，不依赖任何远程 git 仓库）：
sudo ./scripts/install.sh
```

脚本功能：

1. 校验 root 权限，识别发行版（Ubuntu/Debian 走 apt，RHEL/CentOS/阿里云 Linux 走 dnf/yum）。
2. 安装/校验 gzip/gunzip（gunzip 供 upload.sh 在服务端解压上传的二进制，缺失时 upload.sh 会自动回退为不压缩上传）。
3. 调用 `scripts/install_docker.sh` 完成 Docker 相关环境：安装/校验 curl、Docker Engine 与 `docker-compose-plugin`（已安装则跳过，未安装自动安装；Docker 已存在但缺 compose 插件时自动补装），并预拉取运行时镜像 `alpine:3.22` 缓存到本地（`--skip-pull` 可跳过；预拉失败仅警告，不中断安装）。Docker 相关参数（`--mirror`/`--registry-mirror`/`--skip-docker`/`--skip-pull`）由 install.sh 原样透传；只预装/校验 Docker 环境时可直接执行 `sudo ./scripts/install_docker.sh --mirror aliyun`。
4. 创建部署目录 `/data/servers`（current/releases/config/scripts）与日志目录 `/data/logs/{platform,gamelogic,gmt}`。
5. 可选：`--owner <用户>` 将 `/data/servers` 与 `/data/logs` 属主收归指定用户（非 root 运维模型，见 5.5；对已按 root 部署过的目录重跑同样生效）。
6. 同步到部署目录：从安装包所在位置（随 install.sh 一并上传的仓库 scripts/ 目录）复制 `compose.yaml` / `deploy.sh` / `rollback.sh`，仅本地文件复制、不拉取任何远程 git 代码（已存在的文件不会覆盖；install.sh / install_docker.sh 均为首装一次性脚本，不会同步到服务器）。**若上传时漏掉其中某个文件，同步失败仅打印 `[WARN]` 告警，Docker/镜像/目录/nginx 等环境初始化照常完成、脚本不中断**；收尾会提示补齐文件后再执行 `docker compose up -d` 与发布/回滚，重跑 install.sh 可自动补同步。
7. 可选安装 nginx 并自动生成反向代理配置（`--with-nginx` 开启；nginx 已安装或配置已存在时跳过不重复执行）。

常用参数：

```bash
sudo ./scripts/install.sh                     # 默认官方源安装
sudo ./scripts/install.sh --mirror aliyun     # 阿里云 Docker 源（国内云主机推荐）
sudo ./scripts/install.sh --registry-mirror https://docker.m.daocloud.io   # 配置镜像加速 + 预拉取
sudo ./scripts/install.sh --skip-docker       # 服务器已有 Docker，只校验 + 建目录 + 同步
sudo ./scripts/install.sh --skip-pull         # 跳过运行时镜像预拉取
sudo ./scripts/install.sh --only-dirs         # 仅建目录结构，环境已就绪时使用
sudo ./scripts/install.sh --owner ubuntu      # 目录/日志属主收归 ubuntu（非 root 运维模型，见 5.5）
```

仅预装/校验 Docker 环境（跳过目录与文件同步，逻辑与 install.sh 的 Docker 部分完全一致，参数同名）：

```bash
sudo ./scripts/install_docker.sh --mirror aliyun     # 阿里云 Docker 源
sudo ./scripts/install_docker.sh --skip-docker       # 服务器已有 Docker，仅校验 docker + compose 插件
sudo ./scripts/install_docker.sh --skip-pull         # 跳过运行时镜像预拉取
```

nginx 相关参数（详见 5.8）：

```bash
# 用通用 --proxy 为每个服务各生成一个 server 块（可重复传，name:domain:port）
sudo ./scripts/install.sh --with-nginx \
  --proxy platform:api.example.com:8080 \
  --proxy gmt:gm.example.com:8082
# 也可单次用逗号分隔一次传多个
sudo ./scripts/install.sh --with-nginx \
  --proxy platform:api.example.com:8080,gmt:gm.example.com:8082
# 客户端需直连 gamelogic 时，也只是多加一条 --proxy
sudo ./scripts/install.sh --with-nginx --proxy gamelogic:game.example.com:8081
# 仅安装/配置 nginx，跳过 docker/目录/同步（环境已就绪，只补一层反代）
sudo ./scripts/install.sh --only-nginx --proxy platform:api.example.com:8080
# 强制覆盖已存在的反代配置（默认已存在则跳过、不覆盖）
sudo ./scripts/install.sh --with-nginx --proxy platform:api.example.com:8080 --force-nginx-conf
```

| 参数 | 说明 |
| --- | --- |
| `--with-nginx` | 安装 nginx（若未装）并生成反代配置；默认**不**处理 nginx |
| `--skip-nginx` | 显式跳过 nginx（默认行为，仅用于提高命令可读性） |
| `--only-nginx` | 仅执行 nginx 安装与配置，跳过 docker/目录/同步 |
| `--owner <用户>` | 将 `/data/servers` 与 `/data/logs` 属主收归指定用户（配合 docker 组实现非 root 运维，见 5.5） |
| `--proxy <name:domain:port[,...]>` | 新增反代站点，为每个条目生成一个 server 块。支持**重复传多次**，或**单次逗号分隔多个**（`a:..:port,b:..:port`）。`name` 为站点标识（用于日志文件名/注释，可任取，如 platform/gamelogic/gmt/pay）；`domain` 为对外域名，填 `_` 匹配任意 Host（IP 直连）；`port` 为上游本机端口，反代到 `127.0.0.1:<port>` |
| `--force-nginx-conf` | 覆盖已存在的反代配置文件（默认已存在则跳过，原文件会先备份） |

> `name` 不必是固定名字，可任意取（如 `pay`、`web`），仅用于日志文件名与注释；真正决定转发目标的是 `port`。使用 `--with-nginx`/`--only-nginx` 时若**未传任何 `--proxy`**，脚本会安装 nginx 但跳过反代配置生成，并提醒补充 `--proxy`。

#### 镜像拉取机制（网络保障说明）

- `alpine:3.22` 使用**固定 tag**（非 `latest`），`compose.yaml` 三个服务均显式声明 `pull_policy: if_not_present`，Docker 只在**本地没有该镜像时**才联网拉取，拉取成功后永久缓存，之后不再访问镜像仓库。
- 发布/回滚脚本使用的 `docker compose restart` **不触发镜像拉取**，只重启现有容器（启动时重新解析软链接到新版本）。
- 结论：**唯一需要网络拉取镜像的时机是首次部署**（install.sh 已把这一步提前到预装阶段，并可用 `--registry-mirror` 加速）。只要首次 `docker compose up -d` 成功，后续所有服务更新、回滚都与外网无关，网络抖动不会影响线上更新。

环境变量：`SERVER_DIR`（默认 `/data/servers`）、`LOGS_DIR`（默认 `/data/logs`），与 compose 及部署脚本保持一致。

### 5.3 开发机构建并上传

```bash
# 1. 编译（版本号建议用序号格式，如 20260821_001）
./scripts/build.sh -v 20260821_001

# 2. 上传二进制到服务器临时目录
scp dist/platform dist/gamelogic dist/gmt ubuntu@<服务器IP>:/tmp/
```

### 5.4 服务器初始化目录与软链接

```bash
SERVER_DIR=/data/servers

# 创建部署根目录与日志目录
mkdir -p ${SERVER_DIR} /data/logs

# 初始化三个服务的版本目录
for svc in platform gamelogic gmt; do
  mkdir -p ${SERVER_DIR}/releases/${svc}/20260821_001
  mv /tmp/${svc} ${SERVER_DIR}/releases/${svc}/20260821_001/${svc}
  chmod +x ${SERVER_DIR}/releases/${svc}/20260821_001/${svc}
done

# 创建软链接 current/<svc> 指向初始版本
mkdir -p ${SERVER_DIR}/current
ln -sfn ../releases/platform/20260821_001  ${SERVER_DIR}/current/platform
ln -sfn ../releases/gamelogic/20260821_001 ${SERVER_DIR}/current/gamelogic
ln -sfn ../releases/gmt/20260821_001       ${SERVER_DIR}/current/gmt
```

> `ln -sfn ../releases/...` 使用相对路径，保证 release 目录整体迁移/备份后软链接依然有效。

> 执行身份与运维模型一致：方案 A（全 root）以 root 执行本步；方案 B（非 root）以 ubuntu 执行本步、无需 sudo（目录经 `install.sh --owner` 收权后 ubuntu 可写）。

### 5.5 准备配置目录

将生产配置放入 `/data/servers/config/`：

- 每个服务一个 YAML：`platform_prod.yaml`、`gamelogic_prod.yaml`、`gmt_prod.yaml`
- 一份环境变量文件 `server.env`（密钥等敏感配置）

**先选定运维模型（二选一，全链路保持一致），再收紧权限。**

**方案 A · 全 root 运维**：install.sh、deploy.sh、rollback.sh、`docker compose` 全部用 root 执行；配置文件保持 root 属主、权限 600 即可。适合单人、直接用 root 登录/SSH 的场景，也是方案默认行为。

**方案 B · 非 root 运维用户（推荐，更安全）**：日常用 `ubuntu` 这类普通用户执行发布。首装时通过 `install.sh --owner` 一次性把部署目录属主收归该用户，并把它加入 docker 组，之后发布全程免 sudo：

```bash
# 1. 预装环境时指定属主（等价于安装后 chown；对已按 root 部署过的目录重跑同样生效）
sudo ./scripts/install.sh --owner ubuntu

# 2. 将 ubuntu 加入 docker 组（注销重登后生效），使其能调用 docker daemon
sudo usermod -aG docker ubuntu

# 3. 前端产物目录属主一并收归（如需由 ubuntu 更新 /data/web）
sudo chown -R ubuntu:ubuntu /data/web 2>/dev/null || true
```

**两种模型下敏感文件一律收紧权限。** `server.env` 含 JWT 签名密钥、内部验签密钥；三个 `*_prod.yaml` 也可能含带密码的 `MONGO_URI`、GMT 超管初始化项，须一并 `chmod 600`（600 = 仅属主可读写，属主即实际运维用户）：

```bash
chmod 600 /data/servers/config/server.env \
        /data/servers/config/platform_prod.yaml \
        /data/servers/config/gamelogic_prod.yaml \
        /data/servers/config/gmt_prod.yaml
```

> **为什么非 root 运维要同时满足「属主 + docker 组」**：
> ① `docker compose` 是在**客户端**解析 `env_file`（`config/server.env`）再交给 daemon，运行者必须能读该文件，故 server.env 属主须为运行者；权限 600 只挡其它用户，不挡属主自己。
> ② `deploy.sh` / `rollback.sh` 要在 `/data/servers` 下写 `deploy.log`、在 `current/` 与 `releases/` 下建软链/建目录，故目录属主也须为运行者（`--owner` 已把 `/data/servers` 与 `/data/logs` 整树 chown 给该用户）。
> ③ docker 命令本身要求运行者在 `docker` 组。三者都由上方三步一并解决。

`server.env` 参考内容（密钥项必须显式设置且足够强）：

```bash
# 必填：JWT 签名密钥（三服务必须一致，>= 32 字节）
JWT_SIGNING_KEY=<随机生成强密钥>
# 必填：内部服务间验签密钥（三服务必须一致，>= 32 字节）
INTERNAL_SECRET=<随机生成强密钥>
# 平台服务：三方支付回调签名密钥（未配置则拒绝对应接口）
THIRD_PARTY_KEY=<可选>
# GMT 管理后台跨域白名单（生产必须显式配置可信域名）
GMT_CORS_ALLOWED_ORIGINS=https://admin.example.com
# GMT 首次启动自动初始化超管（可选，成对提供）
GMT_BOOTSTRAP_ADMIN_USERNAME=
GMT_BOOTSTRAP_ADMIN_PASSWORD=
```

> 说明：`server.env` 中的变量三服务共用；服务专属的地址（`PLATFORM_URL`、`GAMELOGIC_URL`）已写在 compose 的 `environment` 中按服务区分。也可以全量放 `server.env`（所有服务共享），保持 compose 更简洁。

### 5.6 准备 GMT 前端产物（如需同源托管后台页面）

GMT 支持同端口同源托管 Vue 后台页面：由环境变量 `GMT_WEB_DIST`（compose 中设为 `/data/web`）指向容器内产物目录，宿主机 `/data/web` 只读挂载到该路径。若只把 GMT 当纯 API 服务用，可跳过本节（此时访问 `/` 会因 JWT 返回 `10401`，属正常现象）。

```bash
# 1. 本机构建 Vue 前端产物（示例，具体命令以前端工程为准），产物在 dist/
#    npm run build

# 2. 上传产物到服务器宿主机 /data/web（与 compose 挂载源、GMT_WEB_DIST 保持一致）
scp -r dist/* root@<服务器IP>:/data/web/

# 3. 服务器上确认产物到位，必须含 index.html
ls -la /data/web            # 应能看到 index.html 与 assets/ 等
```

> 方案 B（非 root 运维）且产物由 root 放置时，补执行 `sudo chown -R ubuntu:ubuntu /data/web` 后即可由 ubuntu 更新页面。

> 关键对应关系：宿主机 `/data/web`（挂载源）→ 容器 `/data/web`（挂载点）→ `GMT_WEB_DIST=/data/web`（进程读取路径），三者必须一致。缺 `index.html` 或未挂载产物时，`/` 会走 JWT 鉴权返回 `10401`。

### 5.7 启动服务

```bash
cd /data/servers
docker compose up -d

# 等待启动后健康检查
sleep 5
curl -f http://127.0.0.1:8080/health
curl -f http://127.0.0.1:8081/health
curl -f http://127.0.0.1:8082/health

# 核对运行版本
curl http://127.0.0.1:8080/version

# 如启用了 GMT 前端托管，确认容器内能看到产物，并验证根路径返回 HTML（而非 10401）
docker exec game-gmt ls -la /data/web
curl -s http://127.0.0.1:8082/ | head -n 5
```

### 5.8 配置 nginx 反向代理（域名直解析到本机时推荐）

当生产域名通过 A 记录**直接解析到本服务器公网 IP** 时，DNS 只负责把域名解析成 IP，不处理端口与 TLS。此时推荐在宿主机加一层 nginx 反代：统一在 `80/443` 收流量、做 TLS 卸载与按域名分流，把请求转发到只绑回环的业务端口。`install.sh` 已内置该能力，**幂等可重复执行**（nginx 已装或配置已存在都会跳过）。

> 若使用云厂商 SLB/ALB（域名解析到负载均衡而非本机），TLS 与分流交给云负载均衡处理，可跳过本节、无需在服务器装 nginx。

#### 通用反代模型：`--proxy name:domain:port`

反代不再写死 platform/gmt 两个开关，而是用可重复的 `--proxy name:domain:port` 三元组，每传一次就多生成一个 `server` 块。想代理哪个服务、用什么域名、转发到哪个端口，完全自助：

| `--proxy` 示例 | 生成的 server 块 | 说明 |
| --- | --- | --- |
| `platform:api.example.com:8080` | `api.example.com` → `127.0.0.1:8080` | 对外业务接口 |
| `gmt:gm.example.com:8082` | `gm.example.com` → `127.0.0.1:8082` | 管理后台，建议白名单限制来源 |
| `gamelogic:game.example.com:8081` | `game.example.com` → `127.0.0.1:8081` | 客户端需直连 gamelogic 时才加 |

- `name`：站点标识，仅用于日志文件名（`/var/log/nginx/<name>_access.log`）和配置注释，可任取（`pay`、`web` 均可）。
- `domain`：对外域名；填 `_` 表示匹配任意 Host（IP 直连，或先联调后补域名）。
- `port`：上游本机端口，反代到 `127.0.0.1:<port>`。

#### 一键安装并配置

```bash
# 场景一：随环境预装一起做（首次部署时），按需列出每个服务
sudo ./scripts/install.sh --with-nginx \
  --proxy platform:api.example.com:8080 \
  --proxy gmt:gm.example.com:8082

# 场景二：环境已就绪，只补一层 nginx（不动 docker/目录）
sudo ./scripts/install.sh --only-nginx \
  --proxy platform:api.example.com:8080

# 多个站点也可单次用逗号分隔传入
sudo ./scripts/install.sh --with-nginx \
  --proxy platform:api.example.com:8080,gmt:gm.example.com:8082
```

脚本行为：

1. 检测 nginx，未装则按发行版自动安装并 `enable`+`start`；已装则跳过（幂等）。
2. **先校验全部 `--proxy` 三元组**（name 仅限字母/数字/下划线/连字符，port 为 1-65535 整数），任一非法直接中止、不落地半成品配置。
3. 遍历每个 `--proxy` 生成一个 `server` 块，写入配置：apt 系写 `/etc/nginx/sites-available/game-server.conf` 并软链到 `sites-enabled`（同时移除默认站点 `default`）；dnf 系写 `/etc/nginx/conf.d/game-server.conf`。
4. 配置已存在时默认**跳过不覆盖**（保护手改内容）；`--force-nginx-conf` 才覆盖，且覆盖前自动备份为 `*.bak.<时间戳>`。
5. `nginx -t` 校验通过后 `reload`（校验失败中止并提示，不会让 nginx 带错配置重启），最后打印所有已配置的反代映射清单。

生成的 server 块内置 gzip 压缩，前端产物的压缩刻意放在这一层：Go 服务的静态托管不做压缩（fasthttp 需要在产物目录旁写 `.fasthttp.gz` 压缩副本，而 `/data/web` 是只读挂载，写不进去，只会退化为不压缩并对每个文件打一行告警）。这里有两个 nginx 默认值容易踩坑，模板已显式覆盖：

| 指令 | 模板取值 | 不设的后果 |
| --- | --- | --- |
| `gzip_proxied` | `any` | 默认 `off`，反代来的响应**完全不压缩** |
| `gzip_types` | 显式列出 JS/CSS/JSON/XML/wasm/svg/ttf | 默认只含 `text/html`，JS/CSS 不压缩 |
| `gzip_min_length` | `1024` | 默认 20 字节，小文件压缩反而更大 |
| `gzip_vary` | `on` | 缺 `Vary: Accept-Encoding`，中间缓存可能把压缩版发给不支持的客户端 |

已自带压缩的格式（`woff2`/`png`/`jpg`/`webp`/`mp4`）不重复压缩。验证：`curl -H 'Accept-Encoding: gzip' -I https://gm.example.com/assets/<hash>.js` 应返回 `Content-Encoding: gzip`。已存在的反代配置默认不覆盖，需要套用新模板时加 `--force-nginx-conf`（覆盖前自动备份）。

> 未传任何 `--proxy` 时脚本会安装 nginx 但**跳过反代配置生成**并给出提醒（不落地默认站点）；补上 `--proxy` 后重跑 `--only-nginx` 即可生成配置。若要匹配任意 Host（IP 直连或先联调后补域名），把 `domain` 填 `_`，如 `--proxy platform:_:8080`。三个服务默认均仅绑 `127.0.0.1`，只有你显式为其加 `--proxy`（经本机 nginx 反代）才对外可达。

#### 启用 HTTPS（Let's Encrypt 自动续期）

生成的配置默认只监听 80，证书交给 certbot 自动签发并改写配置（生成的配置头部会按你传入的域名给出对应 certbot 命令示例）：

```bash
# Debian/Ubuntu
apt install -y certbot python3-certbot-nginx
# RHEL 系
dnf install -y certbot python3-certbot-nginx

# 自动签发证书 + 改写 nginx 配置补 443 + 配置 80->443 跳转（一次可带多个 -d）
certbot --nginx -d api.example.com -d gm.example.com
# certbot 默认注册 systemd timer 自动续期，无需手动维护
```

#### 验证与安全组

```bash
# 本机验证反代是否打通（带 Host 头模拟域名访问）
curl -H 'Host: api.example.com' http://127.0.0.1/health

# 安全组/防火墙：仅放行 80/443（及 SSH），业务端口 8080/8081/8082 不对公网开放
```

#### 日志自动清理（logrotate）

反代产生的访问/错误日志写在 `/var/log/nginx/<name>_access.log`、`<name>_error.log`。这些日志**无需手动清理**：发行版通过 `apt`/`dnf` 安装 nginx 时会自带 `/etc/logrotate.d/nginx`，用通配符匹配 `/var/log/nginx/*.log`，默认按天切割、保留有限份数并压缩，自动清理旧日志，防止磁盘写满。`install.sh --with-nginx` 安装完会检测该规则是否存在：存在则提示已托管，缺失则告警提醒补装 `logrotate`。

```bash
# 确认轮转规则存在
ls -l /etc/logrotate.d/nginx
# 手动演练一次轮转（-d 仅打印计划不真正执行，用于验证配置）
logrotate -d /etc/logrotate.d/nginx
```

> 与端口暴露设计的对应关系：compose 中三个服务（platform/gamelogic/gmt）均绑 `127.0.0.1`，只有本机进程（即 nginx）能访问，业务端口不直接对公网；安全组只放行 nginx 的 80/443 即可。若改用云 SLB/ALB 直连后端，则需把对应服务端口放开为 `0.0.0.0`（或内网 IP）并交由 SLB 监听器/安全组限制来源。

---

## 六、版本更新流程

### 6.1 开发机

```bash
./scripts/build.sh -v 20260821_002
scp dist/platform dist/gamelogic dist/gmt root@<服务器IP>:/tmp/
```

### 6.2 服务器：创建新 release 目录并投放二进制

```bash
for svc in platform gamelogic gmt; do
  mkdir -p /data/servers/releases/${svc}/20260821_002
  mv /tmp/${svc} /data/servers/releases/${svc}/20260821_002/${svc}
  chmod +x /data/servers/releases/${svc}/20260821_002/${svc}
done
```

也可以交给 `deploy.sh` 的 `--binary` 参数自动投放（见第七节）。

### 6.3 原子切换并重启（手工方式）

```bash
cd /data/servers
ln -sfn ../releases/gamelogic/20260821_002 current/gamelogic
docker compose restart gamelogic
```

> 强烈建议使用部署脚本 `./scripts/deploy.sh` 完成此步骤，自动包含健康检查与失败回滚。

---

## 七、部署脚本使用说明

### 7.1 `deploy.sh` —— 发布

```bash
# 基本用法：发布指定服务到指定版本
./scripts/deploy.sh gamelogic 20260821_002

# 带上二进制自动投放：从 /tmp/gamelogic 复制进版本目录
./scripts/deploy.sh gamelogic 20260821_002 --binary /tmp/gamelogic

# 跳过 /version 版本核对（一般无需手动加，旧二进制无该接口时自动跳过）
./scripts/deploy.sh gamelogic 20260821_002 --no-version-check
```

行为：
1. 校验服务名、版本号格式、compose 文件存在。
2. 校验版本目录与二进制可执行。
3. 记录旧版本（读取当前软链接实际指向）。
4. `ln -sfn` 原子切换软链接。
5. `docker compose restart <svc>`。
6. 轮询 `GET /health`（默认 30 次 × 2 秒）；通过后再核对 `GET /version` 等于目标版本。
7. 任一校验失败：自动切回旧版本并重启，记录发布日志。

环境变量（可选）：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `GAME_SERVER_DIR` | `/data/servers` | 部署根目录 |
| `HEALTH_URL_BASE` | `http://127.0.0.1:<服务默认端口>` | 健康检查/版本校验地址前缀 |
| `HEALTH_TRIES` | `30` | 健康检查尝试次数 |
| `HEALTH_DELAY` | `2` | 健康检查间隔（秒） |

发布历史记录在 `${GAME_SERVER_DIR}/deploy.log`。

### 7.2 `rollback.sh` —— 回滚

```bash
# 查看各服务当前版本与可用版本
./scripts/rollback.sh
./scripts/rollback.sh --list

# 回滚到上一可用版本（自动选 releases 中除当前外的最新一个）
./scripts/rollback.sh gamelogic

# 回滚到指定版本
./scripts/rollback.sh gamelogic 20260821_001
```

行为：切换软链接 → `docker compose restart` → 健康检查 + 版本核对 → 目标异常自动恢复回切换前版本。

### 7.3 手工回滚（发布失败逃生通道）

```bash
cd /data/servers
ln -sfn ../releases/gamelogic/20260821_001 current/gamelogic
docker compose restart gamelogic
curl -f http://127.0.0.1:8081/health
```

---

## 八、版本回滚（发布失败时）

只需要把软链接切回旧版本，重启容器即可，速度极快：

```bash
cd /data/servers
ln -sfn ../releases/<svc>/<旧版本> current/<svc>
docker compose restart <svc>
```

---

## 九、完整发布流程图

```
                   开发机 / CI
                     │
             go test + ./scripts/build.sh
                     │
      ./scripts/upload.sh <svc> <version>（gzip 压缩 + sha256 校验上传）
                     │
                     ▼
              Linux Server
                     │
       releases/<svc>/<version> 存放新版本二进制
                     │
                     ▼
   deploy.sh: current/<svc> → 新版本（软链接原子切换）
                     │
                     ▼
             docker compose restart <svc>
                     │
                     ▼
              GET /health + GET /version 校验
                     │
            ┌────────┴────────┐
            │                 │
          ✅成功              ❌失败
            │                 │
            ▼                 ▼
         保留新版本         自动切回旧版本，重启服务
                              │
                              ▼
                         deploy.log 记录结果
```

---

## 十、为什么不直接覆盖正在运行的二进制

直接 `cp` 覆盖正在运行的二进制存在风险：

- 程序正在读取文件时覆盖，可能出现文件损坏、程序异常崩溃。
- 没有版本留存，出问题无法快速回滚。

使用 `releases 目录 + current 软链接`：

1. 每个版本文件独立存放，互不干扰。
2. `ln -sfn` 是原子操作，瞬间完成切换。
3. 旧版本文件保留，故障可以一键回滚。

---

## 十一、补充注意事项与安全建议

1. **配置统一放 `config` 目录，不随版本发布**；变更配置单独修改后重启服务，与二进制版本解耦。
2. **旧版本定期清理**：保留最近 N 个版本，避免磁盘占用过高。
   ```bash
   # 示例：删除 gamelogic 中 30 天前的版本目录（先确认 current 未指向）
   find /data/servers/releases/gamelogic -maxdepth 1 -type d -mtime +30 -exec rm -rf {} +
   ```
3. **listen 地址**：Go 服务监听 `0.0.0.0`（Fiber 默认监听全接口），容器内 `127.0.0.1` 外部无法访问。
4. **端口暴露 / 反向代理**：三个服务（platform/gamelogic/gmt）默认均绑 `127.0.0.1`，外部（含云 SLB/ALB）不可直连（本项目已按此设计）。推荐用 `install.sh --with-nginx`（见 5.8）一键装 nginx 并生成反代配置，用可重复的 `--proxy name:domain:port` 为每个服务各配一个站点（如 `--proxy platform:api.example.com:8080 --proxy gmt:gm.example.com:8082`），安全组只放行 nginx 的 80/443。若游戏客户端需直连 gamelogic，加一条 `--proxy gamelogic:game.example.com:8081` 即可；gmt 若无需公网访问则不配 `--proxy`，始终不暴露公网。若改用云 SLB/ALB 直连后端，则需把对应服务端口由 `127.0.0.1:<port>:<port>` 改为 `0.0.0.0:<port>:<port>`（或内网 IP）后 `docker compose up -d <svc>` 重建，并交由 SLB 监听器/安全组限制来源。
5. **密钥与权限**：`config/` 下 `server.env` 与三个 `*_prod.yaml` 均设权限 600，属主为实际运维用户（非 root 运维用 `install.sh --owner` 收权并加入 docker 组，见 5.5）；禁止上传 .env、密钥文件到代码仓库；`JWT_SIGNING_KEY` / `INTERNAL_SECRET` 生产环境必须为高强度随机值。
6. **安全组**：走本机 nginx 反代时，安全组只放行 nginx 的 `80/443`（及 SSH），业务端口 8080/8081/8082 因仅绑 `127.0.0.1` 本就不对公网开放；若放开为 SLB 直连后端，安全组仅对 SLB 内网来源放行对应端口，gmt 端口切勿对公网开放。
7. **健康检查**：对外（或云监控）可定时请求 `/health`，异常时告警；发布脚本已内置健康检查。
8. **容器重建时机**：`docker compose restart` 会重新解析 bind mount 软链接；若改了 compose.yaml 本身，需用 `docker compose up -d` 重建容器。
9. **首次发布无旧版本**：`deploy.sh` 检测到 current 软链接不存在时不会执行回滚，只会清晰报错引导排查。
10. **二进制完整性**：上传后可用 `dist/<version>_manifest.txt` 中的 sha256 在服务器端核对，防止传输损坏。
11. **GMT 前端产物更新**：前端与二进制解耦，走 `deploy.sh` 只更新 GMT 二进制、不影响前端页面。
    - 仅更新页面内容（`/data/web` 已挂载）：直接覆盖宿主机 `/data/web` 下的文件即可，静态资源实时生效，通常无需重启容器；如遇缓存可 `docker compose restart gmt`。
    - 首次新增该挂载或修改了 `compose.yaml`（如加 volume、改 `GMT_WEB_DIST`）：必须 `docker compose up -d gmt` 重建容器，`restart` 不会重新读取新的挂载与环境变量。
    - `curl http://127.0.0.1:8082/` 返回 `10401` 而非 HTML：排查 `GMT_WEB_DIST` 是否配置、宿主机 `/data/web` 是否已挂载且含 `index.html`（`docker exec game-gmt ls -la /data/web`）。
12. **日志轮转与自动清理**：日志由服务进程内的 `pkg/logx`（zap + lumberjack）负责，**无需宿主机 cron 或 logrotate**。当前文件恒为 `/data/logs/<Service>.log`（如 `GameLogic.log`），写满 `log.max_size`（默认 200MB）后轮转为 `<Service>-<ISO时间戳>.log`（时间戳统一为 **UTC**）；超过 `log.max_age`（默认 7 天）的历史文件自动删除，轮转出的文件默认压缩为 `.gz`。可在各服务 `*_prod.yaml` 覆盖，或用环境变量 `LOG_MAX_SIZE` / `LOG_MAX_AGE` / `LOG_MAX_BACKUPS` / `LOG_COMPRESS` 覆盖。日志 JSON 的 `time` 字段同样固定为 UTC，容器环境另统一设置 `TZ=UTC`（见 `scripts/compose.yaml`），跨地区部署时日志时间口径一致。
    - **清理触发时机**：lumberjack 原生仅在「服务启动后首次写入」与「每次写满 `max_size` 轮转」两个时点执行删除与压缩，普通写入不触发。为避免日志量小的服务（一天写不满 200MB）长期不清理，`pkg/logx` 另设**每日兜底轮转**：距上次兜底轮转超过 24h 后的首次写入会主动轮转一次，从而保证超过 `max_age` 的日志必定被删除（代价是低流量服务每天多出一份体积很小的备份）。无需下调 `log.max_size`，也无需宿主机 cron。
    - **压缩是「轮转即压缩」**：轮转出的文件立即压成 `.gz`，不是「延迟 N 天再压缩」，当天较早轮转出的文件也会被压缩。
    - **切勿删除当前日志文件**：运行期删除 `/data/logs/<Service>.log` 不会立即重建——lumberjack 会继续写入已被删除的 inode，直到下一次轮转才按原文件名新建，此期间的日志不可见。归档请使用进程退出后的文件，或依赖 `max_age` 自动清理。
    - **存量按天日志需一次性手工清理**：历史上曾按天命名（`<Service>-YYYY-MM-DD.log`），这类文件不符合 lumberjack 的备份命名（前缀 + 时间戳），`max_age` 永远清理不掉。升级到本方案后执行一次即可：
      ```bash
      # 删除 /data/logs 下 7 天前的旧制按天日志（确认无进程占用后执行）
      find /data/logs -maxdepth 1 -type f -name '*-20??-??-??.log' -mtime +7 -delete
      ```
    - **压缩/删除在容器内以 root 身份、在挂载卷上执行**，宿主机非 root 运维用户（如 ubuntu）只需读权限即可查看；当前日志文件权限已放宽为 0644（`permFixWriter` 负责）。

---

## 十二、CI/CD 扩展（GitHub Actions 等）

`deploy.sh` 的服务端逻辑可完全复用，只需把「开发机构建 + scp 上传」环节放到 CI：

```yaml
# .github/workflows/deploy.yml（示意）
steps:
  - run: ./scripts/build.sh -v ${{ env.VERSION }}
  - run: scp dist/* root@server:/tmp/
  - run: ssh root@server "
      cd /data/servers &&
      ./scripts/deploy.sh platform  $VERSION --binary /tmp/platform &&
      ./scripts/deploy.sh gamelogic $VERSION --binary /tmp/gamelogic &&
      ./scripts/deploy.sh gmt       $VERSION --binary /tmp/gmt
    "
```

服务器脚本无需改动，发布流程完全复用。