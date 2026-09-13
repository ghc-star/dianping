# 学习顺序：从能运行到自己实现

先按README启动，用两个手机号完成登录、关注、发Blog、Feed、秒杀。观察一次成功和一次错误，再读代码。不要第一天就读完整Lua和消费者。

| 阶段 | 知识点 → 对应文件 | 为什么学 | 自己手写 | 验证 |
|---|---|---|---|---|
| 1：第一天 | package/module、struct、tag、指针、method → go.mod、model/models.go、dto/user.go | 建立数据形状，不把Go当Java语法替换 | 定义UserDTO与TableName，写金额整数计算 | go test；JSON字段是nickName且不含password |
| 2：HTTP | Gin Router、绑定、validator、中间件 → handler/router.go,input.go、middleware | 看清请求在哪里验证、身份从哪来 | /user/me、正整数Path校验、统一Result | httptest：无Token401、错误ID业务失败 |
| 3：数据库 | GORM、CRUD、唯一索引、事务、Context → repository/user.go,shop.go、migrations | 建立DB权威和错误传播意识 | FindOrCreateUser并发注册、0值商铺更新 | 同手机号只有1行；score:0真的落库 |
| 4：会话与Redis入门 | String/Hash/TTL、crypto/rand、bcrypt → service/user.go | 学到认证状态而非只会签Token | 生成6位码、原子消费、Hash会话续期注销 | 码过期/重放失败；注销后旧Token401 |
| 5：缓存 | Cache Aside、空值、TTL抖动、singleflight、逻辑过期 → cache/cache.go、service/shop.go | 缓存正确性比GET/SET更重要 | 先普通回源，再空值，再singleflight，最后版本保护 | 30并发只加载一次；更新期间旧数据不回填 |
| 6：社交数据结构 | Set/ZSet/Bitmap/GEO → service/social.go,user.go,shop.go | 从业务选择数据结构 | 点赞切换、共同关注、今日签到、附近商铺 | 重复关注1行、连续签到、距离排序 |
| 7：并发与退出 | goroutine/channel/select/WaitGroup/Mutex/cancel → cache.refresh/Close、worker.Run、main.run | 并发必须有退出所有者 | 带ctx的定时循环、限流channel、panic边界 | cancel后任务退出，race检查无共享数据竞争 |
| 8：可靠Feed | outbox、事务、滚动游标 → repository.CreateSocialBlog、service.Feed/Run | 了解异步失败后的业务恢复 | DB+投递意图同事务；ZADD后标记；累加offset | Redis故障仍保留Blog；恢复补投；同毫秒3页无重复 |
| 9：秒杀 | Lua原子性、Stream Group/Pending/ACK → redisx/*.lua、worker/orders.go | 最后组合前面所有知识 | 资格判断、预扣入队、事务订单、恢复消费 | 80并发仅7成功；重启恢复7单；库存为0 |
| 10：运维复盘 | Docker/Nginx、配置、日志、调试 → compose、RUNNING.md | 区分“编译成功”与“系统可靠” | 从空测试库启动，模拟Redis故障与进程退出 | ready正常，故障日志可定位，停止不丢Pending |

## 每一阶段的学习方法

1. 先写一条curl或httptest，说明输入与期望。
2. 沿Handler → Service → Repository读一次，记下哪些函数可能失败。
3. 暂时关闭答案文件，按IMPLEMENTATION_GUIDE写最小版本。
4. 先跑成功路径，再故意传错参数、制造重复请求、取消Context。
5. 对照答案寻找缺失的业务约束，不追求变量名一致。

阶段1至少亲自解释：`*User`和`User`的区别、slice不是Java ArrayList、error是接口值、defer何时执行。阶段7对Mutex与RWMutex做一个小练习：读多不代表一定用RWMutex，锁持有时间与竞争才决定收益；项目没有需要RWMutex的业务就不添加。

## 推荐挑战与难度

| 模块 | 难度 | 合格标准 |
|---|---|---|
| Bitmap签到 | ★ | 月初、今天未签、连续多天结果正确 |
| 验证码登录 | ★★ | TTL、一次消费、注销、并发注册唯一 |
| GEO | ★★ | 同传坐标、单位米、排序、维护索引 |
| Cache Aside | ★★ | cold miss与不存在都正确 |
| singleflight | ★★★ | 并发回源合并，不缓存DB故障 |
| 安全分布式锁 | ★★★ | 锁过期后旧持有者不能删新锁 |
| 点赞与关注 | ★★★ | 并发一致、持久明细、Redis恢复 |
| Feed滚动分页 | ★★★★ | 3页同时间分数无重复，outbox可重投 |
| Redis Stream消费者 | ★★★★ | 旧消费者Pending也能恢复；退出有界 |
| 完整秒杀 | ★★★★★ | 无负库存、无重复用户订单、未知提交结果可重试 |

最后独立画出一次“DB提交成功、Redis ACK失败”的时间线，再说明为何重放不能再扣库存。能准确回答这一题，比背诵所有Redis命令更重要。
