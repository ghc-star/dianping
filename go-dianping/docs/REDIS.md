# Redis 数据结构、Key 与失败边界

所有业务Key在`internal/redisx`集中声明。动态部分以大括号描述，表中的TTL是当前默认配置；0/无表示不自动过期，需要业务归档。

| Key | 类型 | TTL | 用途与恢复来源 |
|---|---|---:|---|
| login:code:{phone} | String | 2min | 6位验证码；正确验证时Lua删除 |
| login:throttle:{phone} | String | 60s | 发送限速 |
| login:attempts:{phone} | String | 2min | 验证码失败次数，最多5次 |
| login:attempts:password:{phone} | String | 2min | 密码失败次数，最多5次 |
| login:token:{token} | Hash | 30min滑动 | id,nickName,icon；注销删除；MySQL用户可重新登录恢复 |
| cache:shop:{id} | String(JSON envelope) | 正常35–38min；空值2min | 商铺内容、逻辑freshUntil；MySQL可重建 |
| lock:shop:{id} | String | 10s | 多实例缓存回源租约；唯一token，Lua安全释放 |
| cache:shop:version:{id} | String | 更新后24h | 更新代数，阻止旧查询覆盖新缓存 |
| shop_type: | String(JSON) | 35–38min | 按sort排序的类型整体数组；MySQL可重建 |
| lock:shop_type | String | 10s | 类型缓存回源租约 |
| cache:shop_type:version | String | 更新后24h | 类型缓存版本；当前无类型写接口，保留完整缓存协议 |
| shop:geo:{typeId} | GEO/ZSet | 无 | 商铺ID与坐标；MySQL启动预热、新增/更新维护 |
| shop:geo:ready | String | 10min | GEO索引近期校验标志；缺失触发幂等补充 |
| blog:liked:{blogId} | ZSet | 无 | userID→点赞毫秒；`tb_blog_like`权威，可重建 |
| follows:{userId} | Set | 无 | 被关注用户ID；`tb_follow`权威，可重建 |
| feed:{userId} | ZSet | 无 | blogID→发布毫秒，滚动分页收件箱；outbox历史可重建Go时期数据 |
| feed:ready:{userId} | String | 24h | 收件箱完成一次历史重建标记；收件箱单独丢失也会重新检查 |
| sign:{userId}:yyyyMM | Bitmap/String | 无 | 当月每天一bit；没有MySQL恢复源，按留存策略归档 |
| seckill:{seckill}:stock:{voucherId} | String | 无 | 资格层实时剩余库存；持久Redis业务状态 |
| seckill:{seckill}:meta:{voucherId} | Hash | 无 | begin/end Unix毫秒 |
| seckill:{seckill}:buyers:{voucherId} | Hash | 无 | userID→orderID和初始化标志；补偿须校验归属 |
| seckill:{seckill}:status:{orderId} | Hash | 无 | pending/created/failed与错误原因 |
| stream:{seckill}:orders | Stream | 无 | 订单事件和消费组orders；不要裁剪未确认消息 |
| stream:{seckill}:dead | Stream | 无 | 格式错误或确定性拒绝证据 |
| seckill:{seckill}:id:YYYY:MM:DD | String | 72h | UTC日序列，组合int64订单ID |

`{seckill}`是Redis Cluster hash tag，使一段Lua访问的所有Key落在同一slot。本Compose使用单机Redis；这只为未来Cluster键槽兼容做准备，不代表应用已经覆盖Cluster故障转移。

## 为什么选择这些结构

String适合验证码、计数器和缓存JSON；Hash允许会话和订单状态按字段读写；Set让关注交集用SINTER；ZSet把时间当score，支持点赞顺序和Feed游标；Bitmap用31位以内表示一个月，极省内存；GEO基于ZSet做半径搜索；Stream提供消费组、Pending和ACK。

Feed不能使用普通页码offset：新Blog插到头部后，第二页位置会整体移动，导致重复或漏读。它用`max timestamp + offset`；多条消息同一毫秒时下一页累加该分值已消费数量。

## 缓存三类问题

- 穿透：数据库不存在的商铺缓存Missing包裹，2分钟后再确认。DB错误不会当不存在缓存。
- 雪崩：正常TTL增加0–10%抖动，不让大量Key同秒过期。
- 击穿：进程内singleflight合并，进程间SET NX租约；逻辑过期先返回旧值，由最多8个后台任务刷新。

商铺更新提交MySQL后，用Lua递增version并DEL缓存。更新前开始的慢查询即使最后完成，也因版本不同无法回填旧值。Redis不可用时查询可直接读DB；更新失效失败会返回503并明确数据库已更新，允许重复相同更新。

## 为什么锁不能直接DEL

A拿锁后暂停，TTL过期，B取得同名锁。A恢复后直接DEL会删掉B的锁。每次加锁必须写唯一随机value，解锁Lua在同一次原子执行中比较value后删除。先GET再DEL仍有命令间竞态。本项目锁不自动续租，数据库唯一约束才是业务正确性的最后边界。

## 秒杀的持久性边界

Lua保证命令不与其他客户端交错，却不会在运行时错误后回滚已经执行的写入。因此脚本在第一次写前检查全部Key类型和值，并先XADD再预扣。Redis Stream提供至少一次处理：DB提交后才ACK；宕机消息留Pending，由XAUTOCLAIM接管。

Compose启用AOF everysec和持久卷，并用`maxmemory-policy noeviction`避免订单键被逐出。但AOF everysec仍可能在主机故障时丢失约1秒窗口。库存、购买Hash和Stream全部丢失时，MySQL无法推导“已经返回受理但尚未落库”的请求，不能按数据库库存盲目重建。完整恢复矩阵见SECKILL.md。

## 观察与排查

Docker Redis暴露在本机6380：

```bash
docker compose exec redis redis-cli TYPE 'stream:{seckill}:orders'
docker compose exec redis redis-cli XINFO GROUPS 'stream:{seckill}:orders'
docker compose exec redis redis-cli XPENDING 'stream:{seckill}:orders' orders
docker compose exec redis redis-cli XRANGE 'stream:{seckill}:dead' - + COUNT 20
docker compose exec redis redis-cli GET 'seckill:{seckill}:stock:2'
docker compose exec redis redis-cli HGETALL 'seckill:{seckill}:buyers:2'
```

不要在共享环境执行FLUSHDB。测试分别使用Redis DB 12、14、15并只清理自身fixture；应用使用DB 0。
