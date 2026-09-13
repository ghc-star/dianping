# Java 源码扫描与 Go 重构分析

本分析基于当前仓库全部 Controller、Service/Impl、10 个 Entity 与 Mapper、唯一 Mapper XML、11 张表的 SQL、配置、Lua、拦截器、工具和 3 个测试类。Java 源码保留在上层 `src/`，用于逐项对照。原 README 作为背景阅读，行为以可执行源码为准。

## 真实业务边界

| 模块 | 源码与实际调用链 | Go 处理 |
|---|---|---|
| 用户 | UserController → UserServiceImpl → MyBatis/Redis；验证码 String，Token Hash，30 分钟滑动续期 | 保留手机号验证码登录、原 Token Header 和 Result；增加验证码一次性消费、限速、真实注销 |
| 拦截 | RefreshTokenInterceptor → UserHolder(ThreadLocal) → LoginInterceptor | Gin Middleware，将用户放入请求 Context |
| 商铺 | ShopController → ShopServiceImpl → CacheClient.queryWithLogicalExpire | 冷 miss 回源、空值 TTL、抖动、singleflight、逻辑过期与安全失效 |
| 类型/GEO | 类型 Redis List；GEO 数据仅在测试导入 | 有 TTL 的整体类型缓存；启动与写入维护 GEO，按距离排序 |
| 社交 | BlogServiceImpl：点赞 DB + ZSet；发布 DB 后向粉丝 ZADD；FollowServiceImpl：DB + Set | 持久点赞明细、事务约束、可重试 Feed 投递、累加同毫秒游标 offset |
| 签到 | UserServiceImpl → SETBIT/BITFIELD | 保留按月连续签到，业务时区 Asia/Shanghai |
| 优惠券 | 普通券只写 tb_voucher；秒杀券写两表+Redis；VoucherMapper.xml LEFT JOIN | 保留金额单位“分”、状态与表关系 |
| 秒杀 | MySQL 时间检查 → RedisIdWorker → Lua(预扣+Set+XADD) → RabbitMQ → Listener → MySQL | 统一为 Lua 原子入 Stream，受管理消费者落库，Pending 恢复 |
| 上传 | UploadController 写硬编码 Windows 路径 | 可配置目录、图片格式与大小校验、归属校验、安全删除 |

`BlogCommentsController` 没有任何接口，Service 也是空 CRUD 继承。`tb_sign` 存在于 SQL，但没有业务 Mapper/Entity；实际签到只写 Redis。保留这些数据库表，不虚构原项目有评论、支付或退款接口。没有真正的定时任务或 Spring Async 业务。HyperLogLog 只有测试示例。PasswordEncoder 的 MD5 工具未被登录调用；DTO 有 password 不代表原实现支持密码登录。

## 秒杀必须修正的故障

1. `seckill.lua` 写 `stream.orders`，但仓库没有 Stream 消费者；Java 另向 RabbitMQ 发布相同订单。Redis 成功、RabbitMQ 失败会悬挂预扣。
2. `SeckillVoucherListener.handleOrder` 先插入订单，再做 `stock > 0` 扣减；更新失败只打日志，没有回滚，仍 ACK。
3. 只有订单主键，没有 `(user_id,voucher_id)` 唯一索引；按订单 ID 查重不能兜底一人一单。
4. 正常队列异常 NACK 进入死信队列，再失败会丢失。队列 10 秒 TTL 也不能替代可靠重试。
5. 启动 SETNX 仅预热库存，无法自动恢复丢失的预扣和购买资格。Go 不把跨 Redis/MySQL 的事务声称为原子事务，恢复边界在 SECKILL.md 说明。

## 缓存与社交缺陷

逻辑缓存 cold miss 直接返回 null；商铺更新删除缓存后，查询不会自动加载。锁固定 value=1 且直接 DEL，过期后可能误删其他持有者的锁。类型缓存逐项 RPUSH 且无 TTL，多实例同时加载会重复。Feed offset 在同一毫秒跨三页时未累计，可能重复。Follow 没有业务唯一索引，重复请求会产生重复关系。点赞的 Redis 与 MySQL 两步更新不能防并发漂移。以上通过可验证业务约束修复，并在模块文档说明最终一致性边界。

## 数据库检查

复用 `tb_*` 表名与 snake_case 列名。金额为 int64；时间按业务时区处理；`tb_user_info.level` 和评论 status 在 Java 中误映射 Boolean，Go 恢复整数。`user_info.user_id` 非自增。MySQL 8 严格模式不能使用秒杀时间的零日期默认值。原 SQL 含 DROP TABLE，Go 提供独立、不删除旧表的初始化，开发示例不复制 1005 条用户手机号。

原始数据含 14 家商铺、10 种类型、4 篇 Blog、1005 个用户和 1 张普通券；秒杀/订单/关注为空。学习项目提供独立示例数据和可操作的秒杀券，原数据导入及加唯一索引前的去重检查见 DATABASE.md。

## 接口兼容决定

保留既有 URL、HTTP Method、camelCase、`{success,errorMsg,data,total}`、裸 `authorization: token`，同时允许 Bearer。业务失败仍通过 success=false 返回；认证 401，基础设施错误 500/503。写商铺、写券和上传增加登录要求；这些原为匿名写接口，变化逐项写入 API.md。登录仍以 Redis 会话为权威，不引入无必要 JWT 双重过期语义；bcrypt 仅用于显式新增的密码设置/密码登录。

## 实现顺序

显式依赖组装 → Model/SQL → Repository → 用户中间件 → 商铺缓存 → 社交与 Feed → Lua/Stream/Worker → Handler/OpenAPI → Docker → 单元/集成测试 → 中文学习文档。各模块并行实施，公共契约统一；最后整体启动并验证完整 HTTP 调用链。
