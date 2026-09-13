# Go 黑马点评：完整接口体验链路

这是一份可以边执行边理解代码的操作手册。默认你使用 Windows PowerShell，并且已经安装 Docker Desktop、Go 和 `curl.exe`。

文档按当前 `internal/handler/router.go` 实际注册的接口编写。当前可调用的业务和运维操作共 **37 个**；最后的覆盖表以代码为准。

## 1. 先确定访问地址

直接访问 Go API：

```powershell
$base = "http://localhost:8081"
```

通过 Nginx 访问：

```powershell
$base = "http://localhost:8080/api"
```

下面的命令默认使用 `$base`。两种地址不要混用：8081 不需要 `/api`，8080 必须保留 `/api`。

## 2. 启动项目

推荐先启动完整 Docker Compose：

```powershell
Set-Location D:\dianping\go-dianping
docker compose up -d --build
docker compose ps
```

检查服务：

```powershell
curl.exe "$base/health/live"
curl.exe "$base/health/ready"
```

期望看到：

```json
{"success":true,"data":{"status":"up"}}
{"success":true,"data":{"status":"ready"}}
```

`ready` 会检查 MySQL 和 Redis，`live` 只检查 API 进程是否存活。

如果你已经单独启动了 MySQL、Redis，也可以本机运行：

```powershell
go mod download
go run ./cmd/migrate -seed
go run ./cmd/server
```

默认配置使用：

- MySQL：`127.0.0.1:3307`。
- Redis：`127.0.0.1:6380`。
- API：`127.0.0.1:8081`。

如果遇到 `docker-credential-desktop` 不存在，说明 Docker 本地凭据助手配置有问题，镜像构建尚未开始。先修复 Docker Desktop 登录/凭据配置，或使用已经能连接的本地 MySQL、Redis 运行 Go 服务。不要把这个错误当成业务接口错误。

常用观察命令：

```powershell
docker compose ps
docker compose logs -f api
docker compose logs --tail 100 mysql redis
```

## 3. 统一规则和 PowerShell 变量

所有响应都包在统一结构中：

```json
{"success":true,"data":...}
```

没有数据时通常是：

```json
{"success":true}
```

业务失败通常是：

```json
{"success":false,"errorMsg":"..."}
```

注意：业务错误通常仍然返回 HTTP 200，要同时检查 HTTP 状态和 `success` 字段。未登录是 401，依赖不可用是 503，系统故障是 500。

受保护接口的 Header：

```powershell
-H "authorization: $token"
```

也支持：

```powershell
-H "authorization: Bearer $token"
```

建议在当前 PowerShell 窗口保存变量：

```powershell
$phone = "13900000009"
$token = "登录后替换为真实Token"
$userId = 0
$shopId = 1
$blogId = 1
$voucherId = 2
$orderId = ""
$imageName = ""
```

每次打开新的 PowerShell 窗口，都需要重新设置这些变量。

## 4. 第一条链路：验证码登录

### 4.1 发送验证码

接口：`POST /user/code`，不要求登录。

```powershell
curl.exe -X POST "$base/user/code?phone=$phone"
```

成功只返回：

```json
{"success":true}
```

开发环境不会把验证码放在响应中。查看 API 日志：

```powershell
docker compose logs --tail 50 api
```

日志中会出现手机号后四位和 6 位验证码。

也可以直接从本地开发 Redis 读取：

```powershell
$code = (docker compose exec -T redis redis-cli GET "login:code:$phone").Trim()
$code
```

验证码有效期约 2 分钟，同一个手机号发送间隔为 60 秒。不要连续执行发送命令，否则会得到“验证码发送频繁”。

### 4.2 登录

验证码登录请求体必须是 JSON：

```powershell
$login = curl.exe -s -X POST "$base/user/login" `
  -H "Content-Type: application/json" `
  --data-raw "{\"phone\":\"$phone\",\"code\":\"$code\"}" | ConvertFrom-Json

$token = $login.data
$token
```

期望：`$login.success` 为 `True`，`$token` 是一串 64 位十六进制字符串。

如果 PowerShell 反引号换行出现问题，使用单行命令：

```powershell
curl.exe -s -X POST "$base/user/login" -H "Content-Type: application/json" --data-raw "{\"phone\":\"$phone\",\"code\":\"$code\"}"
```

验证码成功登录后会被删除，不能再次使用。请求体也可以使用密码分支，但必须先登录后设置密码：

```powershell
curl.exe -s -X PUT "$base/user/password" -H "authorization: $token" -H "Content-Type: application/json" --data-raw '{"password":"learning-go-2026"}'
curl.exe -s -X POST "$base/user/login" -H "Content-Type: application/json" --data-raw '{"phone":"13900000009","password":"learning-go-2026"}'
```

密码长度必须为 8–72 字节，服务使用 bcrypt 校验。

### 4.3 读取当前用户

接口：`GET /user/me`，必须登录。

```powershell
$me = curl.exe -s "$base/user/me" -H "authorization: $token" | ConvertFrom-Json
$userId = [int64]$me.data.id
$me | ConvertTo-Json -Depth 5
```

这个接口可以用来确认 Token 是否有效，并把当前用户 ID 保存到 `$userId`。

### 4.4 主动续期会话

接口：`POST /user/refresh`，必须登录。

```powershell
curl.exe -s -X POST "$base/user/refresh" -H "authorization: $token"
```

正常返回 `ttlSeconds: 1800`。实际上，有效 Token 访问接口时也会自动滑动续期。

### 4.5 查询用户公开信息

接口：`GET /user/{id}`，必须登录。不返回手机号和密码。

```powershell
curl.exe -s "$base/user/$userId" -H "authorization: $token"
```

种子数据还有用户 1、2、3，可以查询：

```powershell
curl.exe -s "$base/user/1" -H "authorization: $token"
```

### 4.6 查询用户资料

接口：`GET /user/info/{id}`，必须登录。

```powershell
curl.exe -s "$base/user/info/$userId" -H "authorization: $token"
```

没有资料时，响应可能只有 `success: true` 而没有 `data`。

### 4.7 设置密码

接口：`PUT /user/password`，必须登录。

```powershell
curl.exe -s -X PUT "$base/user/password" `
  -H "authorization: $token" `
  -H "Content-Type: application/json" `
  --data-raw '{"password":"learning-go-2026"}'
```

这是“当前登录用户”的密码，不需要在请求体中传用户 ID。

## 5. 第二条链路：签到 Bitmap

### 5.1 今日签到

接口：`POST /user/sign`，必须登录。

```powershell
curl.exe -s -X POST "$base/user/sign" -H "authorization: $token"
```

重复调用是幂等的。实现按 `Asia/Shanghai` 的自然日写 Redis Bitmap，不写 `tb_sign` 表。

### 5.2 查询连续签到天数

接口：`GET /user/sign/count`，必须登录。

```powershell
curl.exe -s "$base/user/sign/count" -H "authorization: $token"
```

第一次签到后通常返回 1。它统计的是“从今天向前连续签到的天数”，不是本月累计签到总数。

## 6. 第三条链路：商铺和 GEO

种子数据包含商铺 ID 1–14，以及商铺类型 ID 1–10。

### 6.1 商铺类型列表

接口：`GET /shop-type/list`，不要求登录。

```powershell
curl.exe -s "$base/shop-type/list"
```

记住类型 ID，例如美食通常是 `1`。

### 6.2 商铺详情

接口：`GET /shop/{id}`，不要求登录。

```powershell
$shop = curl.exe -s "$base/shop/1" | ConvertFrom-Json
$shop | ConvertTo-Json -Depth 6
```

第一次查询可能从 MySQL 回源并写入 Redis，第二次查询可以观察缓存命中。代码入口是 `internal/service/shop.go` 的 `GetByID`。

### 6.3 按名称搜索商铺

接口：`GET /shop/of/name`，不要求登录。

```powershell
curl.exe -s "$base/shop/of/name?name=餐厅&current=1"
```

不传 `name` 时按 ID 分页查询：

```powershell
curl.exe -s "$base/shop/of/name?current=1"
```

### 6.4 按类型查询商铺

接口：`GET /shop/of/type`，不要求登录。每页 5 条。

```powershell
curl.exe -s "$base/shop/of/type?typeId=1&current=1"
```

### 6.5 查询附近商铺

仍然是 `GET /shop/of/type`，同时传 `x` 和 `y` 时改走 Redis GEO：

```powershell
curl.exe -s "$base/shop/of/type?typeId=1&current=1&x=120.149192&y=30.316078"
```

- `x` 是经度，范围 -180 到 180。
- `y` 是纬度，范围 -85.05112878 到 85.05112878。
- 两个坐标必须同时传。
- 查询半径为 5000 米，并按距离升序返回。

可以故意只传 `x` 观察参数校验错误：

```powershell
curl.exe -s "$base/shop/of/type?typeId=1&x=120.149192"
```

### 6.6 创建商铺

接口：`POST /shop`，必须登录。以下命令会真的写入本地数据库：

```powershell
$newShop = curl.exe -s -X POST "$base/shop" `
  -H "authorization: $token" `
  -H "Content-Type: application/json" `
  --data-raw '{"name":"Go学习测试商铺","typeId":1,"address":"杭州测试路1号","x":120.150000,"y":30.320000,"avgPrice":5000,"score":45}' | ConvertFrom-Json
$newShopId = [int64]$newShop.data
$newShop | ConvertTo-Json -Depth 5
```

创建时 `name`、`typeId`、`address`、`x`、`y` 必填。当前实现要求登录，但没有商家角色权限。

### 6.7 更新商铺

接口：`PUT /shop`，必须登录。只传要修改的字段：

```powershell
curl.exe -s -X PUT "$base/shop" `
  -H "authorization: $token" `
  -H "Content-Type: application/json" `
  --data-raw "{\"id\":$newShopId,\"name\":\"Go学习测试商铺-已更新\",\"score\":0}"
```

这里的 `score: 0` 是有效更新，不是“没有传 score”。这是 `ShopInput` 指针字段的一个重要例子。更新后再查询：

```powershell
curl.exe -s "$base/shop/$newShopId"
```

## 7. 第四条链路：图片和 Blog

### 7.1 上传 Blog 图片

接口：`POST /upload/blog`，必须登录。字段名必须是 `file`：

```powershell
$upload = curl.exe -s -X POST "$base/upload/blog" `
  -H "authorization: $token" `
  -F "file=@D:\path\to\photo.png" | ConvertFrom-Json
$imageName = $upload.data
$imageName
```

把 `D:\path\to\photo.png` 换成你本机真实图片路径。服务按文件内容识别 PNG、JPEG、GIF、WebP，最大 5 MiB。使用 `-F` 时不要手动设置 `Content-Type`，否则可能破坏 multipart boundary。

返回的 `$imageName` 类似：

```text
/blogs/15/随机64位字符串.png
```

读取图片：

```powershell
curl.exe -I "$base/imgs$($imageName)"
```

上传返回的路径本身是文件相对路径；API 的静态文件路由使用 `/imgs` 前缀。

### 7.2 发布 Blog

接口：`POST /blog`，必须登录。`images` 可以使用上传接口返回的路径：

```powershell
$blog = curl.exe -s -X POST "$base/blog" `
  -H "authorization: $token" `
  -H "Content-Type: application/json" `
  --data-raw "{\"shopId\":1,\"title\":\"第一次用 Go 写探店笔记\",\"images\":\"$imageName\",\"content\":\"这是我的第一条 Go、Gin、GORM 学习笔记。\"}" | ConvertFrom-Json
$blogId = [int64]$blog.data
$blog | ConvertTo-Json -Depth 5
```

如果暂时没有图片，也可以传空字符串：

```powershell
curl.exe -s -X POST "$base/blog" -H "authorization: $token" -H "Content-Type: application/json" --data-raw '{"shopId":1,"title":"无图片学习笔记","images":"","content":"先把请求链路跑通。"}'
```

作者由当前 Token 决定，客户端不能在请求体中伪造 `userId`。发布时 MySQL 会把 Blog 和 Feed outbox 放在同一事务中。

### 7.3 查看热门 Blog

接口：`GET /blog/hot`，不要求登录；游客的 `isLike` 为 false。

```powershell
curl.exe -s "$base/blog/hot?current=1"
```

### 7.4 查看 Blog 详情

接口：`GET /blog/{id}`，必须登录。

```powershell
curl.exe -s "$base/blog/$blogId" -H "authorization: $token"
```

### 7.5 查看自己的 Blog

接口：`GET /blog/of/me`，必须登录。

```powershell
curl.exe -s "$base/blog/of/me?current=1" -H "authorization: $token"
```

### 7.6 查看指定用户的 Blog

接口：`GET /blog/of/user`，必须登录。

```powershell
curl.exe -s "$base/blog/of/user?id=$userId&current=1" -H "authorization: $token"
```

种子用户 1 的文章：

```powershell
curl.exe -s "$base/blog/of/user?id=1&current=1" -H "authorization: $token"
```

### 7.7 切换点赞

接口：`PUT /blog/like/{id}`，必须登录。

```powershell
curl.exe -s -X PUT "$base/blog/like/$blogId" -H "authorization: $token"
```

再次调用会取消点赞。它沿用原路由，但实际是“切换”操作，不是严格幂等的 PUT；如果网络超时，不要盲目重试。

### 7.8 查询最早点赞用户

接口：`GET /blog/likes/{id}`，必须登录，最多返回 5 人。

```powershell
curl.exe -s "$base/blog/likes/$blogId" -H "authorization: $token"
```

## 8. 第五条链路：两个用户的关注与 Feed

为了观察社交关系，建议使用两个 PowerShell 窗口：

- 窗口 A 使用当前 `$token`。
- 窗口 B 使用手机号 `13900000002` 获取另一个 Token。

种子手机号 `13900000001`、`13900000002`、`13900000003` 仅用于本地开发数据。

### 8.1 登录第二个用户

在窗口 B：

```powershell
$base = "http://localhost:8081"
$phone2 = "13900000002"
curl.exe -X POST "$base/user/code?phone=$phone2"
$code2 = (docker compose exec -T redis redis-cli GET "login:code:$phone2").Trim()
$login2 = curl.exe -s -X POST "$base/user/login" -H "Content-Type: application/json" --data-raw "{\"phone\":\"$phone2\",\"code\":\"$code2\"}" | ConvertFrom-Json
$token2 = $login2.data
$token2
```

确认第二个用户：

```powershell
$me2 = curl.exe -s "$base/user/me" -H "authorization: $token2" | ConvertFrom-Json
$userId2 = [int64]$me2.data.id
$me2 | ConvertTo-Json -Depth 5
```

### 8.2 关注和取消关注

窗口 A 用当前用户关注第二个用户：

接口：`PUT /follow/{id}/{isFollow}`，必须登录。

```powershell
curl.exe -s -X PUT "$base/follow/$userId2/true" -H "authorization: $token"
```

重复 `true` 不会重复插入。取消关注：

```powershell
curl.exe -s -X PUT "$base/follow/$userId2/false" -H "authorization: $token"
```

当前体验中先重新关注，方便后续查看 Feed：

```powershell
curl.exe -s -X PUT "$base/follow/$userId2/true" -H "authorization: $token"
```

不能关注自己：

```powershell
curl.exe -s -X PUT "$base/follow/$userId/true" -H "authorization: $token"
```

### 8.3 查询是否关注

接口：`GET /follow/or/not/{id}`，必须登录。

```powershell
curl.exe -s "$base/follow/or/not/$userId2" -H "authorization: $token"
```

### 8.4 查询共同关注

接口：`GET /follow/common/{id}`，必须登录。

```powershell
curl.exe -s "$base/follow/common/$userId2" -H "authorization: $token"
```

要看到有意义的共同关注结果，可以让窗口 A 和窗口 B 都关注用户 3：

```powershell
# 窗口 A
curl.exe -s -X PUT "$base/follow/3/true" -H "authorization: $token"

# 窗口 B
curl.exe -s -X PUT "$base/follow/3/true" -H "authorization: $token2"

# 窗口 A：查询与用户2的共同关注
curl.exe -s "$base/follow/common/$userId2" -H "authorization: $token"
```

### 8.5 第二个用户发布 Blog

窗口 B 发布一条文章：

```powershell
$blog2 = curl.exe -s -X POST "$base/blog" `
  -H "authorization: $token2" `
  -H "Content-Type: application/json" `
  --data-raw '{"shopId":1,"title":"第二个用户的 Feed 测试","images":"","content":"观察关注用户如何收到这条消息。"}' | ConvertFrom-Json
$blogId2 = [int64]$blog2.data
$blogId2
```

Blog 发布后，Feed 投递由后台循环异步执行。立即查询没有结果时，稍后再查，不要重复发布。

### 8.6 查询关注 Feed

接口：`GET /blog/of/follow`，必须登录，每页 2 条。

第一页：

```powershell
$feed = curl.exe -s "$base/blog/of/follow?lastId=$([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())&offset=0" -H "authorization: $token" | ConvertFrom-Json
$feed | ConvertTo-Json -Depth 8
```

响应里的 `data` 包含：

- `list`：当前页 Blog。
- `minTime`：下一页使用的 `lastId`。
- `offset`：同一毫秒分值已经消费的数量。

下一页必须把上一次的 `minTime` 和 `offset` 原样带回：

```powershell
$lastId = $feed.data.minTime
$offset = $feed.data.offset
$feed2 = curl.exe -s "$base/blog/of/follow?lastId=$lastId&offset=$offset" -H "authorization: $token" | ConvertFrom-Json
$feed2 | ConvertTo-Json -Depth 8
```

不要每页都把 `offset` 重置为 0，否则同一毫秒发布的 Blog 可能重复出现。Feed 页面只有 2 条，这是为了方便你观察滚动游标。

## 9. 第六条链路：普通券和秒杀券

种子数据已经提供：

- 普通券 ID `1`。
- 秒杀券 ID `2`，库存 100，活动时间覆盖当前开发时间。

金额单位是“分”，例如 `10000` 表示 100 元。

### 9.1 查询商铺券列表

接口：`GET /voucher/list/{shopId}`，不要求登录。

```powershell
curl.exe -s "$base/voucher/list/1"
```

### 9.2 创建普通券

接口：`POST /voucher`，必须登录。下面会创建一张新的普通券：

```powershell
$normalVoucher = curl.exe -s -X POST "$base/voucher" `
  -H "authorization: $token" `
  -H "Content-Type: application/json" `
  --data-raw '{"shopId":1,"title":"Go学习普通券","subTitle":"本地体验","rules":"仅供开发测试","payValue":8000,"actualValue":10000}' | ConvertFrom-Json
$normalVoucherId = [int64]$normalVoucher.data
$normalVoucher | ConvertTo-Json -Depth 5
```

普通券只写 MySQL，不会创建秒杀库存 Redis Key。

### 9.3 创建秒杀券

接口：`POST /voucher/seckill`，必须登录。秒杀券需要库存和有效时间：

```powershell
$seckillVoucher = curl.exe -s -X POST "$base/voucher/seckill" `
  -H "authorization: $token" `
  -H "Content-Type: application/json" `
  --data-raw '{"shopId":1,"title":"Go学习秒杀券","subTitle":"本地体验","rules":"每人限购一张","payValue":5000,"actualValue":10000,"stock":3,"beginTime":"2020-01-01T00:00:00+08:00","endTime":"2037-01-01T00:00:00+08:00"}' | ConvertFrom-Json
$testVoucherId = [int64]$seckillVoucher.data
$seckillVoucher | ConvertTo-Json -Depth 5
```

如果 Redis 初始化失败，数据库中的券可能已经保存，响应会提示 `initialization_failed`。不要盲目重复创建，先按 `docs/SECKILL.md` 排查。

日常体验优先使用种子秒杀券：

```powershell
$voucherId = 2
curl.exe -s "$base/voucher/list/1"
```

## 10. 第七条链路：异步秒杀订单

### 10.1 发起秒杀

接口：`POST /voucher-order/seckill/{id}`，必须登录。

建议始终带 `idAsString=true`：

```powershell
$orderUrl = "$base/voucher-order/seckill/$voucherId`?idAsString=true"
$seckillResponse = curl.exe -i -s -X POST $orderUrl -H "authorization: $token"
$seckillResponse
```

这个命令会同时显示响应头和响应体。响应头中一定有：

```text
X-Order-ID: 精确订单号字符串
```

响应体成功时类似：

```json
{"success":true,"data":"633000000000000001"}
```

在 PowerShell 中手动把响应体中的字符串保存到变量：

```powershell
$orderId = "把响应体 data 中的订单号复制到这里"
```

也可以不显示响应头，直接解析 JSON：

```powershell
$accepted = curl.exe -s -X POST "$base/voucher-order/seckill/$voucherId`?idAsString=true" -H "authorization: $token" | ConvertFrom-Json
$orderId = [string]$accepted.data
$orderId
```

如果返回“库存不足”或“每人限购一份”，说明 Redis Lua 已在入口拒绝。使用种子券时，同一个用户对券 2 成功一次后，不能再成功第二次。

### 10.2 轮询订单状态

接口：`GET /voucher-order/status/{id}`，必须登录。

```powershell
curl.exe -s "$base/voucher-order/status/$orderId" -H "authorization: $token"
```

正常过程可能先看到：

```json
{"success":true,"data":{"id":"...","state":"pending"}}
```

后台 Worker 完成 MySQL 事务后会变成：

```json
{"success":true,"data":{"id":"...","state":"created"}}
```

可以手动执行几次。不要用旧的默认数值订单 ID 重新转成 JavaScript Number；订单 ID 必须当字符串保存。

### 10.3 查询已落库订单

只有状态变成 `created` 后，再查询：

接口：`GET /voucher-order/{id}`，必须登录。

```powershell
curl.exe -s "$base/voucher-order/$orderId" -H "authorization: $token"
```

返回订单对象，其中包括 `id`、`userId`、`voucherId`、`status` 和 `payType`。

当前项目没有支付、核销、退款接口。数据库中的相关字段不代表这些业务已经实现。

### 10.4 观察 Stream 和数据库

```powershell
docker compose exec redis redis-cli XINFO GROUPS "stream:{seckill}:orders"
docker compose exec redis redis-cli XPENDING "stream:{seckill}:orders" orders
docker compose exec -e MYSQL_PWD=root_dev_only mysql mysql -uroot go_dianping -e "SELECT id,user_id,voucher_id,status FROM tb_voucher_order ORDER BY create_time DESC LIMIT 10"
docker compose exec -e MYSQL_PWD=root_dev_only mysql mysql -uroot go_dianping -e "SELECT voucher_id,stock FROM tb_seckill_voucher"
```

这组命令可以帮助你把 HTTP 请求和 `OrderWorker.process`、`Repository.OrderCreate` 对照起来。

## 11. 删除上传图片

接口：`DELETE /upload/blog/delete`，必须登录。

只能删除上传接口返回的、属于当前用户的新格式路径：

```powershell
$encodedImageName = [uri]::EscapeDataString($imageName)
curl.exe -s -X DELETE "$base/upload/blog/delete?name=$encodedImageName" -H "authorization: $token"
```

不要自己拼接任意文件系统路径。服务会校验路径格式和用户 ID，其他用户的文件会被拒绝。

## 12. 注销并验证认证边界

### 12.1 注销

接口：`POST /user/logout`，必须登录。

```powershell
curl.exe -s -X POST "$base/user/logout" -H "authorization: $token"
```

### 12.2 验证旧 Token 已失效

```powershell
curl.exe -i "$base/user/me" -H "authorization: $token"
```

应返回 HTTP 401。注销不仅清理 Gin 当前请求，还会真正删除 Redis Token Hash。

### 12.3 验证公共接口和受保护接口的区别

公共接口不带 Token 也能访问：

```powershell
curl.exe -s "$base/shop/1"
curl.exe -s "$base/blog/hot?current=1"
curl.exe -s "$base/voucher/list/1"
```

受保护接口不带 Token 会返回 401：

```powershell
curl.exe -i "$base/user/me"
curl.exe -i "$base/blog/1"
curl.exe -i -X POST "$base/user/sign"
```

## 13. 37 个接口覆盖表

下面的表按 `internal/handler/router.go` 的实际注册情况列出。静态文档路由 `/docs`、`/api/openapi.yaml` 和 `/imgs/*filepath` 也由服务提供，但不属于 OpenAPI 业务操作，因此单独列出。

| # | 方法 | 路径 | 登录 |
|---:|---|---|---|
| 1 | GET | `/health/live` | 否 |
| 2 | GET | `/health/ready` | 否 |
| 3 | POST | `/user/code` | 否 |
| 4 | POST | `/user/login` | 否 |
| 5 | POST | `/user/logout` | 是 |
| 6 | POST | `/user/refresh` | 是 |
| 7 | GET | `/user/me` | 是 |
| 8 | GET | `/user/info/{id}` | 是 |
| 9 | GET | `/user/{id}` | 是 |
| 10 | PUT | `/user/password` | 是 |
| 11 | POST | `/user/sign` | 是 |
| 12 | GET | `/user/sign/count` | 是 |
| 13 | GET | `/shop/{id}` | 否 |
| 14 | GET | `/shop/of/type` | 否 |
| 15 | GET | `/shop/of/name` | 否 |
| 16 | GET | `/shop-type/list` | 否 |
| 17 | POST | `/shop` | 是 |
| 18 | PUT | `/shop` | 是 |
| 19 | GET | `/blog/hot` | 否 |
| 20 | GET | `/blog/{id}` | 是 |
| 21 | POST | `/blog` | 是 |
| 22 | PUT | `/blog/like/{id}` | 是 |
| 23 | GET | `/blog/of/me` | 是 |
| 24 | GET | `/blog/of/user` | 是 |
| 25 | GET | `/blog/of/follow` | 是 |
| 26 | GET | `/blog/likes/{id}` | 是 |
| 27 | PUT | `/follow/{id}/{isFollow}` | 是 |
| 28 | GET | `/follow/or/not/{id}` | 是 |
| 29 | GET | `/follow/common/{id}` | 是 |
| 30 | POST | `/voucher` | 是 |
| 31 | POST | `/voucher/seckill` | 是 |
| 32 | GET | `/voucher/list/{shopId}` | 否 |
| 33 | POST | `/voucher-order/seckill/{id}` | 是 |
| 34 | GET | `/voucher-order/{id}` | 是 |
| 35 | GET | `/voucher-order/status/{id}` | 是 |
| 36 | POST | `/upload/blog` | 是 |
| 37 | DELETE | `/upload/blog/delete` | 是 |

上表列出了当前实际注册的 37 个接口：用户 10 个、商铺 6 个、Blog 8 个、关注 3 个、券/订单 6 个、上传 2 个、健康检查 2 个。静态文档路由 `/docs`、`/api/openapi.yaml` 和 `/imgs/*filepath` 也由服务提供，但不属于这 37 个业务和运维操作。

静态路由：

```powershell
curl.exe -I "$base/docs"
curl.exe -I "$base/api/openapi.yaml"
```

## 14. 常见问题

### “请求 JSON 或参数格式错误”

确认：

- 请求体是合法 JSON。
- JSON Header 是 `Content-Type: application/json`。
- PowerShell 字符串中的双引号已正确转义。
- 登录验证码是当前 Redis 中的验证码，而不是旧日志里的验证码。

最简单的写法是单行：

```powershell
curl.exe -X POST "$base/user/login" -H "Content-Type: application/json" --data-raw "{\"phone\":\"$phone\",\"code\":\"$code\"}"
```

### “验证码无效或尝试次数过多”

验证码只有约 2 分钟有效，成功后会立即消费。重新发送前要等待 60 秒发送间隔，然后重新读取 Redis：

```powershell
$code = (docker compose exec -T redis redis-cli GET "login:code:$phone").Trim()
```

### “数据库未初始化”

```powershell
go run ./cmd/migrate -seed
```

### 秒杀返回成功但查询不到订单

这是正常的异步窗口。`POST /voucher-order/seckill/{id}` 只表示 Redis 已受理，先查询 `/voucher-order/status/{id}`，状态为 `created` 后再查订单详情。

### 附近商铺没有结果

确认 `x` 和 `y` 同时传入，并且 Redis 正常。服务启动时会预热 GEO；可查看：

```powershell
curl.exe -s "$base/health/ready"
docker compose logs --tail 100 api
```

### 端口被占用

查看 8081：

```powershell
netstat -ano | findstr :8081
```

可以停止占用端口的、确认属于本项目的本机 API，或者修改配置/Compose 左侧端口。不要结束不认识的系统进程。

### Docker `down` 后数据还在

```powershell
docker compose down
```

只停止容器，通常保留数据卷。只有明确要清空本地项目数据时才使用：

```powershell
docker compose down -v
```

## 15. 一边操作一边读代码

| 体验步骤 | 主要代码 |
|---|---|
| 验证码、登录、注销 | `internal/handler/router.go`、`internal/service/user.go`、`internal/middleware/middleware.go` |
| 用户签到 | `internal/service/user.go`、`internal/redisx/keys.go` |
| 商铺缓存 | `internal/service/shop.go`、`internal/cache/cache.go`、`internal/repository/shop.go` |
| GEO | `internal/service/shop.go`、`internal/redisx/shop_keys.go` |
| Blog、点赞、关注 | `internal/service/social.go`、`internal/repository/social.go` |
| Feed | `internal/service/social.go`、Feed outbox 表和 Redis ZSet |
| 上传 | `internal/handler/upload.go` |
| 普通券 | `internal/service/voucher.go`、`internal/repository/voucher.go` |
| 秒杀入口 | `internal/redisx/seckill.lua`、`internal/service/voucher.go` |
| 订单异步消费 | `internal/worker/orders.go`、`internal/repository/order.go` |
| 统一响应 | `pkg/response/response.go`、`pkg/apperror/error.go` |
| 启动与退出 | `cmd/server/main.go` |

推荐每次只追一条链：

```text
命令
→ router.go
→ 一个 Service 方法
→ 一个 Repository 或 Redis 方法
→ 响应
```

先跑成功路径，再故意传错 ID、去掉 Token、重复点赞、重复关注、重复秒杀。你会比只读代码更快理解每层为什么存在。

更详细的架构说明见 [`BEGINNER_ARCHITECTURE.md`](BEGINNER_ARCHITECTURE.md)，原始接口契约见 [`API.md`](API.md)，运行和排障见 [`RUNNING.md`](RUNNING.md)。
