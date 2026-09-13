# 运行、测试与调试

## Docker完整启动

```powershell
Set-Location D:\dianping\go-dianping
docker compose up -d --build
docker compose ps
Invoke-RestMethod http://localhost:8081/health/ready
Invoke-RestMethod http://localhost:8080/api/health/ready
```

预期两个请求都返回`success=true`、`status=ready`。`/health/live`只说明进程存活；`/health/ready`同时PING MySQL和Redis。Compose状态应为MySQL/Redis/API healthy，migrate Exited(0)，Nginx running。

常用操作：

```powershell
docker compose logs -f api
docker compose logs --tail 100 mysql redis
docker compose restart api
docker compose down
```

`down`保留命名卷。清空开发数据会删除订单和Redis持久状态，必须明确针对当前`go-dianping` Compose项目后再执行`docker compose down -v`。

## 配置

默认配置在`configs/config.yaml`：API 8081、MySQL 3307、Redis 6380、业务时区Asia/Shanghai、缓存和Worker安全默认值。命令行只有`-config PATH`；任何字段都可用`DIANPING_`环境变量覆盖。示例：

```powershell
$env:DIANPING_SERVER_ADDRESS=':9081'
$env:DIANPING_MYSQL_DSN='USER:PASSWORD@tcp(127.0.0.1:3306)/go_dianping?charset=utf8mb4&parseTime=true&loc=Asia%2FShanghai'
$env:DIANPING_REDIS_ADDRESS='127.0.0.1:6379'
go run ./cmd/server -config configs/config.yaml
```

生产要设置release并关闭开发验证码日志，否则配置校验拒绝启动。正式短信接口未内置：关闭模拟后`POST /user/code`返回503，直到实现短信Sender。

## 初始化MySQL

创建空数据库后执行：

```powershell
go run ./cmd/migrate -seed
```

省略`-seed`只建表。迁移器拒绝直接操作检测到的旧Java库；旧库步骤见DATABASE.md。不要对原`hmdp.sql`直接重复执行，因为它包含DROP TABLE。

## 开发调试

在`cmd/server/main.go`的`run`设置入口断点；请求路径依次进入middleware → handler → service → repository。调试异步秒杀时在以下位置断点：

1. `VoucherService.Seckill`看Lua返回码与订单ID。
2. `OrderWorker.process`看Stream消息和Pending。
3. `Repository.OrderCreate`看事务、条件库存影响行数。
4. `finish.lua`执行后看status和ACK。

断点暂停超过`worker.claim_idle`可能让另一消费者接管同一消息；数据库幂等仍应保证正确，但单步时日志会出现重复处理。调试可把并发设1、claim_idle设5m。

读取Redis：

```powershell
docker compose exec redis redis-cli GET 'seckill:{seckill}:stock:2'
docker compose exec redis redis-cli XINFO GROUPS 'stream:{seckill}:orders'
docker compose exec redis redis-cli XPENDING 'stream:{seckill}:orders' orders
docker compose exec redis redis-cli XRANGE 'stream:{seckill}:dead' - + COUNT 20
```

读取MySQL：

```powershell
docker compose exec -e MYSQL_PWD=root_dev_only mysql mysql -uroot go_dianping -e "SELECT id,user_id,voucher_id,status FROM tb_voucher_order ORDER BY create_time DESC LIMIT 10"
docker compose exec -e MYSQL_PWD=root_dev_only mysql mysql -uroot go_dianping -e "SELECT voucher_id,stock FROM tb_seckill_voucher"
```

## 默认测试

```powershell
go mod tidy
go fmt ./...
go build ./...
go vet ./...
go test ./...
go test -race ./internal/cache ./internal/lock ./internal/redisx ./internal/service ./internal/worker
```

默认测试不要求外部服务：miniredis覆盖Lua、锁和缓存协议，sqlmock检查事务SQL，httptest检查路由和认证。miniredis不支持BITFIELD，连续签到算法由纯函数测试，完整命令由真实HTTP集成测试覆盖。

## 真实集成测试

使用独立`go_dianping_test`，不要指向生产库。下面为已启动Compose创建测试库并运行迁移：

```powershell
docker compose exec -e MYSQL_PWD=root_dev_only mysql mysql -uroot -e "CREATE DATABASE IF NOT EXISTS go_dianping_test CHARACTER SET utf8mb4; GRANT ALL ON go_dianping_test.* TO 'dianping'@'%';"
$env:DIANPING_MYSQL_DSN='dianping:dianping_dev@tcp(127.0.0.1:3307)/go_dianping_test?charset=utf8mb4&parseTime=true&loc=Asia%2FShanghai'
go run ./cmd/migrate -seed
$env:DIANPING_TEST_MYSQL_DSN=$env:DIANPING_MYSQL_DSN
$env:DIANPING_TEST_REDIS_ADDR='127.0.0.1:6380'
go test ./internal/service ./internal/handler ./internal/worker -run Integration -count=1 -v
```

Social使用Redis DB12、HTTP使用DB14、秒杀使用DB15；测试只删除自己的fixture，不执行FLUSHDB。测试覆盖真实事务、GEO/BITFIELD、并发点赞、Feed恢复、80请求抢7库存、Pending接管与ACK幂等。

## 常见故障

| 表现 | 原因与处理 |
|---|---|
| `database not initialized` | 先运行`go run ./cmd/migrate -seed` |
| `existing Java database detected` | 指向了旧库；按DATABASE.md备份、查重、人工升级 |
| `incomplete Redis seckill state` | 活动Key部分丢失或Stream已有未核对事件；停止入口，按SECKILL.md恢复，不能强灌DB库存 |
| 验证码接口503 | 生产模式关闭了日志模拟且未接短信商，行为符合配置 |
| 附近商铺503 | Redis不可用或GEO重建失败；检查Redis和商铺坐标，修复后ready标记会重建 |
| 商铺更新返回503但DB已变 | 缓存失效失败；恢复Redis后幂等重发同一更新 |
| 订单长期pending | 查XPENDING、API日志、DB连接；暂时错误应保留等待重试 |
| `failed`订单 | 查dead Stream和状态error；确定性失败已幂等补偿，不要直接重投旧消息 |
| 端口占用 | 修改Compose端口左侧或用环境变量修改本机启动地址 |

接口详情见API.md，本地浏览页是`http://localhost:8081/docs`。
