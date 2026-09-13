# 从当前 Java 项目理解 Go

请把上层`src/main/java/com/hmdp`与`internal/`并排打开。目的不是把注解换成语法，而是找到相同业务规则在Go中的归属。

| 本项目Java能力 | Go对应文件/函数 | 改变 |
|---|---|---|
| SpringBootApplication | cmd/server/main.go run | main显式创建依赖并负责关闭 |
| @Autowired/@Resource | NewUser/NewShop/NewSocial/NewVoucher | 构造参数一眼可见依赖，不做运行时扫描 |
| Controller/@RequestMapping | handler.New | Router显式登记Method/路径 |
| Service接口+ServiceImpl | service里的具体struct | 无真实替换需求不造两层类型 |
| MyBatis Plus/Mapper | repository + GORM | 明确TableName/column，事务闭包返回error |
| RedisTemplate | go-redis/v9 | 每次操作显式带context，必须检查Err |
| application.yaml | config.Load/Viper | 配置文件加环境覆盖和启动校验 |
| Interceptor | middleware.Auth/RequireLogin | 用户在当前Gin Context中 |
| UserHolder/ThreadLocal | middleware.User/ UserID | 无线程绑定、无全局当前用户 |
| CacheClient线程池 | cache.Client.refresh/Close | 有界goroutine、cancel、WaitGroup |
| SimpleRedisLock/Redisson配置 | lock.Redis | SET NX PX和Lua比较后删除；原Redisson未参与活跃链路 |
| RabbitListener | worker.OrderWorker.Run | 改消费原Lua已使用的Redis Stream，统一唯一队列 |
| RuntimeException/Advice | error/apperror/response.Fail | 正常失败是显式返回值，不展开调用栈 |
| Lombok Entity/DTO | model/dto struct + tags | 数据库列名和JSON字段名分别声明 |
| Java集合/Stream | slice/map/for | 用显式循环维持Feed结果顺序 |

## 为什么不需要Spring式IOC

Go的`main`本来就能组装对象：`repo := repository.New(db)`，再`users := service.NewUser(repo,rdb,log,cfg)`。构造时类型检查能发现缺少依赖，阅读main能看见启动顺序。项目只有数个Service，框架容器并不能省去业务复杂度，反而会隐藏初始化失败和资源退出。

struct并不自动等同于Spring的单例Component。这里在main只创建一次，所以Service共享连接池；请求数据放在局部变量，不能放Service字段导致串号。

## interface在需要的地方才出现

Go接口由实现的方法集合自动满足，没有implements。接口通常由使用者定义，例如未来想替换短信发送商，可以在UserService一侧定义`Send(ctx,phone,code) error`这一项能力。当前代码的Repository没有两个实现，不需要IRepository/RepositoryImpl来回跳。

标准库`error`、`io.Reader`与`http.Handler`都是真实抽象：调用者只关心行为，不需要知道具体类型。测试当前SQL事务用sqlmock是对数据库边界的观察，不要求所有业务对象变成接口。

## 错误返回比异常更显式

阅读`OrderCreate`：条件扣库存影响0行时返回OrderRejection，GORM事务闭包随error回滚。Java原Listener此处只有日志，事务仍提交，说明“同一事务”不意味着所有分支自动正确。Go要求每个分支明确选择返回错误或成功，便于审查。

`errors.Is`沿包装链匹配固定错误；`errors.As`提取OrderRejection的ExistingOrderID。直接`err.Error()=="..."`会被包装文本破坏。Redis BUSYGROUP是客户端未结构化的服务器错误，代码仅在这个协议边界使用前缀判断。

## 为什么不能照搬ThreadLocal

goroutine不是OS线程，请求也不依赖某个固定线程。将当前用户保存在全局变量会使两个并发请求互相覆盖。Auth把`*dto.User`写入当前`gin.Context`，Handler取出ID，再作为普通参数传入Service。后台订单不能拿Gin Context，必须从Stream消息读取userId并验证结构。

`gin.Context`是HTTP框架对象，`context.Context`是取消/超时/请求范围元数据协议，两者不是一个东西。业务层只接受后者和明确userID。

## 指针、slice、defer与生命周期

Service方法使用指针接收者，因为对象包含客户端、singleflight和同步状态，复制会破坏语义。Model传指针给GORM创建，是为了让生成的主键回写。DTO用值或指针都可以，按是否需要表达“不存在”选择。

slice是一个引用底层数组的视图。`enrichBlogs(ctx, blogs, viewer)`修改元素会反映到调用方；追加可能换数组，不能把这与深拷贝混淆。Map用于把IN查询结果恢复到Redis指定的ID顺序，不能依赖SQL IN自然排序。

`defer`在函数返回时执行，适合Close、Unlock、cancel。`go f()`只启动并发，不提供可靠消息、幂等或事务。main负责等待，worker负责消息重试，MySQL负责最终约束；这四层必须分别理解。

## 关于JWT和bcrypt

原项目采用随机Token+Redis Hash，优先保持这种滑动会话。再叠JWT会出现JWT过期与Redis续期互相矛盾，需要不必要的刷新协议，因此没有引入JWT库。bcrypt用于新增密码设置和密码登录；原Java PasswordEncoder的盐+MD5并未被业务使用，没有迁移为默认密码认证。技术栈选择服务于真实业务，而不是依赖数量。
