# 架构与请求链路

这是一个单体 Go API，外加受同一进程管理的订单消费者和 Feed 投递循环。选择具体 struct 和构造函数，让初学者能沿着真实依赖读代码；没有为了模仿 Spring 增加 IService/ServiceImpl 两套类型。

```text
HTTP / Nginx
  → Gin Router
  → AccessLog / Recovery
  → Auth：Redis Token Hash → gin.Context("user") → RequireLogin
  → Handler：Path/Query/JSON校验、调用业务、Result序列化
  → Service：业务流程与Redis协调
     ├─ Cache / Lua / Redis
     └─ Repository：db.WithContext(ctx) → MySQL

秒杀请求 → Lua预扣并XADD → Stream消费组 → OrderWorker
                                       → Repository.OrderCreate事务
                                       → 状态created + XACK
Blog发布 → 同事务Blog+FeedOutbox → SocialService.Run → ZADD → 标记投递
```

## 启动从哪里看

`cmd/server/main.go: run` 依次加载配置、创建Zap、建立信号Context、打开MySQL和Redis、组装Repository和Service、预热GEO及秒杀状态、启动HTTP与两个后台循环。`cmd/migrate` 独立管理DDL，服务启动不会AutoMigrate偷偷改结构。

`internal/bootstrap/database.go: Database` 配置连接池20个打开连接、5个空闲连接、30分钟连接寿命；GORM客户端可以被多goroutine使用，事务句柄只留在事务闭包里。不要把一个请求的Context存在Repository字段中。

## 各目录的边界

| 目录 | 负责 | 不应放入 |
|---|---|---|
| handler | HTTP输入、校验、输出与文件上传 | 秒杀库存规则、SQL拼接 |
| middleware | 身份、恢复panic、访问日志 | 用户注册和订单业务 |
| service | 参数业务约束、事务/缓存的编排 | 从Gin读取Header |
| repository | 明确表查询、SQL事务、条件更新 | HTTP状态码 |
| cache | 空值、TTL抖动、逻辑过期、singleflight、版本失效 | 商铺字段知识 |
| redisx | Redis键、嵌入Lua、订单ID | Handler逻辑 |
| worker | 消费组、重试、Pending、ACK、退出 | 信任消息直接无限重试 |
| model/dto | 数据库映射、对外精简用户等对象 | 全局用户状态 |

`Repository`是具体对象，不是每张表一个空接口。将来接第二种存储或需要更窄的测试替身时，在调用方定义只有需要方法的接口，而不是预先铺满所有层。

## Context与并发所有权

Handler传`c.Request.Context()`；Service把它交给go-redis和Repository；Repository用`DB.WithContext(ctx)`。HTTP基础Context来自进程生命周期，停机取消请求，未完成订单消息留Pending。GORM NowFunc和业务签到使用配置的Asia/Shanghai，Lua资格校验用Redis TIME避免多API节点时间不一致。

进程用`errgroup`等待HTTP、订单、Feed与停机协程。订单Run内部用WaitGroup等待有限数量消费者。缓存后台刷新有8个并发槽，5秒超时，Close取消并等待；不会每次miss无限启动goroutine。channel仅用于缓存并发槽和测试同步；没有需要Mutex时就不强塞RWMutex。

普通业务失败返回error；Handler统一转换。业务错误通过`errors.As`识别，原Result业务失败HTTP200；未登录401，依赖503，未知错误500。`fmt.Errorf("...: %w", err)`保留根因。普通业务不用panic；HTTP/缓存刷新/订单消息/Feed循环各有panic边界。

## 配置与日志

`configs/config.yaml`通过Viper加载，同名环境变量`DIANPING_`覆盖，例如`DIANPING_REDIS_ADDRESS`、`DIANPING_MYSQL_DSN`。`-config`选择其他配置。Docker中服务DNS为mysql/redis，本机端口是3307/6380。

Zap输出JSON，访问日志记录Method、Path、状态、耗时，不记录Token、请求Body或完整手机号。开发短信仅记录手机号后四位和验证码；release模式拒绝开启此模拟。尚未接第三方短信接口，关闭模拟时接口明确503。错误日志保留内部根因，对外不暴露SQL/凭据。

## 一致性边界

MySQL和Redis没有共同事务。缓存失败可回退DB；商铺修改后失效失败明确返回“数据库已更新”并允许幂等重试。Feed用事务outbox恢复。点赞/关注以DB明细为权威，可恢复Redis投影。秒杀预扣依赖持久Redis，MySQL唯一索引与库存条件更新做最后约束，不能恢复已经随Redis磁盘丢失的未落库请求。

本项目的点赞排行榜和共同关注优先清晰与可恢复，会锁DB行并重建投影；高粉丝量发布会写大量outbox，不能把教学实现当成无限吞吐设计。扩展时先测量，再考虑批处理、独立投递任务和分片，见SOCIAL.md。
