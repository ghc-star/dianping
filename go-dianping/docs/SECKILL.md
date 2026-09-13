# 秒杀：从资格判断到可恢复的订单

这份文档的目标是让你能亲手重写本项目的秒杀链路，并能解释进程在每一步宕机之后会发生什么。先阅读 `internal/service/voucher.go` 的 `Seckill`，再按下面的顺序进入 Lua、Worker 和数据库事务。

## 1. 先理解 Java 仓库实际运行了什么

原仓库并非只有一种消息队列实现：

```text
VoucherOrderController.seckillVoucher
  → VoucherOrderServiceImpl.checkTimeWindow：MySQL 查秒杀时窗
  → RedisIdWorker.nextId
  → seckill.lua：扣 Redis 库存、记录 Set、XADD stream.orders
  → RabbitTemplate.convertAndSend：X / XA → QA
  → SeckillVoucherListener：QA 或 QD
  → TransactionTemplate：插入订单、扣 MySQL 库存
  → RabbitMQ ACK
```

`seckill.lua` 确实写了 Stream，但整个 Java 仓库没有 Stream 消费者。真正落库的是 RabbitMQ 监听器。原监听器在库存更新影响行数为 0 时只记录日志，仍提交订单并 ACK；订单表也没有 `(user_id, voucher_id)` 唯一索引。RabbitMQ 发送失败不能回滚 Redis 预扣，QD 再次处理失败会丢弃消息。

Go 版本把消息入口统一为 Redis Stream，并补全库存条件更新、唯一约束、事务回滚、Pending 恢复和确定性失败补偿。RabbitMQ 不再是运行依赖。

**迁移现有 Java 运行数据时**，先停止 Java 秒杀入口，核对 QA/QD 和数据库订单。旧 `stream.orders` 可能保存了已经通过 RabbitMQ 落库的同一张订单，不能把其中每条消息都视为“尚未处理”。应按订单 ID、用户、券核对旧 Stream 与数据库，再决定恢复未完成订单。Go 使用新的带 `{seckill}` 的键，不会自动读取旧 Stream；不要让 Java 与 Go 对同一活动同时售卖。

## 2. 四个组件分别保证什么

```text
POST /voucher-order/seckill/:id（登录用户）
  → VoucherService.Seckill(ctx, voucherID, userID)
  → seckill.lua：资格 + 预扣 + Stream 事件
  → HTTP 返回订单 ID（此时仅代表受理）

OrderWorker.Run(ctx)
  → XREADGROUP 新消息 / XAUTOCLAIM 历史 Pending
  → Repository.OrderCreate(ctx, order)
  → MySQL 条件扣库存 + INSERT，事务 COMMIT
  → finish.lua：订单状态 created + XACK
```

| 组件 | 职责 | 没有提供的保证 |
|---|---|---|
| Redis Lua | 同一次脚本内检查时窗、库存、购买资格，预扣库存并写入事件 | 不等于 MySQL 的持久事务，也没有运行时错误自动回滚 |
| Redis Stream | 保留事件、消费组分发、记录待确认消息 | 不保证业务只执行一次；ACK 也不会删除事件本体 |
| goroutine | 在后台并发执行消费任务，避免 HTTP 等待数据库落单 | 内存线程不是持久队列；进程退出后 goroutine 不存在 |
| MySQL 事务与索引 | 最终订单、库存不可分割地提交，一人一单兜底 | 无法单独证明 Redis 中有多少尚未落库的预扣 |

正确性来自这些机制的组合。一次请求返回订单号之后，前端应查询订单状态，区分 `pending`、`created`、`failed`。订单查询和状态查询都会校验当前用户，不能通过别人的订单号读取数据。默认响应中的数字订单ID保留Java兼容；浏览器应传`idAsString=true`或读取`X-Order-ID`响应头，避免JavaScript Number丢失int64精度。

## 3. 为什么不能先查库存再直接扣减

假设库存为 1。请求 A 和 B 都执行 `SELECT stock`，都得到 1。若随后各插入一张订单，就卖出 2 张券，这叫超卖。

数据库本身可以使用下面的条件更新保证不出现负库存：

```sql
UPDATE tb_seckill_voucher
SET stock = stock - 1
WHERE voucher_id = ? AND stock > 0;
```

检查影响行数是否为 1，并将其与插单放入同一事务，就能正确处理库存。高峰期若让全部失败请求也争抢同一行锁，数据库会承受大量无效竞争。Redis 的作用是提前筛掉库存耗尽和重复购买请求，减少最终落库压力。

“一人一单”在这里指同一用户只能购买同一张秒杀券一次。不同券仍可购买。本项目没有付款、退款、取消后再购工作流，不能把删除购买标记当作退款实现。

## 4. 读懂 `seckill.lua`

文件：`internal/redisx/seckill.lua`。脚本通过 `KEYS` 接收键名，通过 `ARGV` 接收用户 ID、券 ID、订单 ID。

执行步骤如下：

1. 验证所有键类型及库存/时间值，检测错误类型和部分数据丢失。
2. 通过 Redis `TIME` 检查秒杀开始、结束时间，避免多台 API 机器时钟差异导致资格判断不一致。
3. 通过购买 Hash 的 `HEXISTS userID` 判断是否已购买。
4. 检查剩余库存是否大于 0。
5. `XADD` 保存订单事件。
6. `DECR` 预扣库存，`HSET userID orderID` 保存购买归属，写入 `pending` 状态。

同一 Redis 服务中的脚本执行不会与其他命令交错。因此两个竞争请求不会同时读到最后一份库存再各自扣减。脚本要保持简短，否则会阻塞其他 Redis 请求。参阅 [Redis Lua 脚本说明](https://redis.io/docs/latest/develop/programmability/eval-intro/)。

**原子执行不等于发生错误后自动撤销。** 因此本项目先检查 `TYPE` 和数值，再进行写入；并把 `XADD` 放在第一次库存修改前，避免队列类型错误导致“库存已经扣了，消息却没有写进去”。内存不足、Redis 存储故障、管理员删除数据等灾难性情况仍需要监控和恢复措施，不能由一句“Lua 是原子的”掩盖。

返回值：`0` 受理；`1` 库存不足；`2` 重复购买；`3` 未开始；`4` 已结束；负数代表活动状态缺失或键类型异常。缺失时不自动从数据库重建实时库存。

## 5. Redis Key 与 TTL

键集中声明在 `internal/redisx/seckill_keys.go`。其中 `{seckill}` 是 Redis Cluster hash tag，使一个脚本访问的键处在相同槽位。本项目客户端部署为单机 Redis；仅使用 hash tag 不代表已经支持完整 Cluster 运维。

| Key | 类型 | TTL | 内容 |
|---|---|---|---|
| `seckill:{seckill}:stock:{voucherID}` | String | 无 | 未被受理请求预占的库存 |
| `seckill:{seckill}:meta:{voucherID}` | Hash | 无 | `begin` / `end`，Unix 毫秒 |
| `seckill:{seckill}:buyers:{voucherID}` | Hash | 无 | `userID → orderID`，及 `__initialized` 标记 |
| `seckill:{seckill}:status:{orderID}` | Hash | 无 | `id`、`userId`、`voucherId`、`state`、可选 `error` |
| `stream:{seckill}:orders` | Stream | 无 | 字段 `id`、`userId`、`voucherId` |
| `stream:{seckill}:dead` | Stream | 无 | 确定性拒绝或无法解析的消息及原因 |
| `seckill:{seckill}:id:YYYY:MM:DD` | String | 首次递增后 72 小时 | UTC 每日订单 ID 序列 |

购买 Hash 比仅存用户 ID 的 Set 多记录了预扣属于哪张订单，因此补偿时可以确认“这次释放的是自己的预扣”。空 Hash 会被 Redis 删除，所以用 `__initialized` 保留空活动的初始化标记；Hash 整体丢失时会停止该活动受理。

这些库存、购买标记、状态和 Stream 没有自动过期。业务方完成活动结算、核对所有 Pending 与订单后，才适合做归档和删除。不能对活跃订单 Stream 直接 `MAXLEN` 裁剪，否则可能删除仍待消费的消息。本学习项目保留完整事件，长时间运行时需要规划容量与归档。

## 6. Consumer Group、ACK 和 Pending

`OrderWorker.Run` 先执行 `XGROUP CREATE ... 0 MKSTREAM`。从 `0` 建组能消费 Worker 启动前已经受理的消息；若从 `$` 开始，就可能跳过这些旧消息。组名固定为 `orders`，每个进程、每个 Worker 的消费者名称不同。

`XREADGROUP ... >` 读取尚未分发给该组的新消息。Redis 分发之后，将消息放入 Pending Entries List，简称 PEL。这里记录已交付但尚未确认的消息。

数据库提交之后才执行 `finish.lua`，它写 `created` 并 `XACK`。ACK 只是移除 PEL 中的待确认记录，Stream 本体仍保留。参阅 [XACK 文档](https://redis.io/docs/latest/commands/xack/)。

每个消费循环还使用 `XAUTOCLAIM` 扫描空闲时间足够长的 Pending，并沿返回的游标继续扫描。它能接管已经消失的消费者留下的消息；只读取“自己的 Pending”无法解决进程重启后消费者名称变化的问题。参阅 [XAUTOCLAIM 文档](https://redis.io/docs/latest/commands/xautoclaim/)。

默认单条处理超时 10 秒，Pending 接管阈值 30 秒。正式运行时，接管阈值应大于正常单条处理的最大耗时。即使误接管导致两次处理并行，数据库约束仍要保证正确，不能把阈值当成唯一保护。

## 7. 事务与重复消费

文件：`internal/repository/order.go`，函数 `OrderCreate`。

1. 查订单 ID。若 ID、用户、券都相同，则已经持久化，可以成功返回。
2. 查同一用户、同一券是否已有订单；若存在其他订单，返回确定性重复购买。
3. 用 `stock > 0` 条件扣库存。影响行数不是 1 时返回错误。
4. 插入订单；插入失败，库存更新跟随事务回滚。
5. 全部成功后提交。

表上的主键保证订单 ID 唯一，`UNIQUE(user_id, voucher_id)` 保证一人一单。只做“先查询，没有再插入”不足以替代唯一索引，因为两笔事务可以同时查询到不存在。

GORM 的 `Transaction` 回调返回非空 error 会回滚，返回 nil 才提交。参阅 [GORM 事务说明](https://gorm.io/docs/transactions.html)。

还有一个容易忽略的并发边界：消费者 B 查询时尚未看到 A 的订单，等待库存行锁之后却发现库存已被 A 扣光。B 此时不能马上补偿。`OrderCreate` 在库存失败、唯一索引冲突后会用**新事务快照**重查订单：若同一张订单已被并发提交，就按成功处理。测试 `TestStockRejectionRollsBackAndRechecksConcurrentCommit` 专门覆盖这个条件。

## 8. 故障后如何继续

| 故障发生位置 | 后续行为 |
|---|---|
| Lua 受理成功，HTTP 回包丢失 | Stream 中仍有事件；再次购买被购买 Hash 拦截。若客户端没有订单 ID，需要运维按用户与券核对购买 Hash 或数据库订单后恢复查询，不能直接删除购买标记 |
| Worker 读到消息但事务尚未开始就宕机 | 留在 PEL，其他消费者通过 XAUTOCLAIM 接管 |
| 事务内插单失败 | 库存回滚；网络异常等暂时性错误留 Pending 重试 |
| COMMIT 已成功，但 Redis 状态写入或 ACK 失败 | 再次消费时查到相同订单，不重复扣库存，补写状态与 ACK |
| 数据库明确库存不足或已有其他购买订单 | `reject.lua` 标记 failed，记录死信，按预扣归属补一次库存，最后 ACK |
| 消息缺少合法订单/用户/券 ID | 保存原始 payload 到 dead Stream 并 ACK；无法证明预扣归属时不能猜测并补库存，需要人工核对 |
| Redis 补偿失败 | 保留 Pending；下次重试同一个幂等补偿脚本 |

网络错误、死锁、请求取消、COMMIT 结果未知都不能直接归类为“业务失败”。尤其不能因为重试了 3 次，就退库存并让用户再买一次；第一次事务可能已经提交。

`reject.lua` 只有在 `buyers[userID] == 当前 orderID` 时释放预扣。重复调用看到 `failed` 会直接 ACK，不会再次加库存。若数据库里已有另一张有效订单，购买 Hash 改成该订单 ID，继续阻止重复购买。

死信并不意味着“可以不管了”。它保留错误证据，让你排查数据不一致。不要直接重投已经补偿的失败订单消息；应先决定业务是否允许重新申请、核对库存与数据库，再走新的受理流程。

## 9. 进程启动、退出和数据恢复

启动顺序必须是：数据库/Redis 可用 → `VoucherService.Warm` → `OrderWorker.Run` → 开放流量。

`Warm` 的规则是：完整活动缓存已经存在时原样保留；空 Redis 首次初始化时使用数据库库存，并把已落库订单种入购买 Hash；部分活动键缺失时返回错误；活动全部键缺失但 Stream 已经存在时，也拒绝按数据库库存重建。因为数据库库存没有计入仍在排队的预扣，直接 SET 会把这些库存卖第二次。

退出时由 main 取消根 Context。Worker 用 WaitGroup 等待固定数量消费者退出，阻塞读有 1 秒上限，数据库和 Redis 都接收 Context。Worker 在每条消息边界恢复 panic 并保留 Pending。没有无限期存活的裸 `go consume()`。

### 完全丢失 Redis 不能只靠 Warm 修好

如果库存、购买标记和 Stream 全部丢失，应用无法从 MySQL 推导出“已向客户端返回受理，但还没持久化”的订单。冷启动初始化只能恢复已经提交的数据，不能恢复已经消失的事件。

因此运行环境应保留 Redis 数据卷、开启 AOF，使用适合业务的持久化和备份策略。`appendfsync everysec` 仍存在故障时丢失最近写入的窗口。强保证场景还需评估同步持久化、复制确认及持久化请求日志，而不能宣称本项目对 Redis 全量数据丢失提供零丢单保证。检测到数据损坏时应停止秒杀入口，恢复 Redis 备份，核对数据库订单和未确认消息，然后再开放流量。

### 新增券提交数据库后 Redis 初始化失败

`Create` 的数据库事务与 Redis 不处于同一事务，错误中会包含已经生成的券 ID。此券不是一个可以靠回滚整个 HTTP 请求撤销的对象。不要盲目反复创建相同活动。

对**刚创建、从未受理过订单**的券，可以按下面流程恢复：

1. 停止秒杀入口和消费者，保留 Redis 数据，不删除 Stream。
2. 用券 ID 检查 `tb_voucher`、`tb_seckill_voucher`，确认 `tb_voucher_order WHERE voucher_id = ...` 为 0。
3. 分页检查 orders / dead Stream 和活动 keys，确认该券没有历史受理记录、没有任何预扣。仅订单表为 0 不足以证明没有排队请求。
4. 从 `tb_seckill_voucher` 获取准确库存与开始/结束 Unix 毫秒，执行下面的初始化脚本。`new` 表示你已经完成前面的无历史受理核对，不是一个自动忽略故障的开关。
5. 验证库存、meta、buyers 的类型和值，重启 API。若存在历史受理，转为逐单核对和备份恢复，不能使用该快捷路径。

```bash
# 在 go-dianping 目录，替换 VOUCHER_ID、STOCK、BEGIN_MS、END_MS，连接正确的 Redis。
redis-cli --eval internal/redisx/initialize.lua \
  'seckill:{seckill}:stock:VOUCHER_ID' \
  'seckill:{seckill}:meta:VOUCHER_ID' \
  'seckill:{seckill}:buyers:VOUCHER_ID' \
  'stream:{seckill}:orders' , STOCK BEGIN_MS END_MS new
```

脚本在完整状态存在时返回 `0`、完成新初始化返回 `1`、发现部分状态时返回负数并拒绝写入。它不会覆盖已经预扣的实时库存。

## 10. 分布式锁在这里怎么学

文件：`internal/lock/redis.go`，函数 `TryAcquire` 和 `Lease.Release`。商铺缓存重建会使用它减少重复工作；秒杀正确性由 Lua、唯一索引和事务保证，无需在每个下单请求外再套一把大锁。

获取锁通过 `SET key uniqueValue NX PX ttl`，锁值使用随机字节。TTL 避免进程崩溃导致锁永远不释放。释放锁使用 Lua 比较当前值与自己的随机值，相等才 `DEL`。

假设 A 的锁过期，B 已经拿到新锁。A 此时直接 `DEL` 会删除 B 的锁。即使用 `GET` 比较后再 `DEL`，两条命令之间也可能发生过期与换主，所以比较和删除必须放在同一个脚本里。

本实现没有自动续租，也不是可重入锁。临界区可能比 TTL 更长时，锁只能减少重复工作，不能代替业务约束。`TestExpiredOwnerCannotUnlockNewOwner` 演示旧持有者不能删除新持有者的锁。

## 11. 建议你自己重写的顺序

| 顺序 | 自己实现什么 | 怎么证明理解了 |
|---|---|---|
| 1 | 库存条件更新 + 事务插单 | 制造 INSERT 失败，数据库库存不减少 |
| 2 | 一人一单唯一索引 | 并发重复请求最多插入一单 |
| 3 | 不含队列的 Lua 资格判断 | 100 人抢 10 张，成功数量为 10 |
| 4 | Lua 加 XADD，状态设 pending | 受理数、Stream 事件数、库存变化一致 |
| 5 | 单个 XREADGROUP 消费者 | 提交后才 ACK，数据库故障时 Pending 增长 |
| 6 | XAUTOCLAIM + 幂等事务 | 旧消费者宕机后，新消费者能继续；库存只扣一次 |
| 7 | Context + WaitGroup | 取消 Context 后消费者全部退出 |
| 8 | 确定性拒绝与幂等补偿 | 同一补偿重放两次，库存只增加一次 |
| 9 | 错误类型/部分数据丢失防御 | 破坏测试 Redis 的 Stream 类型，库存保持原值 |

## 12. 测试和排查命令

普通测试不需要外部服务，使用 miniredis 执行 Lua/Stream 行为，使用 sqlmock 验证事务边界。它们不能取代真实 Redis 和 MySQL 的集成测试。

```bash
go test ./internal/redisx ./internal/lock ./internal/repository ./internal/worker
go test -race ./internal/redisx ./internal/lock ./internal/worker
```

`TestSeckillIntegration` 使用真实 Redis Lua、Stream 和 MySQL 事务：80 个并发请求竞争 7 份库存，模拟旧消费者留下 Pending，再由新 Worker 接管落库。测试数据库必须已执行本项目 migration。Redis 固定使用 DB 15，测试只删除自己的券、订单、状态和 Stream 消息，不执行 FLUSHDB。

```bash
export DIANPING_TEST_MYSQL_DSN='USER:PASSWORD@tcp(127.0.0.1:3306)/go_dianping_test?charset=utf8mb4&parseTime=true&loc=Local'
export DIANPING_TEST_REDIS_ADDR='127.0.0.1:6379'
go test ./internal/worker -run TestSeckillIntegration -count=1 -v
```

PowerShell 使用 `$env:DIANPING_TEST_MYSQL_DSN = '...'` 和 `$env:DIANPING_TEST_REDIS_ADDR = '...'` 设置同名变量。不要把密码提交进仓库。

下面命令只读，可以观察队列：

```bash
redis-cli XINFO GROUPS 'stream:{seckill}:orders'
redis-cli XPENDING 'stream:{seckill}:orders' orders
redis-cli XRANGE 'stream:{seckill}:dead' - + COUNT 20
redis-cli HGETALL 'seckill:{seckill}:status:ORDER_ID'
```

观察库存时要区分 Redis 预扣库存和 MySQL 持久库存：有 Pending 时，Redis 库存暂时少于数据库库存是正常现象；待所有消息成功落库、且没有失败补偿差异之后再做最终核对。
