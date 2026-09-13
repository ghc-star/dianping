# Blog、点赞、关注与 Feed：从调用链到自己实现

本模块保留原 Java 的 URL、camelCase 字段和推模式 Feed。它补上了点赞持久明细、关注唯一约束及 Feed 事务发件箱，使并发请求和 Redis 重启不再直接破坏业务事实。

建议先打开 `internal/service/social.go`，再按函数进入 `internal/repository/social.go`。`internal/model/models.go` 显式映射表名；`internal/dto/social.go` 只承载 Feed 响应。

## 1. 先看懂原 Java 的真实实现

| 原调用链 | 原来实际发生什么 | Go 对应与修复 |
| --- | --- | --- |
| `BlogController.saveBlog → BlogServiceImpl.saveBlog` | 存 Blog，然后逐个查粉丝、写 Redis ZSet | `CreateBlog → CreateSocialBlog` 同事务提交 Blog 与投递记录；后台重试 |
| `updateLike → ZSCORE → liked±1 → ZADD/ZREM` | 查询点赞状态与更新计数分开，两个并发请求可能同时加一 | `ToggleSocialLike` 锁 Blog 行，明细与计数在同一 MySQL 事务中修改 |
| `queryBlogLikes → ZRANGE 0 4 → ORDER BY FIELD` | 返回最早点赞的 5 人，不是最近点赞，也不是点赞最多的用户 | `BlogLikes → likeRanking` 保留升序排名，并按 Redis 顺序拼装 DTO |
| `FollowServiceImpl.follow` | 每次关注都 INSERT，没有关系唯一约束 | `(user_id,follow_user_id)` 唯一索引；重复关注成功但只保留一行 |
| `followCommons → SINTER` | Redis 关注集合丢失后，共同关注一直为空 | 从持久关注恢复 Set 后执行 SINTER；Redis 不可用时从同一 DB 快照算交集 |
| `quertBlogOfFollow` | `ZREVRANGEBYSCORE max 0 LIMIT offset 2` | `Feed` 保留每页 2 条，修复同一时间戳连续跨多页的 offset 累加 |

Java 的热门列表已经批量查询作者；Go 将详情、热门及 Feed 的作者/点赞状态都按批次查询。原来的“我的笔记”“用户笔记”没有补作者字段，Go 保留这一行为。

原 `BlogCommentsController` 是空类，没有可迁移的评论接口。`tb_blog_comments` 的 Model 与数据结构被保留，文档不会将它表述为已存在的评论 CRUD。

## 2. 接口契约

完整 HTTP 状态和错误约定见 [API.md](API.md) 与 [OpenAPI](../api/openapi.yaml)。本节补充社交行为，所有参数名称与原前端一致。

除 `GET /blog/hot` 外，下表接口都需要登录。请求 Header 使用 `authorization: TOKEN`；无需 Body 的接口不要额外发送 JSON。

| 名称 / 功能 | Method / URL | Path / Query / Body | 成功 `data` | DB / Redis 影响 |
| --- | --- | --- | --- | --- |
| 发布探店笔记 | `POST /blog` | JSON：`shopId,title,images,content` | 新 Blog 的整数 ID | 事务写 `tb_blog`、每个粉丝的 `tb_feed_outbox`；投递 `feed:{粉丝ID}` |
| 切换点赞 | `PUT /blog/like/{id}` | Path：笔记 ID | 无 data | 事务改 `tb_blog_like` 和 `tb_blog.liked`；重建 `blog:liked:{id}` |
| 我的笔记 | `GET /blog/of/me` | Query：`current=1` | Blog 数组，最多 10 条 | 查当前用户的 `tb_blog`，按 ID 降序 |
| 热门笔记 | `GET /blog/hot` | Query：`current=1`；公开，带 Token 可返回个人点赞状态 | Blog 数组，最多 10 条 | 查 Blog、作者、当前用户点赞明细；`liked DESC,id DESC` |
| 笔记详情 | `GET /blog/{id}` | Path：笔记 ID | Blog 对象 | 查 Blog、作者、个人点赞明细 |
| 最早的点赞者 | `GET /blog/likes/{id}` | Path：笔记 ID | User DTO 数组，最多 5 人 | 读明细、校正 ZSet、执行 ZRANGE，再批量查用户 |
| 某用户的笔记 | `GET /blog/of/user` | Query：`id` 必填，`current=1` | Blog 数组，最多 10 条 | 查询该用户的 Blog |
| 关注 Feed | `GET /blog/of/follow` | Query：`lastId` 必填，`offset=0` | `{list,minTime,offset}`；读尽时无 data | 收件箱按分数逆序读取；缺少恢复标记时从 outbox 重建 |
| 关注 / 取关 | `PUT /follow/{id}/{isFollow}` | Path：目标用户 ID；`isFollow=true/false` | 无 data | INSERT 幂等 / DELETE 关系；同步 `follows:{当前用户ID}` |
| 是否关注 | `GET /follow/or/not/{id}` | Path：目标用户 ID | 布尔值 | 查询持久关注关系 |
| 共同关注 | `GET /follow/common/{id}` | Path：另一个用户 ID | User DTO 数组 | 同一事务读取双方关系；Set 刷新和 SINTER；按用户 ID 升序返回 |

Blog 的业务字段为 `id,shopId,userId,title,images,content,liked,comments,createTime,updateTime`。富化字段 `name,icon,isLike` 不写入 `tb_blog`。User DTO 只有 `id,nickName,icon`，不会暴露手机号和密码。

`current` 必须在 1 至 100000 之间；ID 必须为正整数。标题按字符校验 1 至 255，内容 1 至 2048。图片用逗号分隔，最多 9 个非空地址，合计不超过 2048 字符。作者由登录上下文决定；客户端提交 `userId,id,liked,comments` 不会覆盖服务端数据。

### 请求与响应示例

先让账号 B 关注账号 A，后续 A 发布的笔记才会进入 B 的收件箱。关注动作不会补发关注前的笔记；取关也不会删除已经收取的笔记，与原 Java 推送语义一致。

```bash
curl -X PUT http://localhost:8081/follow/1/true \
  -H 'authorization: TOKEN_B'

curl -X POST http://localhost:8081/blog \
  -H 'authorization: TOKEN_A' \
  -H 'Content-Type: application/json' \
  -d '{"shopId":1,"title":"第一次探店","images":"/imgs/blogs/a.jpg","content":"味道不错，下次还来。"}'
```

```json
{"success":true,"data":23}
```

```bash
curl -X PUT http://localhost:8081/blog/like/23 -H 'authorization: TOKEN_B'
curl http://localhost:8081/blog/likes/23 -H 'authorization: TOKEN_B'
curl http://localhost:8081/follow/or/not/1 -H 'authorization: TOKEN_B'
curl http://localhost:8081/follow/common/1 -H 'authorization: TOKEN_B'
curl 'http://localhost:8081/blog/hot?current=1'
curl 'http://localhost:8081/blog/of/me?current=1' -H 'authorization: TOKEN_A'
curl 'http://localhost:8081/blog/of/user?id=1&current=1' -H 'authorization: TOKEN_B'
curl http://localhost:8081/blog/23 -H 'authorization: TOKEN_B'
```

点赞切换成功：`{"success":true}`。再次发送同一个切换请求会取消赞，所以它不具备“重试不改变结果”的幂等性；前端应在请求完成前禁用点赞按钮。并发的每次请求仍会被数据库正确串行处理。

```json
{"success":true,"data":[{"id":2,"nickName":"学习者B","icon":""}]}
```

关注结果：`{"success":true,"data":true}`。列表为空时返回 `{"success":true,"data":[]}`。无 Token 返回 HTTP 401；参数无效返回 `success:false` 与中文 `errorMsg`；不存在的 Blog / 商铺 / 关注目标返回“资源不存在”；未预期数据库错误返回 HTTP 500，详细原因只写服务日志。

```json
{"success":false,"errorMsg":"笔记标题须为 1 至 255 个字符"}
```

Feed 第一次请求的 `lastId` 使用当前 Unix 毫秒时间。后续请求必须照抄上一页的 `minTime` 和 `offset`，不要自己减一毫秒，也不要改成 `current` 页码。

```bash
curl 'http://localhost:8081/blog/of/follow?lastId=1800000000000&offset=0' \
  -H 'authorization: TOKEN_B'
```

```json
{
  "success":true,
  "data":{
    "list":[{
      "id":23,"shopId":1,"userId":1,"title":"第一次探店",
      "images":"/imgs/blogs/a.jpg","content":"味道不错，下次还来。",
      "liked":1,"comments":0,"name":"学习者A","icon":"","isLike":true,
      "createTime":"2026-09-07T12:00:00+08:00","updateTime":"2026-09-07T12:00:00+08:00"
    }],
    "minTime":1788753600000,
    "offset":1
  }
}
```

上述 ID、时间与 Token 是示例；实际创建返回的 ID 应用于后续请求。Feed 耗尽保持 Java 的 `{"success":true}`。

## 3. 点赞：MySQL 事务保证什么

```text
PUT /blog/like/{id}
    → SocialService.LikeBlog
    → Repository.ToggleSocialLike
        → BEGIN
        → SELECT tb_blog ... FOR UPDATE
        → 查询 (blog_id,user_id) 点赞明细
        → INSERT / DELETE tb_blog_like
        → UPDATE tb_blog.liked ± 1
        → COMMIT
    → likeRanking
        → 再次锁 Blog 行，读取已提交明细
        → Lua 原子替换 ZSet，并返回 ZRANGE 0 4
```

`tb_blog_like` 的复合主键防止同一用户重复拥有两条点赞。Blog 行锁解决多个 API 实例对同一笔记同时操作的问题。只锁进程内 `sync.Mutex` 不能保护另一个 API 实例。

排名同步在提交后进行，因此 Redis 故障不撤销已经成功的点赞。再次同步时重新取得 Blog 行锁，防止较旧快照覆盖新状态。Redis 回调最多等待 2 秒，避免无限占用数据库行锁。排行榜刷新失败时直接使用本次持久快照中相同排序的 5 人。

Lua 原子替换避免其他读取看到 `DEL` 后、`ZADD` 前的空集合。这里的原子性只覆盖 Redis 命令，不能让 Redis 与 MySQL 组成一个跨系统事务。

热门和详情中的 `isLike` 直接批量读取持久明细。ZSet 是排名投影，Redis 被清空后，下一次点赞者列表请求会恢复它。

原 SQL 只有历史 `liked` 计数，没有“哪一个人点过赞”的明细。迁移保留旧计数，不会伪造旧点赞者；从 Go 开始产生的新点赞进入 `tb_blog_like`。如果需要恢复旧点赞身份，应在切换前导出 Java 的 `blog:liked:*`，在受控迁移中导入，且不能重复增加原计数。

为了让学习路径清楚，本版在点赞/排名读取时同步该笔记完整点赞集合。极大量点赞时，事务与 Redis 回调会增加延迟；后续可将排名改为带版本的增量投影和批量修复任务，但必须继续保留明细主键、事务及重放语义。

## 4. 关注：Set 与数据库的分工

`tb_follow` 是事实来源；`follows:{userId}` 是可重建的 Set。`PUT .../true` 重复执行不会新增重复关系，`PUT .../false` 删除不存在关系仍成功。

关注修改先锁当前用户行，再修改关系。共同关注查询按用户 ID 从小到大锁两个用户，读取两组关注并刷新 Set，最后调用 SINTER。这一锁顺序避免 A 对 B 和 B 对 A 的共同关注查询互相等待成环。

空 Set 在 Redis 中不会保留实体 key，读取时从数据库恢复仍然正确。Redis 不可用时从已读取的两组 ID 计算交集；这是同一个业务结果的降级，不会把 Redis 故障误判成“没有共同关注”。

## 5. Feed：发布后宕机也能继续投递

```text
CreateSocialBlog 事务：Blog + 所有当前粉丝的 outbox 行
                       ↓ COMMIT
          即时尝试 / SocialService.Run
                       ↓
        查询 delivered_at IS NULL 的 100 行
                       ↓
        Redis pipeline ZADD feed:{fanID} score blogID
                       ↓ Redis 成功
                标记 delivered_at
```

Outbox 指“与业务数据放在同一个数据库事务中的待发送记录”。它解决“Blog 保存成功，但 Redis 写入失败”导致永久漏推送的问题。

| 故障时机 | 数据状态 | 恢复行为 |
| --- | --- | --- |
| Blog 事务提交前崩溃 | Blog 和 outbox 都回滚 | 客户端可以重新发布 |
| 事务提交后、ZADD 前崩溃 | Blog 和待投递行都存在 | 重启 worker 后继续发送 |
| Pipeline 只执行了一部分 | 未确认整批交付 | 整批重试；相同 member 的 ZADD 不产生重复 |
| ZADD 成功、标记前崩溃 | 收件箱已有，outbox 仍待投递 | 重复 ZADD，最后标记 |
| Redis 收件箱全部丢失 | 已投递 outbox 历史仍在 MySQL | 第一次 Feed 读取批量重建原收件箱 |

这里不使用 Redis Stream；Stream 保留给秒杀订单。Feed 使用 MySQL outbox 是为了把“发布笔记”和“必须投递给哪些粉丝”放进同一个 MySQL 事务。

`Run(ctx)` 自身阻塞运行，不偷偷启动不受控 goroutine。应用启动它并负责 `WaitGroup`；循环每秒重试，持续有整批数据时马上处理下一批；停止时由 context 退出。每轮捕获非业务 panic，记日志后继续重试，未投递记录不会被标记成功。

已交付的 outbox 行保留为收件箱历史，因此取关后不会意外移除历史笔记，Redis 恢复也不会把关注前的笔记添加进来。生产环境应按明确的 Feed 保留策略归档历史，不能随手清空 outbox 后还期待完整恢复。

`feed:ready:{userId}` 的 24 小时标记表示一次历史恢复已经完成。每次还会确认实际 inbox 存在，避免仅收件箱被淘汰、标记仍在时漏恢复。空收件箱执行一次索引历史查询。首次读取采用 `singleflight.Group.Do` 合并同进程的恢复工作，最多 10 秒，沿用调用者 context；没有额外的无期限后台 goroutine。API 多实例同时恢复会重复 ZADD，结果仍幂等。

## 6. 为什么 Feed offset 要累加

假设 7 条笔记的 score 都是 `1000`，Redis 每页取 2 条：

| 请求 | 返回数量 | 下一游标 |
| --- | ---: | --- |
| `max=1000,offset=0` | 2 | `(1000,2)` |
| `max=1000,offset=2` | 2 | `(1000,4)` |
| `max=1000,offset=4` | 2 | `(1000,6)` |
| `max=1000,offset=6` | 1 | `(1000,7)` |

下一页的最小时间仍等于本次请求的 `max` 时，必须将“本页该时间戳的数量”加上“已经消费的 offset”。如果新一页最小时间变成 `999`，offset 只保留本页 `999` 的数量。

`nextSocialCursor` 的单元测试专门覆盖三页都处于同一毫秒的情况。Java 原实现每页把 offset 重新算成 2，第三页会重新读取第二页。

相同 score 时 Redis 按 member 字符串排序；例如字符串 `"10"` 会排在 `"2"` 前。Go 先读取 Redis ID 顺序，再用 map 批量查找 Blog，不能相信 SQL 的 `IN (...)` 会保留参数顺序。

## 7. Redis Key 清单

所有构造函数位于 `internal/redisx/social_keys.go`，前缀常量集中在 `internal/redisx`。

| Key | 类型 | TTL | 内容 / 作用 | 可恢复来源 |
| --- | --- | --- | --- | --- |
| `blog:liked:{blogId}` | ZSet | 无 | member=userId，score=点赞 Unix 毫秒 | `tb_blog_like` |
| `follows:{userId}` | Set | 无 | 已关注的 userId | `tb_follow` |
| `feed:{userId}` | ZSet | 无 | member=blogId，score=发布 Unix 毫秒 | `tb_feed_outbox` 历史 |
| `feed:ready:{userId}` | String | 24 小时 | 已完成收件箱历史恢复 | 可直接重建 |

同一个 Redis 实例内，这些投影能原子执行单 key Lua。当前项目使用单实例 Redis；没有宣称多 key SINTER 可以不经改造运行在 Redis Cluster 的不同槽位。

## 8. 不看答案，自己怎么写

1. **先写 Blog CRUD 查询骨架，难度 ★★。** 建 Model 的显式 TableName，完成发布与详情；验证客户端 `userId` 不会伪装成别人。给作者信息补一次批量查询，验证列表不出现 N+1 查询。
2. **再写持久点赞，难度 ★★★。** 建复合主键，写事务行锁；不要先写 Redis。并发调用偶数次切换，验证最终新增计数为 0、明细为 0；模拟 INSERT 失败，验证计数不会独自增加。
3. **加 ZSet 排名，难度 ★★★。** 使用毫秒 score，保存 userId，读取前 5 名；写一个原子替换脚本，验证相同毫秒下的字符串排序。清掉缓存后再查，结果应由持久明细恢复。
4. **实现关注，难度 ★★。** 从唯一索引、重复请求幂等和禁止自关注开始；先写 `IsFollow`，再写 Set 同步与共同关注。用两个用户都关注第三个人来验证 SINTER。
5. **实现推模式 Feed，难度 ★★★。** 发布时查询粉丝，给每个收件箱 ZADD。同一时间戳人工塞 7 条数据，以每页 2 条读取，检查每个 ID 恰好出现一次。
6. **补事务 outbox，难度 ★★★★。** 将笔记 INSERT 和粉丝投递记录 INSERT 放入一个事务，先停 Redis 再发布；恢复 Redis 和 worker 后，验证粉丝能看到新笔记。
7. **练习生命周期和恢复，难度 ★★★★。** 给 worker 加 context、ticker、panic 隔离及应用 WaitGroup；在 ZADD 后故意中断一次，再启动 worker，验证收件箱成员不重复。

## 9. 验证与排错

无需 MySQL/Redis 的单测：

```bash
go test ./internal/service -run 'TestFeedCursor|TestBlogValidation|TestSocial' -v
```

真实集成测试覆盖 12 次并发点赞、关注唯一性、Redis 故障后的 outbox 补投、单独丢失 inbox 的恢复及重复投递幂等。先向专用测试库运行迁移，再设置环境变量。测试固定使用 Redis DB 12，只清理自己创建的记录和 key：

```powershell
$env:DIANPING_TEST_MYSQL_DSN='dianping:dianping_dev@tcp(127.0.0.1:3307)/go_dianping_test?charset=utf8mb4&parseTime=true&loc=Asia%2FShanghai'
$env:DIANPING_TEST_REDIS_ADDR='127.0.0.1:6380'
go test ./internal/service -run TestSocialIntegration -count=1 -v
```

主机端口以实际 Compose 配置为准。若 Redis 配了密码，另设 `DIANPING_TEST_REDIS_PASSWORD`。没有这两个必需环境变量时集成测试会明确 SKIP，普通单元测试仍执行。

如果 Windows 当前工具链支持 C 编译器，或在 Linux 开发环境中，可增加：

```bash
go test -race ./internal/service
```

排查 Feed 漏消息时，先检查 `tb_feed_outbox` 有没有对应的 `(user_id,blog_id)`，再检查 `delivered_at`，最后检查 Redis ZSet。没有 outbox 行表示发布时此用户不是粉丝；有待投递行表示 worker/Redis 需要恢复；已投递但缓存缺失，可以删除该用户 `feed:ready:{id}`，下一次读取会恢复历史。

不要在真实数据环境执行 `FLUSHDB` 做测试。使用独立测试数据库、独立 Redis DB，并只清理测试创建的 ID。
