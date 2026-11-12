# TG打卡考勤

可直接编辑、构建的源码：Go 后端 + Vue 后台 + Telegram 机器人。包含多标签后台、子账号本人权限、`/bind 后台子账号`、新成员入群 ID 提示，以及打卡文案、班次、活动、罚款、审批和报表。

## 部署

| 部署位置 | 使用文件 | 指南 |
|---|---|---|
| Render 免费 Web Service + Aiven | Dockerfile、render.yaml | [Render 部署](docs/DEPLOY.md) |
| 自有服务器 + 本地 PostgreSQL | Dockerfile、compose.yaml、Caddyfile | [服务器部署与备份](docs/SERVER.md) |
| 自有服务器 + Aiven | Dockerfile、compose.aiven.yaml、Caddyfile | [外部数据库部署](docs/SERVER-AIVEN.md) |

- **服务器**：填写 `.env` 后运行 `docker compose up -d --build`，数据库自动创建，数据在 `data/postgres/`；备份运行 `sh backup.sh`。Render 不使用 compose，HTTPS 由平台提供。
- **上传 GitHub**：仓库根目录应直接看到 Dockerfile、render.yaml、compose.yaml、backend、frontend、docs。网页上传别漏掉隐藏文件 `.gitignore`、`.dockerignore`、`.env*.example`；不要上传 node_modules、dist、真实 `.env` 或 CA 证书。必须使用本包的 Dockerfile。
- **CA 证书（`aiven-ca.pem`）不进 GitHub**（`.gitignore` 已排除 `*.pem`），部署时单独放置：Render 在 Environment → Secret Files 添加文件名 `aiven-ca.pem`、内容为 Aiven 下载的完整 PEM，会挂载为 `/etc/secrets/aiven-ca.pem`，并设置 `PGSSLROOTCERT` 指向它；自有服务器 + Aiven 把证书放在服务器项目根目录并命名 `aiven-ca.pem`（见 [外部数据库部署](docs/SERVER-AIVEN.md)）；使用 `compose.yaml` 自带本地 PostgreSQL 则不需要 CA。缺少或放错证书时服务无法连接数据库，不要用关闭校验的方式绕过。
- **已部署的 Render 升级**：用本目录覆盖仓库根目录文件；旧的 `*.tar.gz` 可删除；保留 Render 上已有的环境变量、Secret File 和 Aiven 数据库；**不清库，不更换 `TELEGRAM_TOKEN_SECRET`**（迁移兼容初版、v2、v3）。若之前未启用入群通知，把机器人设为群管理员，并在「打卡规则 → 基础配置 → 界面与权限」重新注册 Webhook。

## 环境变量

仓库只放示例值，真实密码、数据库连接和 Bot Token 填到平台环境变量或服务器 `.env`，不要提交。模板：本地 [`.env.example`](.env.example)，服务器 [`.env.server.example`](.env.server.example)，服务器 + Aiven [`.env.aiven.example`](.env.aiven.example)，Render 见 [render.yaml](render.yaml)。

**必填**

| 变量 | 说明 |
|---|---|
| `DATABASE_URL` | 外部数据库（Render / Aiven / 本地运行）的完整 PostgreSQL URI，含库名，生产用 `sslmode=verify-full`。本地数据库的 Compose 自动拼接，不用填 |
| `POSTGRES_PASSWORD` | 仅本地数据库 Compose。建议 64 位随机十六进制，避免特殊字符破坏 URI |
| `ADMIN_PASSWORD` | 初始管理员密码，至少 6 字节，每次启动都必须设置，但只用于创建初始管理员。已有账号请在后台改密码，改这个变量不会重置密码 |
| `TELEGRAM_TOKEN_SECRET` | 加密保存 Bot Token 的固定密钥，至少 32 字节。升级时保持原值，不要重新生成 |
| `APP_DOMAIN` | 仅两种服务器 Compose。纯域名，如 `attendance.example.com`，不带 `https://` 或路径 |

**可选**

| 变量 | 默认值 | 说明 |
|---|---|---|
| `ADMIN_USERNAME` | `admin` | 初始管理员账号 |
| `PUBLIC_BASE_URL` | 自动 | 公网 HTTPS 地址，不带 `/telegram/webhook`。Compose 由域名生成，Render 回退到 `RENDER_EXTERNAL_URL`，本地运行需自己填 |
| `PGSSLROOTCERT` | — | 外部数据库 CA 证书路径。Render 为 `/etc/secrets/aiven-ca.pem`，Aiven Compose 已固定，本地填实际路径 |
| `DB_MAX_CONNECTIONS` | `5`（范围 3–10） | 数据库连接池上限 |
| `TELEGRAM_BOT_TOKEN` | 不填 | 通常在后台保存 Token；设置此变量则每次启动用它更新后台 Token（秘密信息） |
| `DATABASE_QUOTA_BYTES` | `0`（未知） | 数据库容量额度，字节。为 `0` 时无法判断是否超过 80%。`1 GiB = 1073741824`，`5 GiB = 5368709120` |
| `RESOURCE_DISK_PATH` | `.`（Render 为 `/app`） | 监控此路径所在的文件系统，Docker 内须是容器可见路径；不代表外部数据库的磁盘 |
| `TELEGRAM_RESET_EXPORT_DIR` | 系统临时目录 | 重置导出的临时副本位置，不要指向数据库或备份目录 |

`ENV`、`PORT`、`TZ`、`FRONTEND_DIST`、`GOMEMLIMIT`、`GOGC` 已在 Dockerfile、Compose 和 render.yaml 中预设，无需填写（本地运行的取值见 `.env.example`）。修改任何环境变量后需重启或重新部署。

**容量监控与清理**

- **后台已保存的清理策略优先于 `DATABASE_QUOTA_BYTES`**：改环境变量覆盖不了已保存的额度，请到「设置 → 清理内存」修改。
- 默认自动清理，每分钟检查，严格超过 80% 才触发，两次执行至少间隔 30 分钟；已处理消息/通知保留 30 天，维护日志 180 天，已备份导出的临时副本 72 小时。开关和保留期都在后台设置，无需额外环境变量。
- 只清理允许清理的数据，不删除核心考勤、账号、有效备份或数据库底层文件。容量告警显示在清理页面和服务日志中，详见 [升级说明](docs/UPGRADE.md)。

## 排查打卡延迟

日志里每次打卡会输出各环节耗时（毫秒）：

- `[worker]`：每个机器人请求的 `auth_ms`（鉴权）和 `handler_ms`（处理）。
- `[punch]`：活动开始 / 结束的分段耗时，如 `identity`、`lock`、`state`、`bounds`、`count`、`insert`、`close_*`、`tail`；最大的一项就是瓶颈。
- `[tgbot] stage=`、`[inbox]`：机器人侧各环节、队列与处理总耗时。

设置行在进程内缓存 3 秒：后台保存立即生效，直接改数据库最长 3 秒后生效。

空闲时每 45 秒预热一次到 Telegram 的连接，避免两次打卡间隔较久后首条回复重新握手（约多 500ms）。同一时机还会让数据库连接池保持最多 3 条连接活跃（`warmDatabasePool`，每条只做一次 Ping，繁忙时 2 秒内放弃），避免空闲超过 5 分钟或被数据库断开后，下一次打卡并行读取时重新建连。

**延迟的主要来源是网络距离。** 日志里 `[punch]` 各分段每步约等于一次应用到数据库的往返；Render 与 Aiven 不在同一区域（例如新加坡与孟买）时每步约 80ms，打卡链路约 1 秒，其中数据库往返占一半左右。把数据库放到与 Render 相同的区域，是最有效的提速方式，不需要改代码。迁移时保持 `TELEGRAM_TOKEN_SECRET` 不变。

Render 免费版空闲 15 分钟会休眠，唤醒需要几十秒，进程睡着时内部保活不会运行。需要及时响应时使用外部定时访问 `/health`，或改用常驻服务器。

## 源码组织

- `backend/services/telegram.go`：TG 业务、文案、活动、轮班、罚款与报表。
- `backend/services/attendance.go`：员工与考勤核心逻辑；`attendance_operation.go` 是按员工的操作锁。
- `backend/services/deployment.go`：独立数据库、维护任务、事件时间和绑定。
- `backend/services/punch_stages.go`：打卡分段耗时日志。
- `backend/internal/standalone/module.go`：会话、子账号权限和持久消息队列。
- `backend/internal/tgbot/module.go`：TG 命令、菜单、消息和入群事件。
- `backend/handlers/attendance.go`：考勤 / TG 后台接口。
- `backend/services/compatibility.go`：旧业务仍引用的辅助类型和实现。没有路由、也不会被独立入口调用的遗留功能（站点统计、薪资、员工账号管理、LiveKit、Redis、MinIO、远程查看、邮件、告警监控等）已清理，路由与现有功能不变。
- `frontend/`：可单独编辑的 Vue 页面、组件和样式。

Go 源文件按包合并，保留 `Source:` 标记和测试。合并减少的是文件数，**不等于代码量、内存或网络延迟同比减少**。真实的数据库初始化和迁移在 Go 源码内，没有独立的 SQL 迁移文件。后续拆分建议见 [ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 本地构建与测试

需要 Go 1.26、Node 22+。在 `frontend` 运行 `npm ci`、`npm run build`；在 `backend` 按 `.env.example` 创建 `.env` 后运行 `go run .`。

```sh
cd backend
go test . ./services ./internal/tgbot ./internal/standalone
```

数据库集成测试只能连专用的临时测试库（会改动 TG 配置并插入虚拟员工），不能指向业务库：

```sh
STANDALONE_TEST_DATABASE_URL='postgresql://.../disposable_db?sslmode=disable' \
  go test . ./services ./internal/standalone -run 'TestStandalone|TestInbox|TestMemberNotice' -count=1
```

Render 免费版休眠会延迟响应和定时任务，常驻服务器可避免，但不保证网络故障时消息即时送达。镜像和服务器 HTTPS 需部署后验收。
