# Go 黑马点评

这是当前仓库Java/Spring Boot黑马点评的Go重构。它保留原核心URL、camelCase字段和`{success,errorMsg,data,total}`响应，同时用Go惯用结构重新实现登录、商铺缓存、Blog社交、Feed、签到、GEO、优惠券和秒杀订单。上层`../src`保留Java源码，适合逐模块对照学习。

## 已实现功能

- 手机验证码、bcrypt密码登录、Redis会话、滑动续期、真实注销、Gin中间件
- 商铺CRUD、类型缓存、Cache Aside、空值、TTL抖动、singleflight、逻辑过期与版本失效
- Blog发布/查询、持久点赞明细、关注/共同关注、ZSet Feed与事务Outbox补投
- Bitmap签到、GEO附近商铺、图片上传与本人安全删除
- 普通券和秒杀券、Redis Lua资格原子判断、Stream消费组、Pending接管、事务落单、ACK与幂等补偿
- MySQL显式迁移、Docker Compose、Nginx、OpenAPI、健康检查、单元与真实集成测试

原Java的`BlogCommentsController`为空，支付、核销、退款也没有Controller/Service流程，因此只保留相应表和Model，没有伪造业务接口。短信目前是明确的开发模拟：验证码写Zap日志；生产关闭模拟后接口返回503，等待接入短信商。

## 技术栈

Go 1.25、Gin、GORM、MySQL 8、go-redis/v9、Viper、Zap、validator/v10、bcrypt、singleflight、Redis Lua/Stream/Set/ZSet/Bitmap/GEO、OpenAPI 3、Docker、Nginx。测试使用testing/httptest、miniredis、sqlmock和可选真实MySQL/Redis。

原项目是Redis随机Token会话，本实现继续保留，避免叠加JWT产生两套相互冲突的过期机制。设计取舍见`docs/JAVA_TO_GO.md`。

## 最快启动：Docker

要求Docker Desktop/Engine与Compose v2可用。端口：Nginx 8080、API 8081、MySQL 3307、Redis 6380。

```bash
cd go-dianping
docker compose up -d --build
docker compose ps
curl http://localhost:8081/health/ready
```

第一次启动会创建空数据库、执行建表并插入开发数据，然后启动API。重复启动不会重复迁移或覆盖秒杀实时库存。停止服务：

```bash
docker compose down
```

该命令保留数据卷。只有确定要清空这个Compose项目的本地数据时才使用`docker compose down -v`。

## 本机启动

环境要求：Go 1.25+、MySQL 8.0+、Redis 7.2+。复制或编辑`configs/config.yaml`；敏感配置用环境变量，不要提交本地密码。

```bash
cd go-dianping
go mod download
go run ./cmd/migrate -seed
go run ./cmd/server
```

默认配置连接Docker暴露的MySQL 3307和Redis 6380。使用本机默认端口可覆盖：

```powershell
$env:DIANPING_MYSQL_DSN='dianping:password@tcp(127.0.0.1:3306)/go_dianping?charset=utf8mb4&parseTime=true&loc=Asia%2FShanghai'
$env:DIANPING_REDIS_ADDRESS='127.0.0.1:6379'
go run ./cmd/migrate -seed
go run ./cmd/server
```

配置键`server.address`、`mysql.dsn`等转换成`DIANPING_SERVER_ADDRESS`、`DIANPING_MYSQL_DSN`。release模式必须设置`DIANPING_SERVER_MODE=release`并关闭`DIANPING_AUTH_DEV_CODE_LOG=false`。

## 第一次调用

开发验证码不会放在HTTP响应，查看API日志：

```bash
curl -X POST 'http://localhost:8081/user/code?phone=13900000009'
docker compose logs --tail 20 api
```

用日志中的6位码登录：

```bash
curl -X POST http://localhost:8081/user/login \
  -H 'Content-Type: application/json' \
  -d '{"phone":"13900000009","code":"123456"}'
```

保存data里的Token，以Header访问受保护接口：

```bash
curl http://localhost:8081/user/me -H 'authorization: TOKEN'
curl 'http://localhost:8081/shop/of/type?typeId=1&x=120.149192&y=30.316078'
curl -X POST 'http://localhost:8081/voucher-order/seckill/2?idAsString=true' -H 'authorization: TOKEN'
```

秒杀返回只代表受理。浏览器应传`idAsString=true`，再调用`GET /voucher-order/status/{id}`；响应头`X-Order-ID`也始终提供精确字符串。

## 测试与检查

不依赖外部服务的默认验收：

```bash
go mod tidy
go fmt ./...
go build ./...
go vet ./...
go test ./...
go test -race ./internal/cache ./internal/lock ./internal/redisx ./internal/service ./internal/worker
```

真实集成测试需要独立、已迁移的测试数据库，并使用Redis DB 12/14/15：

```powershell
$env:DIANPING_TEST_MYSQL_DSN='dianping:dianping_dev@tcp(127.0.0.1:3307)/go_dianping_test?charset=utf8mb4&parseTime=true&loc=Asia%2FShanghai'
$env:DIANPING_TEST_REDIS_ADDR='127.0.0.1:6380'
go test ./internal/service ./internal/handler ./internal/worker -run Integration -count=1 -v
```

完整搭建、断点、Redis/SQL观察和常见故障见`docs/RUNNING.md`。

## 项目结构

```text
go-dianping/
├── cmd/server          # API和后台Worker启动、优雅退出
├── cmd/migrate         # 显式数据库初始化
├── internal/
│   ├── bootstrap       # MySQL连接池
│   ├── cache           # 通用Cache Aside
│   ├── config          # Viper配置
│   ├── handler         # Gin路由、验证、上传
│   ├── middleware      # 登录、日志、panic恢复
│   ├── model,dto       # 数据库模型与API对象
│   ├── repository      # GORM查询和事务
│   ├── redisx,lock     # Redis键、Lua、锁
│   ├── service         # 用户、商铺、社交、优惠券
│   └── worker          # Stream订单消费者
├── pkg                 # 业务错误与统一Result
├── migrations          # 新库DDL、开发数据、旧库人工升级
├── api                 # OpenAPI与本地接口浏览页
├── docs                # 架构、API、专项和学习文档
├── configs             # 配置与Nginx
├── Dockerfile
└── docker-compose.yml
```

## 文档入口

- `docs/MIGRATION_ANALYSIS.md`：Java全仓扫描、真实调用链和原问题
- `docs/ARCHITECTURE.md`：请求、依赖、Context、并发与生命周期
- `docs/API.md` / `api/openapi.yaml`：37个操作、请求响应和数据影响
- `docs/JAVA_TO_GO.md`：Spring能力到Go设计的逐项解释
- `docs/LEARNING_PATH.md`：按阶段学习与练习验证
- `docs/IMPLEMENTATION_GUIDE.md`：不看答案时从零实现的步骤
- `docs/REDIS.md`：所有Key、类型、TTL和恢复边界
- `docs/SECKILL.md`：Lua、Stream、Pending、事务、补偿与故障矩阵
- `docs/SOCIAL.md`：点赞、关注、Feed与Outbox
- `docs/DATABASE.md`：13张表、字段、索引与Java旧库升级
- `docs/RUNNING.md`：运行、测试、调试和排障

接口浏览页：http://localhost:8081/docs；OpenAPI：http://localhost:8081/api/openapi.yaml。
