# 接口文档

本文件与 `api/openapi.yaml` 由 `scripts/generate-api.py` 的已核对接口目录生成。OpenAPI文件使用JSON语法（合法YAML 1.2），可直接导入Swagger/Postman。API根地址 `http://localhost:8081`；Nginx兼容根地址 `http://localhost:8080/api`。
## 统一约定

成功 `{ "success": true, "data": ... }`；无返回值省略data。错误 `{ "success": false, "errorMsg": "..." }`。保留可选total字段，但原分页接口没有返回总数，本实现也不虚构。业务错误HTTP200，认证401，未知接口404，系统故障500，暂时不可用503。所有受保护接口Headers：`authorization: TOKEN`（也支持Bearer），JSON请求加`Content-Type: application/json`。有效Token每次访问自动滑动续期30分钟。公共路由携带无效Token按游客，携带Token遇Redis故障会报错。

金额使用整数分；坐标是浮点度数。JSON时间输出RFC3339带时区，输入秒杀时间兼容Java无时区字符串。浏览器调用秒杀接口应传`idAsString=true`，也可读取`X-Order-ID`响应头；不要先把默认数值data转成Number再转回字符串。成功仅代表异步受理，轮询订单状态确认落库。
## 与 Java 的变化

| 原接口 | Go接口 | 变化及前端动作 |
|---|---|---|
| 商铺/券/上传写接口匿名 | URL和Method不变 | 增加authorization；上传删除限本人新格式路径 |
| /user/logout | 不变 | 真正删除Redis Token，清除前端本地Token |
| /user/login | 不变 | 验证码单次有效、5次尝试、60秒发送间隔；新增bcrypt密码分支 |
| Java LocalDateTime/Date | 不变 | 输出带时区，前端日期展示需正确解析；生日显示取日期 |
| 数字订单ID | 不变 | 新增idAsString=true和X-Order-ID精确字符串；旧前端仍可读默认数字 |
| 无订单查询 | GET /voucher-order/{id}、/status/{id} | 新增受理结果查询 |
| 无主动续期/密码设置 | POST /user/refresh、PUT /user/password | 可选新增，不影响验证码登录 |

除hot外Blog读取、用户资料、关注路由原本就需登录。原BlogCommentsController无接口，本项目没有伪造评论/支付/退款功能。所有已有有效业务URL均保留。
## 1. 发送验证码

`POST /user/code`；登录：不要求。

功能与备注：开发短信写API日志，不在响应中泄露。关闭dev_code_log时明确返回503，需接短信商。

Headers：可选authorization；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| query | phone | string | 是 | 11位中国大陆手机号 |

Body：无。

```bash
curl -X POST 'http://localhost:8081/user/code?phone=13900000001'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true
}
```

错误示例：`{"success": false, "errorMsg": "验证码发送频繁，请稍后重试"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：String login:code:手机号 2m；login:throttle:手机号 60s。

数据库/文件影响：无。
## 2. 验证码/密码登录

`POST /user/login`；登录：不要求。

功能与备注：裸Token返回data；password分支只验证bcrypt，不接受原未启用的MD5工具。

Headers：可选authorization；Content-Type: application/json。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：LoginInput（字段见下方数据结构表及OpenAPI）。

```bash
curl -X POST 'http://localhost:8081/user/login' -H 'Content-Type: application/json' -d '{"phone": "13900000001", "code": "从日志或Redis读取"}'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": "64位随机十六进制Token"
}
```

错误示例：`{"success": false, "errorMsg": "验证码无效或尝试次数过多"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：消费验证码；限制5次尝试；写login:token:Token Hash，TTL30m。

数据库/文件影响：按唯一手机号查询；首次验证码登录创建tb_user。
## 3. 注销当前会话

`POST /user/logout`；登录：必须。

功能与备注：修复Java仅清ThreadLocal却未删除登录态的问题。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：无。

```bash
curl -X POST 'http://localhost:8081/user/logout' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：DEL当前Token Hash。

数据库/文件影响：无。
## 4. 主动续期

`POST /user/refresh`；登录：必须。

功能与备注：新增；平常任意携带有效Token的请求也自动滑动续期。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：无。

```bash
curl -X POST 'http://localhost:8081/user/refresh' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "ttlSeconds": 1800
  }
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：中间件把当前Token TTL刷新至30m。

数据库/文件影响：无。
## 5. 当前用户

`GET /user/me`；登录：必须。

功能与备注：当前用户，保持原接口用途。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/user/me' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "id": 1,
    "nickName": "学习用户一",
    "icon": ""
  }
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：读取并刷新当前Token Hash。

数据库/文件影响：无。
## 6. 查询用户公开信息

`GET /user/{id}`；登录：必须。

功能与备注：不存在返回success:true无data，不返回phone/password。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/user/1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "id": 1,
    "nickName": "学习用户一",
    "icon": ""
  }
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅会话续期。

数据库/文件影响：SELECT tb_user。
## 7. 用户资料

`GET /user/info/{id}`；登录：必须。

功能与备注：无资料时无data；不返回创建/更新时间。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/user/info/1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "userId": 1,
    "city": "杭州",
    "gender": 0,
    "level": 0
  }
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅会话续期。

数据库/文件影响：SELECT tb_user_info。
## 8. 设置当前用户密码

`PUT /user/password`；登录：必须。

功能与备注：新增学习接口；当前有效登录态作为授权。

Headers：authorization: TOKEN；Content-Type: application/json。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：PasswordInput（字段见下方数据结构表及OpenAPI）。

```bash
curl -X PUT 'http://localhost:8081/user/password' -H 'authorization: TOKEN' -H 'Content-Type: application/json' -d '{"password": "learning-go-2026"}'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅会话续期。

数据库/文件影响：UPDATE tb_user.password 为bcrypt哈希。
## 9. 今日签到

`POST /user/sign`；登录：必须。

功能与备注：按Asia/Shanghai自然日；重复签到幂等，不写tb_sign。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：无。

```bash
curl -X POST 'http://localhost:8081/user/sign' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：SETBIT sign:用户ID:yyyyMM，当月第day-1位。

数据库/文件影响：无。
## 10. 连续签到天数

`GET /user/sign/count`；登录：必须。

功能与备注：从今天向前数末尾连续1，今天未签到返回0，不跨月。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/user/sign/count' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": 3
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：BITFIELD sign:用户ID:yyyyMM GET u日数 0。

数据库/文件影响：无。
## 11. 商铺详情

`GET /shop/{id}`；登录：不要求。

功能与备注：冷缓存自动回源；singleflight+锁合并；更新版本防止旧数据回填。

Headers：可选authorization；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/shop/1'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "id": 1,
    "name": "示例商铺",
    "typeId": 1,
    "address": "示例地址",
    "x": 120.149192,
    "y": 30.316078,
    "score": 45
  }
}
```

错误示例：`{"success": false, "errorMsg": "资源不存在"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：Cache Aside cache:shop:ID；空值2m，正常30–33m逻辑TTL+5m过期缓冲。

数据库/文件影响：未命中查询tb_shop。
## 12. 创建商铺

`POST /shop`；登录：必须。

功能与备注：原匿名写接口改为须登录。

Headers：authorization: TOKEN；Content-Type: application/json。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：ShopInput（字段见下方数据结构表及OpenAPI）。

```bash
curl -X POST 'http://localhost:8081/shop' -H 'authorization: TOKEN' -H 'Content-Type: application/json' -d '{"name": "新商铺", "typeId": 1, "address": "示例路1号", "x": 120.15, "y": 30.31}'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": 15
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：维护shop:geo:类型ID；失效对应缓存。

数据库/文件影响：INSERT tb_shop，校验tb_shop_type。
## 13. 更新商铺

`PUT /shop`；登录：必须。

功能与备注：原匿名写接口改为须登录。支持score:0；缓存删除失败会返回503并提示DB已更新，可重试相同更新。

Headers：authorization: TOKEN；Content-Type: application/json。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：ShopInput（字段见下方数据结构表及OpenAPI）。

```bash
curl -X PUT 'http://localhost:8081/shop' -H 'authorization: TOKEN' -H 'Content-Type: application/json' -d '{"id": 1, "name": "新名称", "score": 0}'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：提交后删除缓存并递增版本；维护GEO。

数据库/文件影响：UPDATE tb_shop 白名单字段。
## 14. 类型/附近商铺

`GET /shop/of/type`；登录：不要求。

功能与备注：每页5条；必须同时提供x/y，不提供走普通分类查询。

Headers：可选authorization；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| query | typeId | integer | 是 | 商铺类型正整数 |
| query | current | integer | 否 | 页码；默认1。商铺上限1000，Blog上限100000。 |
| query | x | number | 否 | 经度-180..180 |
| query | y | number | 否 | 纬度-85.05112878..85.05112878 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/shop/of/type?typeId=1&current=1&x=120.149192&y=30.316078'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "name": "示例商铺",
      "typeId": 1,
      "address": "示例地址",
      "x": 120.149192,
      "y": 30.316078,
      "score": 45,
      "distance": 120.5
    }
  ]
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：提供x/y时GEOSEARCH，5000米、ASC距离排序；必要时重建索引。

数据库/文件影响：类型过滤或按GEO返回ID回查tb_shop。
## 15. 名称搜索商铺

`GET /shop/of/name`；登录：不要求。

功能与备注：无name时按ID分页全部商铺。

Headers：可选authorization；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| query | name | string | 否 | 名称关键词 |
| query | current | integer | 否 | 页码；默认1。商铺上限1000，Blog上限100000。 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/shop/of/name?name=%E7%BE%8E%E9%A3%9F&current=1'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "name": "示例商铺",
      "typeId": 1,
      "address": "示例地址",
      "x": 120.149192,
      "y": 30.316078,
      "score": 45
    }
  ]
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：无（有效Token仍续期）。

数据库/文件影响：tb_shop.name LIKE，分页10条。
## 16. 商铺类型列表

`GET /shop-type/list`；登录：不要求。

功能与备注：替代原无TTL且可能重复的List，接口数组不变。

Headers：可选authorization；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/shop-type/list'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "name": "美食",
      "icon": "/types/ms.png",
      "sort": 1
    }
  ]
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：shop_type: 缓存包裹JSON String，30–33m+5m。

数据库/文件影响：未命中tb_shop_type ORDER BY sort,id。
## 17. 热门笔记

`GET /blog/hot`；登录：不要求。

功能与备注：每页10条。游客isLike=false。

Headers：可选authorization；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| query | current | integer | 否 | 页码；默认1。商铺上限1000，Blog上限100000。 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/blog/hot?current=1'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "shopId": 1,
      "userId": 1,
      "title": "探店笔记",
      "images": "/imgs/demo.png",
      "content": "这是一篇学习笔记",
      "liked": 1,
      "name": "学习用户一",
      "icon": "",
      "isLike": true
    }
  ]
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅可选Token续期。

数据库/文件影响：tb_blog按liked desc,id desc；批量查作者/点赞明细。
## 18. 笔记详情

`GET /blog/{id}`；登录：必须。

功能与备注：原实现除hot外所有Blog查询均要求登录。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/blog/1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "id": 1,
    "shopId": 1,
    "userId": 1,
    "title": "探店笔记",
    "images": "/imgs/demo.png",
    "content": "这是一篇学习笔记",
    "liked": 1,
    "name": "学习用户一",
    "icon": "",
    "isLike": true
  }
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅会话续期。

数据库/文件影响：tb_blog + tb_user + tb_blog_like。
## 19. 发布笔记

`POST /blog`；登录：必须。

功能与备注：最多9张图片；作者强制当前用户。Redis失败后由outbox补投。

Headers：authorization: TOKEN；Content-Type: application/json。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：BlogInput（字段见下方数据结构表及OpenAPI）。

```bash
curl -X POST 'http://localhost:8081/blog' -H 'authorization: TOKEN' -H 'Content-Type: application/json' -d '{"shopId": 1, "title": "探店笔记", "images": "/imgs/demo.png", "content": "这是一篇学习笔记"}'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": 5
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：尝试将blogId以毫秒score写入粉丝feed:ID ZSet。

数据库/文件影响：同事务写tb_blog与tb_feed_outbox。
## 20. 切换点赞

`PUT /blog/like/{id}`；登录：必须。

功能与备注：重复请求会切换回取消；该接口不是幂等PUT语义，沿用原路由，网络未知结果不要盲重试。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X PUT 'http://localhost:8081/blog/like/1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：从持久明细更新blog:liked:ID ZSet。

数据库/文件影响：锁定Blog行，事务切换tb_blog_like并更新liked。
## 21. 最早点赞用户

`GET /blog/likes/{id}`；登录：必须。

功能与备注：返回最早5个赞；同毫秒按Redis成员字典序，Redis失败用DB结果。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/blog/likes/1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "nickName": "学习用户一",
      "icon": ""
    }
  ]
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：重建blog:liked:ID；ZRANGE 0 4。

数据库/文件影响：读取持久点赞明细及用户。
## 22. 我的笔记

`GET /blog/of/me`；登录：必须。

功能与备注：每页10条；此接口沿用原未富化的Blog列表。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| query | current | integer | 否 | 页码；默认1。商铺上限1000，Blog上限100000。 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/blog/of/me?current=1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "shopId": 1,
      "userId": 1,
      "title": "探店笔记",
      "images": "/imgs/demo.png",
      "content": "这是一篇学习笔记",
      "liked": 1,
      "name": "学习用户一",
      "icon": "",
      "isLike": true
    }
  ]
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅会话续期。

数据库/文件影响：tb_blog按当前user_id，id desc。
## 23. 指定用户笔记

`GET /blog/of/user`；登录：必须。

功能与备注：每页10条。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| query | id | integer | 是 | 用户ID，正整数 |
| query | current | integer | 否 | 页码；默认1。商铺上限1000，Blog上限100000。 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/blog/of/user?id=1&current=1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "shopId": 1,
      "userId": 1,
      "title": "探店笔记",
      "images": "/imgs/demo.png",
      "content": "这是一篇学习笔记",
      "liked": 1,
      "name": "学习用户一",
      "icon": "",
      "isLike": true
    }
  ]
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅会话续期。

数据库/文件影响：tb_blog按目标user_id，id desc。
## 24. 关注Feed滚动分页

`GET /blog/of/follow`；登录：必须。

功能与备注：每页2条；下一页带minTime作为lastId和offset原值；到底返回success:true无data。默认lastId为当前毫秒。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| query | lastId | integer | 否 | 最大毫秒时间戳，包含该分值 |
| query | offset | integer | 否 | 该分值已消费数量；0..100000 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/blog/of/follow?lastId=1788790000000&offset=0' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "list": [
      {
        "id": 1,
        "shopId": 1,
        "userId": 1,
        "title": "探店笔记",
        "images": "/imgs/demo.png",
        "content": "这是一篇学习笔记",
        "liked": 1,
        "name": "学习用户一",
        "icon": "",
        "isLike": true
      }
    ],
    "minTime": 1788790000000,
    "offset": 1
  }
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：ZREVRANGEBYSCORE feed:当前用户；可从持久outbox恢复。

数据库/文件影响：按ID有序回查Blog并富化作者点赞。
## 25. 关注/取消关注

`PUT /follow/{id}/{isFollow}`；登录：必须。

功能与备注：目标用户须存在且不能关注自己；重复true/false幂等。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |
| path | isFollow | boolean | 是 | true关注/false取消 |

Body：无。

```bash
curl -X PUT 'http://localhost:8081/follow/1/true' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：刷新follows:当前用户 Set。

数据库/文件影响：tb_follow唯一(user_id,follow_user_id)，true插入false删除。
## 26. 是否关注

`GET /follow/or/not/{id}`；登录：必须。

功能与备注：是否关注，保持原接口用途。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/follow/or/not/1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": true
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅会话续期。

数据库/文件影响：查询tb_follow。
## 27. 共同关注

`GET /follow/common/{id}`；登录：必须。

功能与备注：Redis不可用时回退持久关注交集。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/follow/common/1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "nickName": "学习用户一",
      "icon": ""
    }
  ]
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：重建两用户Set并SINTER。

数据库/文件影响：锁定用户行顺序读取tb_follow，回查用户。
## 28. 新增普通优惠券

`POST /voucher`；登录：必须。

功能与备注：原匿名写接口改为须登录。

Headers：authorization: TOKEN；Content-Type: application/json。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：VoucherInput（字段见下方数据结构表及OpenAPI）。

```bash
curl -X POST 'http://localhost:8081/voucher' -H 'authorization: TOKEN' -H 'Content-Type: application/json' -d '{"shopId": 1, "title": "普通券", "payValue": 8000, "actualValue": 10000}'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": 3
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：不写秒杀库存。

数据库/文件影响：INSERT tb_voucher，type=0,status=1。
## 29. 新增秒杀券

`POST /voucher/seckill`；登录：必须。

功能与备注：原匿名写改须登录。DB已提交而Redis初始化失败时需按SECKILL.md修复，勿直接重复新增。

Headers：authorization: TOKEN；Content-Type: application/json。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：VoucherInput（字段见下方数据结构表及OpenAPI）。

```bash
curl -X POST 'http://localhost:8081/voucher/seckill' -H 'authorization: TOKEN' -H 'Content-Type: application/json' -d '{"shopId": 1, "title": "秒杀券", "payValue": 5000, "actualValue": 10000, "stock": 10, "beginTime": "2020-01-01T00:00:00+08:00", "endTime": "2037-01-01T00:00:00+08:00"}'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": 4
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：初始化库存String、时间Hash、已购Hash。

数据库/文件影响：事务写tb_voucher及tb_seckill_voucher。
## 30. 商铺优惠券列表

`GET /voucher/list/{shopId}`；登录：不要求。

功能与备注：保持原查询规则，不额外按时间过滤。

Headers：可选authorization；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | shopId | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/voucher/list/1'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": [
    {
      "id": 2,
      "shopId": 1,
      "title": "学习秒杀券",
      "payValue": 5000,
      "actualValue": 10000,
      "type": 1,
      "status": 1,
      "stock": 100,
      "beginTime": "2020-01-01T00:00:00+08:00",
      "endTime": "2037-01-01T00:00:00+08:00"
    }
  ]
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：无。

数据库/文件影响：tb_voucher LEFT JOIN tb_seckill_voucher；status=1。
## 31. 秒杀下单

`POST /voucher-order/seckill/{id}`；登录：必须。

功能与备注：返回表示已受理，非已持久化。默认数字保持Java兼容；传idAsString=true返回精确字符串。响应头X-Order-ID始终提供精确字符串。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |
| query | idAsString | boolean | 否 | true时data为精确十进制字符串；浏览器新代码建议开启 |

Body：无。

```bash
curl -X POST 'http://localhost:8081/voucher-order/seckill/1?idAsString=true' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": "633000000000000001"
}
```

错误示例：`{"success": false, "errorMsg": "库存不足"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：Lua检查时间、库存、一人一单并原子预扣+XADD+pending状态。

数据库/文件影响：请求通常不写DB；后台事务条件扣库存+插订单。
## 32. 查询自己的已落库订单

`GET /voucher-order/{id}`；登录：必须。

功能与备注：新增；未落库/不属于当前用户都返回资源不存在。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/voucher-order/1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "id": 633000000000000001,
    "userId": 1,
    "voucherId": 2,
    "status": 1,
    "payType": 1
  }
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅会话续期。

数据库/文件影响：SELECT tb_voucher_order WHERE id AND user_id。
## 33. 查询异步订单状态

`GET /voucher-order/status/{id}`；登录：必须。

功能与备注：新增；pending/created/failed；状态id是字符串。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| path | id | integer | 是 | 目标资源ID，正整数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/voucher-order/status/1' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "id": "633000000000000001",
    "state": "pending"
  }
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：DB无订单时读取订单状态Hash。

数据库/文件影响：优先MySQL权威订单。
## 34. 上传笔记图片

`POST /upload/blog`；登录：必须。

功能与备注：原匿名改登录；最多5MiB，按文件内容识别PNG/JPEG/GIF/WebP；返回路径供/imgs前缀使用。

Headers：authorization: TOKEN；multipart/form-data（由客户端生成boundary）。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：file二进制图片，必填。

```bash
curl -X POST 'http://localhost:8081/upload/blog' -H 'authorization: TOKEN' -F 'file=@photo.png'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": "/blogs/1/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"
}
```

错误示例：`{"success": false, "errorMsg": "只支持 PNG/JPEG/GIF/WebP 图片"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅会话续期。

数据库/文件影响：无；写配置目录文件。
## 35. 删除本人图片

`DELETE /upload/blog/delete`；登录：必须。

功能与备注：原匿名删除改为登录且只允许本人新格式文件；旧图片可保留读取，由运维迁移，不提供任意路径删除。

Headers：authorization: TOKEN；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| query | name | string | 是 | 上传接口返回的完整name |

Body：无。

```bash
curl -X DELETE 'http://localhost:8081/upload/blog/delete?name=%2Fblogs%2F1%2Faaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png' -H 'authorization: TOKEN'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：仅会话续期。

数据库/文件影响：无；删除当前用户目录中的图片。
## 36. 存活检查

`GET /health/live`；登录：不要求。

功能与备注：运维接口；依赖不可用ready返回503。

Headers：可选authorization；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/health/live'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "status": "up"
  }
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：ready检查PING，live不检查。

数据库/文件影响：ready检查连接池PING，live不检查。
## 37. 就绪检查

`GET /health/ready`；登录：不要求。

功能与备注：运维接口；依赖不可用ready返回503。

Headers：可选authorization；无Content-Type要求。

| 位置 | 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| — | — | — | — | 无Path/Query参数 |

Body：无。

```bash
curl -X GET 'http://localhost:8081/health/ready'
```

成功示例（data字段结构参见数据结构表）：

```json
{
  "success": true,
  "data": {
    "status": "ready"
  }
}
```

错误示例：`{"success": false, "errorMsg": "请求参数无效"}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。

Redis影响：ready检查PING，live不检查。

数据库/文件影响：ready检查连接池PING，live不检查。
## 数据结构与响应字段

每个接口的顶层字段遵循统一Result；以下表格说明data的对象字段。数组接口返回这些对象的数组。请求Body只允许接口注明字段，服务端ID/作者/计数/创建时间不能由客户端覆盖。
### User



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| phone | string | 手机号，不在用户公开DTO返回 |
| nickName | string | 昵称 |
| icon | string | 头像/图标路径 |
| createTime | string | 创建时间，RFC3339 带时区 |
| updateTime | string | 更新时间，RFC3339 带时区 |
### UserInfo



| 字段 | 类型 | 说明 |
|---|---|---|
| userId | integer | 用户 ID |
| city | string | 城市 |
| introduce | string | 简介 |
| fans | integer | 粉丝数量（原用户资料字段，不随关注实时统计） |
| followee | integer | 关注数（原资料字段） |
| gender | integer | 0男/1女（沿用原数据） |
| birthday | string | 生日；当前输出RFC3339日期时间 |
| credits | integer | 积分 |
| level | integer | 会员等级0–9 |
### Shop



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| name | string | 名称；Blog 中为作者昵称 |
| typeId | integer | 商铺类型 ID |
| images | string | 图片路径，多个以逗号分隔 |
| area | string | 商圈 |
| address | string | 地址 |
| x | number | 经度 |
| y | number | 纬度 |
| avgPrice | integer | 整数均价（沿用原单位） |
| sold | integer | 销量 |
| comments | integer | 评论数 |
| score | integer | 评分乘 10；0–50 |
| openHours | string | 营业时间 |
| createTime | string | 创建时间，RFC3339 带时区 |
| updateTime | string | 更新时间，RFC3339 带时区 |
| distance | string | 距查询点的距离，单位米 |
### ShopType



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| name | string | 名称；Blog 中为作者昵称 |
| icon | string | 头像/图标路径 |
| sort | integer | sort |
### Blog



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| shopId | integer | 商铺 ID |
| userId | integer | 用户 ID |
| title | string | 标题 |
| images | string | 图片路径，多个以逗号分隔 |
| content | string | 正文 |
| liked | integer | 点赞数 |
| comments | integer | 评论数 |
| createTime | string | 创建时间，RFC3339 带时区 |
| updateTime | string | 更新时间，RFC3339 带时区 |
| icon | string | 头像/图标路径 |
| name | string | 名称；Blog 中为作者昵称 |
| isLike | boolean | 当前用户是否点赞 |
### BlogComment



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| userId | integer | 用户 ID |
| blogId | integer | blogId |
| parentId | integer | 一级评论ID；顶层为0 |
| answerId | integer | 回复评论ID |
| content | string | 正文 |
| liked | integer | 点赞数 |
| status | integer | 状态：优惠券 1上架/2下架/3过期；订单1未支付/2已支付/3已核销/4取消/5退款中/6已退款 |
| createTime | string | 创建时间，RFC3339 带时区 |
| updateTime | string | 更新时间，RFC3339 带时区 |
### BlogLike



| 字段 | 类型 | 说明 |
|---|---|---|
| blogId | integer | blogId |
| userId | integer | 用户 ID |
| createTime | string | 创建时间，RFC3339 带时区 |
### Follow



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| userId | integer | 用户 ID |
| followUserId | integer | 被关注用户ID |
| createTime | string | 创建时间，RFC3339 带时区 |
### FeedOutbox



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| userId | integer | 用户 ID |
| blogId | integer | blogId |
| score | integer | 评分乘 10；0–50 |
| deliveredAt | string | deliveredAt |
| createTime | string | 创建时间，RFC3339 带时区 |
### Voucher



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| shopId | integer | 商铺 ID |
| title | string | 标题 |
| subTitle | string | 副标题 |
| rules | string | 使用规则 |
| payValue | integer | 支付金额，单位分 |
| actualValue | integer | 抵扣金额，单位分 |
| type | integer | 0普通券/1秒杀券 |
| status | integer | 状态：优惠券 1上架/2下架/3过期；订单1未支付/2已支付/3已核销/4取消/5退款中/6已退款 |
| createTime | string | 创建时间，RFC3339 带时区 |
| updateTime | string | 更新时间，RFC3339 带时区 |
| stock | integer | 剩余库存 |
| beginTime | string | 开始时间 |
| endTime | string | 结束时间 |
### SeckillVoucher



| 字段 | 类型 | 说明 |
|---|---|---|
| voucherId | integer | 优惠券 ID |
| stock | integer | 剩余库存 |
| createTime | string | 创建时间，RFC3339 带时区 |
| beginTime | string | 开始时间 |
| endTime | string | 结束时间 |
| updateTime | string | 更新时间，RFC3339 带时区 |
### VoucherOrder



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| userId | integer | 用户 ID |
| voucherId | integer | 优惠券 ID |
| payType | integer | 1余额/2支付宝/3微信，仅存储字段，未实现支付接口 |
| status | integer | 状态：优惠券 1上架/2下架/3过期；订单1未支付/2已支付/3已核销/4取消/5退款中/6已退款 |
| createTime | string | 创建时间，RFC3339 带时区 |
| payTime | string | 支付时间 |
| useTime | string | 核销时间 |
| refundTime | string | 退款时间 |
| updateTime | string | 更新时间，RFC3339 带时区 |
### Sign



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| userId | integer | 用户 ID |
| year | integer | year |
| month | integer | month |
| date | string | 日期 |
| isBackup | boolean | 是否补签 |
### UserDTO



| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| nickName | string | 昵称 |
| icon | string | 头像/图标路径 |
### LoginInput



| 字段 | 类型 | 说明 |
|---|---|---|
| phone | string | 手机号，不在用户公开DTO返回 |
| code | string | 验证码优先于password；仅可成功使用一次 |
| password | string | 新增bcrypt密码登录；先用验证码登录设置密码 |
### PasswordInput



| 字段 | 类型 | 说明 |
|---|---|---|
| password | string | 8–72字节 |
### ShopInput

POST必填name,typeId,address,x,y；PUT必填id和至少一个更新字段，0值可更新，省略字段保持。

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。 |
| name | string | 名称；Blog 中为作者昵称 |
| typeId | integer | 商铺类型 ID |
| images | string | 图片路径，多个以逗号分隔 |
| area | string | 商圈 |
| address | string | 地址 |
| x | number | 经度 |
| y | number | 纬度 |
| avgPrice | integer | 整数均价（沿用原单位） |
| sold | integer | 销量 |
| comments | integer | 评论数 |
| score | integer | 评分乘 10；0–50 |
| openHours | string | 营业时间 |
### BlogInput



| 字段 | 类型 | 说明 |
|---|---|---|
| shopId | integer | 商铺 ID |
| title | string | 标题 |
| images | string | 图片路径，多个以逗号分隔 |
| content | string | 正文 |
### VoucherInput

金额>0，秒杀额外必填stock>0、beginTime、endTime。接受带时区RFC3339或无时区2006-01-02T15:04:05（按配置时区）。

| 字段 | 类型 | 说明 |
|---|---|---|
| shopId | integer | 商铺 ID |
| title | string | 标题 |
| subTitle | string | 副标题 |
| rules | string | 使用规则 |
| payValue | integer | 支付金额，单位分 |
| actualValue | integer | 抵扣金额，单位分 |
| stock | integer | 剩余库存 |
| beginTime | string | 开始时间 |
| endTime | string | 结束时间 |
### ScrollResult



| 字段 | 类型 | 说明 |
|---|---|---|
| list | array | list |
| minTime | integer | 下一页lastId，最小毫秒时间戳 |
| offset | integer | 此minTime已消费条数，原样带到下一页 |
### OrderStatus



| 字段 | 类型 | 说明 |
|---|---|---|
| id | string | 精确订单ID字符串 |
| state | string | state |
| error | string | error |
| order | VoucherOrder | order |
### Error



| 字段 | 类型 | 说明 |
|---|---|---|
| success | boolean | success |
| errorMsg | string | errorMsg |

## 文档与静态资源

GET `/docs` 提供无外部CDN依赖的接口目录；GET `/api/openapi.yaml` 下载规范；GET/HEAD `/imgs/{path}` 读取上传目录静态图片。原前端HTML不在仓库中，Nginx只提供反向代理。
