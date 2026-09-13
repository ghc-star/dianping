# Go 黑马点评：初学者架构导学

这份文档回答两个问题：

1. 这个项目的每一层负责什么？
2. 以后你自己写 Go 项目时，应该如何拆分代码？

你刚学完 Go、Gin、GORM，不需要先把所有代码读完。先掌握一条请求如何流动，再逐步学习缓存、事务和并发。

## 1. 先建立整体认识

这是一个**单体 Go API**。它运行一个进程，提供 Gin HTTP 接口，同时启动订单消费者和 Feed 投递循环。

```text
客户端
  │
  ├── 直接访问 Go API :8081
  └── 访问 Nginx :8080/api
          │
          ▼
      Gin Router
          │
          ├── AccessLog：记录请求方法、路径、状态、耗时
          ├── Recovery：捕获 Handler panic
          └── Auth：从 Redis 读取登录会话
                  │
                  ▼
              Handler
        参数绑定、校验、响应转换
                  │
                  ▼
              Service
       业务规则与多个依赖的编排
             ┌────┴────┐
             ▼         ▼
         Repository   Redis/cache
             │         │
             ▼         ▼
           MySQL   缓存、Lua、Stream

订单请求 ──> Redis Lua ──> Redis Stream ──> OrderWorker ──> MySQL 事务
Blog 发布 ─> MySQL Blog + Outbox ────────> Feed 投递循环 ─> Redis ZSet
```

### 这不是“每个目录一个框架层”

项目没有为了模仿 Java Spring 增加 `IService`、`ServiceImpl` 等空接口。Go 更常见的方式是：

- 用具体的 `struct` 保存依赖和方法。
- 用构造函数完成组装。
- 在真正需要替换依赖的调用方定义窄接口。
- 让 `error` 沿调用链返回，由上层决定如何展示。

这样做的目标是让依赖关系直接可见，而不是让初学者在很多接口之间跳转。

## 2. 目录职责

| 目录 | 当前职责 | 这里不应该放什么 |
|---|---|---|
| `cmd/server` | API 进程入口、依赖组装、启动和退出 | 具体业务规则 |
| `cmd/migrate` | 独立执行数据库迁移和开发数据初始化 | HTTP 路由 |
| `internal/handler` | Gin 路由、Path/Query/JSON 校验、响应和上传 | SQL、库存规则 |
| `internal/middleware` | 登录会话、访问日志、panic 恢复 | 注册用户、创建订单 |
| `internal/service` | 业务规则、事务/缓存/Redis 的编排 | 读取 Gin Header |
| `internal/repository` | GORM 查询、明确的数据库操作、事务 | HTTP 状态码和 JSON |
| `internal/model` | 数据库表映射和内部数据模型 | 登录 Token 全局状态 |
| `internal/dto` | HTTP 或对外使用的精简对象 | 数据库查询流程 |
| `internal/cache` | 通用 Cache Aside、空值、TTL、singleflight | 商铺业务字段判断 |
| `internal/redisx` | Redis Key、Lua 脚本和 Redis 协议细节 | Gin Context |
| `internal/worker` | Redis Stream 消费、Pending 接管、ACK 和退出 | 无限重试消息 |
| `pkg/apperror` | 业务错误类型 | 具体业务流程 |
| `pkg/response` | 统一成功/失败响应 | 查询数据库 |
| `migrations` | 显式 DDL、种子数据和旧库升级脚本 | 请求时自动改表 |

`internal` 的含义是：这些包只供当前 Go module 内部使用，不作为外部库 API。

## 3. 从启动代码理解依赖注入

入口在 `cmd/server/main.go:33` 的 `run`。阅读时按下面顺序走：

1. `config.Load` 读取 `configs/config.yaml`，再用 `DIANPING_` 环境变量覆盖。
2. `zap.NewProduction` 创建 JSON 日志。
3. `signal.NotifyContext` 创建进程生命周期 Context。收到 Ctrl+C 或终止信号后，它会取消子任务。
4. `bootstrap.Database` 打开 GORM/MySQL 连接池。
5. 检查 `schema_migrations`，确保已经运行 `cmd/migrate`，服务不会偷偷 `AutoMigrate`。
6. 创建 `go-redis/v9` 客户端并执行 `PING`。
7. 创建一个具体 `repository.Repository`。
8. 把 Repository、Redis、日志和配置注入 `UserService`、`ShopService`、`SocialService`、`VoucherService`。
9. 启动商铺 GEO 和秒杀状态预热。
10. 创建 `OrderWorker` 和 Gin Router。
11. 创建 `http.Server`，启动订单 Worker、Feed 循环和 HTTP 服务。
12. 进程取消时，优雅关闭 HTTP 服务，并等待后台任务结束。

可以把依赖关系画成：

```text
Config ─┬─> Database ─> Repository ─┬─> UserService
        │                           ├─> ShopService
        ├─> Redis ──────────────────┼─> SocialService
        └─> Logger ─────────────────└─> VoucherService
                                      ├─> Handler.New
                                      └─> OrderWorker
```

### 为什么在入口组装依赖

`service.NewUser` 需要 Repository 和 Redis，但它不应该自己读取配置文件、创建数据库连接。入口统一组装有三个好处：

- 依赖关系清楚。
- 测试可以传入 `sqlmock`、`miniredis` 或替身。
- 服务层不依赖 Gin 或命令行环境。

`cmd/migrate` 是另一条程序入口。先执行迁移，再启动 API：

```powershell
go run ./cmd/migrate -seed
go run ./cmd/server
```

## 4. 一次请求如何经过各层

### 4.1 Handler：只处理 HTTP 形状

`internal/handler/router.go:30` 创建路由。所有请求先经过：

```go
r.Use(middleware.AccessLog(api.Log), middleware.Recovery(api.Log))
r.Use(middleware.Auth(api.Users))
```

Handler 负责四件事：

1. 从 Path、Query 或 JSON 读取输入。
2. 使用 Gin binding 和额外函数校验输入。
3. 调用 Service。
4. 用 `respond` 或 `response.Fail` 返回结果。

例如登录 Handler 在 `internal/handler/router.go:55`：

```go
var in dto.Login
if !bind(c, &in) {
    return
}
data, err := api.Users.Login(c.Request.Context(), in)
respond(c, data, err)
```

它没有直接操作 Redis 或 MySQL。这样 Handler 只关心“HTTP 输入是什么”，Service 才关心“登录业务是否成立”。

### 4.2 Middleware：把横切逻辑放在请求外层

`internal/middleware/middleware.go:33` 的 `Auth`：

1. 读取 `authorization` Header。
2. 支持裸 Token 和 `Bearer TOKEN`。
3. 调用 `UserService.Session` 查询 Redis Hash。
4. 把用户和 Token 放入 `gin.Context`。

`RequireLogin` 再检查 Context 中是否有用户。未登录返回 401。

因此 Service 不写 `c.GetHeader`。它接收明确的 `userID` 或业务参数，便于测试和复用。

### 4.3 Service：表达业务流程

例如登录在 `internal/service/user.go:89`：

- 验证手机号。
- 有验证码时，用 Redis Lua 原子校验并消费验证码。
- 没有验证码但有密码时，读取用户并用 bcrypt 比较密码。
- 通过唯一手机号创建或读取用户。
- 写入 Redis 会话 Hash，设置 30 分钟 TTL。
- 返回随机 Token。

一个 Service 可以协调多个依赖，但不应该知道 HTTP 状态码。它返回 `error`，上层统一转换。

### 4.4 Repository：把数据库操作集中起来

Repository 使用 GORM 查询 MySQL。典型原则是：

- 每个查询都使用 `db.WithContext(ctx)`。
- 查询结果和数据库错误原样区分。
- 事务只在 Repository 或明确的事务编排处存在。
- 条件更新检查影响行数，不能只看 SQL 是否执行成功。

数据库是用户、商铺、Blog、点赞、关注、优惠券和订单的权威来源。Redis 的数据是否可以丢失，要根据业务分别判断。

## 5. 四条真实业务链路

### 5.1 验证码登录

```text
POST /user/code
  → handler 读取 phone
  → UserService.SendCode
  → Redis Lua：检查发送间隔、保存验证码和 TTL
  → 开发日志输出验证码

POST /user/login
  → handler bind dto.Login
  → UserService.Login
  → Redis Lua：比较验证码并删除
  → Repository.FindOrCreateUser
  → Redis TxPipelined：写 login:token:TOKEN Hash
  → Handler 返回 Token
```

验证码只允许成功使用一次，Redis 中的验证码不能当作永久数据库记录。开发环境只模拟短信，不是真正的短信服务。

### 5.2 商铺查询与缓存

```text
GET /shop/{id}
  → handler 校验正整数 id
  → ShopService.GetByID
  → cache：先查 Redis
      ├─ 命中且未过期：直接返回
      ├─ 命中逻辑过期：返回旧值并有限后台刷新
      └─ 未命中：singleflight 合并回源
  → Repository 查询 MySQL
  → 写回 Redis（成功数据或短 TTL 空值）
  → 返回 Shop
```

更新商铺时先提交 MySQL，再失效缓存并维护 GEO。缓存不是最终真相，因此不能把“写入缓存成功”当作数据库提交成功。

### 5.3 Blog 发布与 Feed

```text
POST /blog
  → handler 读取 shopId/title/images/content
  → SocialService.CreateBlog
  → Repository 事务写 Blog 和 FeedOutbox
  → 后台 SocialService.Run 读取未投递 Outbox
  → 写入粉丝 feed 的 Redis ZSet
  → 成功后标记 delivered_at
```

如果 Blog 已写入 MySQL 但 Redis 暂时不可用，Outbox 记录仍在。Redis 恢复后可以继续投递。这是“事务 Outbox”模式：先保存可靠的投递意图，再异步更新外部投影。

### 5.4 秒杀下单

```text
POST /voucher-order/seckill/{id}
  → handler 校验券 ID
  → VoucherService.Seckill
  → Redis Lua 原子检查时间、库存、一人一单
  → 预扣库存、记录用户、XADD 到 Stream
  → 立即返回订单号（仅表示受理）
  → OrderWorker 消费 Stream
  → Repository.OrderCreate 事务：条件扣 MySQL 库存并插入订单
  → DB 成功后标记 created，再 ACK
  → 客户端 GET /voucher-order/status/{id}
```

Redis 负责高并发入口，MySQL 事务和唯一索引是最终约束。客户端不能把“收到订单号”理解成“订单已经落库”。

## 6. Go 初学者必须掌握的几个边界

### 6.1 Context

Handler 传入 `c.Request.Context()`。Service 把它继续传给 Redis 和 Repository，Repository 调用 `WithContext(ctx)`。

Context 用来传递：

- 请求取消信号。
- 超时。
- 生命周期结束信号。

不要把某个请求的 Context 保存到 Service 或 Repository 的字段里。请求结束后，那个 Context 就不再适合下一个请求。

### 6.2 Model 和 DTO

`model` 是数据库或内部业务对象，`dto` 是接口输入/输出对象。

例如 `dto.User` 只包含 `id`、`nickName`、`icon`，不会把密码和手机号返回给前端。不要直接把带密码的数据库 Model 作为 JSON 响应。

`ShopInput` 使用指针字段区分：

- 没传 `score`：不更新。
- 传 `score: 0`：明确把评分更新为 0。

这也是 Go 中用指针表示“可选字段”的常见方式。

### 6.3 error 和统一响应

Service 返回普通 `error` 或 `apperror`。Handler 最后调用 `respond`，`pkg/response` 统一输出：

```json
{"success":true,"data":...}
```

或：

```json
{"success":false,"errorMsg":"..."}
```

`fmt.Errorf("保存登录态: %w", err)` 会保留原始错误，`errors.As` 仍能识别业务错误类型。业务失败沿用项目约定返回 HTTP 200 并检查 `success`；未登录是 401，依赖不可用是 503，未知故障是 500。

### 6.4 GORM 事务

涉及多个必须同时成功的数据库操作时使用事务。例如订单需要：

1. 扣减库存。
2. 插入订单。

任何一步失败都应该回滚。库存更新还必须带 `stock > 0` 条件，并检查影响行数。仅仅把两个 `UPDATE` 放在一起，不等于事务。

### 6.5 Redis 数据的三种角色

| Redis 用途 | 例子 | MySQL 不可用时能否丢失 |
|---|---|---|
| 会话状态 | `login:token:*` | 丢失后用户重新登录 |
| 缓存 | 商铺详情、类型列表 | 可以从 MySQL 回源 |
| 业务投影/并发入口 | Feed ZSet、秒杀库存 | 需要对应恢复边界，不能随意重建或强灌 |

先确认数据角色，再决定故障处理方式。不要把所有 Redis Key 都当作普通缓存。

### 6.6 Goroutine 必须有退出者

`cmd/server/main.go` 使用 `errgroup` 管理 HTTP、订单 Worker 和 Feed 循环。缓存刷新使用取消 Context 和等待机制。

启动一个 goroutine 时，先回答：

- 谁负责停止它？
- 它等待什么信号？
- 进程退出时是否等待它完成？
- 出错后是重试、保留 Pending，还是结束？

没有退出路径的 goroutine 会导致测试挂起、进程无法优雅关闭或资源泄漏。

## 7. 推荐阅读顺序

### 第一步：读数据形状

```text
internal/model/models.go
internal/dto/user.go
internal/dto/shop.go
internal/dto/social.go
go.mod
```

练习：解释 `struct`、JSON tag、指针、slice、方法接收者和 `error`。

### 第二步：读一条简单 HTTP 请求

```text
internal/handler/router.go
internal/middleware/middleware.go
internal/service/user.go
internal/repository/user.go
```

先读 `/user/me`，再读验证码登录。观察参数在哪里绑定，用户在哪里进入 Context。

### 第三步：读 GORM 与缓存

```text
internal/repository/shop.go
internal/service/shop.go
internal/cache/cache.go
```

先理解“先查缓存，未命中查数据库”，再看空值、TTL、singleflight 和逻辑过期。

### 第四步：读社交数据结构

```text
internal/service/social.go
internal/repository/social.go
internal/redisx/social_keys.go
```

把 Set、ZSet、Bitmap、GEO 分别和关注、点赞、Feed、签到、附近商铺对应起来。

### 第五步：读可靠性和秒杀

```text
internal/redisx/seckill.lua
internal/worker/orders.go
internal/repository/order.go
docs/SECKILL.md
```

最后再读 Lua、Stream、Pending、ACK 和订单事务。先看测试断言，再看实现，更容易理解约束。

## 8. 适合你的练习顺序

1. 为 `/user/me` 写一条 `httptest` 请求，验证无 Token 返回 401。
2. 用 `dto.ShopInput` 更新 `score: 0`，观察指针字段如何区分“未传”和“传零”。
3. 读商铺详情两次，观察第一次回源、第二次命中缓存。
4. 连续调用签到和签到统计，理解 Bitmap 的最低位。
5. 用两个用户完成关注、发布 Blog、Feed 查询。
6. 创建一张小库存秒杀券，观察订单从 `pending` 变成 `created`。
7. 阅读测试并解释：为什么 DB 提交成功但 ACK 失败时，重放不能再次扣库存。

更详细的实现练习见：

- [`docs/ARCHITECTURE.md`](ARCHITECTURE.md)：项目设计边界。
- [`docs/IMPLEMENTATION_GUIDE.md`](IMPLEMENTATION_GUIDE.md)：如果从零实现，按什么顺序做。
- [`docs/LEARNING_PATH.md`](LEARNING_PATH.md)：十个学习阶段和验证标准。
- [`docs/SECKILL.md`](SECKILL.md)：秒杀故障矩阵和恢复边界。
- [`docs/EXPERIENCE_FLOW.md`](EXPERIENCE_FLOW.md)：按接口实际操作完整项目。

## 9. 判断自己是否真正理解

你不需要背下所有 Redis 命令。先尝试回答下面四个问题：

1. 为什么 Handler 不应该直接写 GORM 查询？
2. 为什么 Cache Aside 回源时不能把数据库错误缓存成空值？
3. 为什么秒杀接口返回成功后还要查询订单状态？
4. 如果 Blog 已经写入 MySQL，但 Redis 写入失败，系统靠什么恢复 Feed？

能结合当前代码路径说清楚这四点，就已经建立了这个项目最重要的架构基础。
