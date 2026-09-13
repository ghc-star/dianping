# 数据库结构与迁移

Go继续使用原Java项目的`tb_*`表和snake_case列。GORM Model显式声明TableName和column，服务启动不调用AutoMigrate。金额始终是整数：`pay_value`和`actual_value`单位为分，`avg_price`沿用原库的整数均价；禁止用float64保存金额。

## 表总览

| 表 | 主键 | 主要字段 | 关系、索引与业务约束 |
|---|---|---|---|
| tb_user | id自增 | phone,password,nick_name,icon,时间 | phone唯一；password保存bcrypt。旧Java盐+MD5工具未在登录链路使用 |
| tb_user_info | user_id非自增 | city,introduce,fans,followee,gender,birthday,credits,level | 与用户一对一；gender/level为整数，修正Java误映射Boolean |
| tb_shop_type | id自增 | name,icon,sort,时间 | 类型按sort,id排序 |
| tb_shop | id自增 | name,type_id,images,area,address,x,y,avg_price,sold,comments,score,open_hours | type_id索引；score按1–5分乘10保存；未声明物理外键以兼容原库 |
| tb_blog | id自增 | shop_id,user_id,title,images,content,liked,comments,时间 | 新增(user_id,id)、(liked,id)索引；liked是汇总计数 |
| tb_blog_comments | id自增 | user_id,blog_id,parent_id,answer_id,content,liked,status,时间 | 保留原表；原Controller为空，因此没有评论HTTP接口 |
| tb_blog_like | (blog_id,user_id)复合主键 | create_time | Go新增持久点赞身份；(blog_id,create_time)支持最早点赞排行 |
| tb_follow | id自增 | user_id,follow_user_id,create_time | 新增唯一(user_id,follow_user_id)保证幂等；反向粉丝索引 |
| tb_feed_outbox | id自增 | user_id,blog_id,score,delivered_at,create_time | Go新增；每个粉丝/Blog唯一，支持未投递扫描及收件箱恢复 |
| tb_voucher | id自增 | shop_id,title,sub_title,rules,pay_value,actual_value,type,status,时间 | (shop_id,status)索引；type 0普通/1秒杀，status 1上架/2下架/3过期 |
| tb_seckill_voucher | voucher_id | stock,begin_time,end_time,时间 | 与券一对一；stock是MySQL最终持久库存，条件更新不能为负 |
| tb_voucher_order | id（服务生成） | user_id,voucher_id,pay_type,status及支付/核销/退款时间 | 新增唯一(user_id,voucher_id)兜底一人一单；voucher_id索引 |
| tb_sign | id自增 | user_id,year,month,date,is_backup | 保留原SQL；实际签到与原Java相同，写Redis Bitmap，不写此表 |

没有声明数据库外键是原项目的设计选择，便于导入演示数据。Service仍检查商铺类型、目标用户和券是否存在。若改为正式系统，可在清理孤儿行后逐步增加外键；不要在已有脏数据上直接ALTER。

## 新库初始化

`migrations/001_schema.sql`从原`src/main/resources/db/hmdp.sql`抽取13张表并修正MySQL 8零日期、关键索引和两张一致性表。它只有CREATE IF NOT EXISTS，没有DROP。`002_seed.sql`提供10类、14家店、3个虚构学习用户、1篇Blog、普通券和有效期到2037年的秒杀券；没有复制原SQL中的1005个手机号。

```bash
cd go-dianping
go run ./cmd/migrate -seed
```

迁移器使用MySQL命名锁防多实例并发，记录文件SHA-256；已经执行过的迁移文件若内容变化会拒绝启动。DDL在MySQL中可能隐式提交，因此每条CREATE均可重试，不能假装整个DDL文件拥有事务回滚。

配置默认连接本机Docker的`127.0.0.1:3307/go_dianping`。自建MySQL需先创建空数据库和用户，再用环境变量覆盖：

```powershell
$env:DIANPING_MYSQL_DSN='USER:PASSWORD@tcp(127.0.0.1:3306)/go_dianping?charset=utf8mb4&parseTime=true&loc=Asia%2FShanghai'
go run ./cmd/migrate -seed
```

不要把真实密码写入Git。`parseTime=true`让驱动扫描time.Time，`loc=Asia%2FShanghai`决定无时区数据库值的解释；HTTP输出为带时区RFC3339。

## 升级原Java数据库

先做备份并停止Java/Go写流量。Go迁移器检测到有`tb_user`却没有迁移账本时会拒绝，避免把新库初始化误用于生产旧库。人工流程：

1. 备份MySQL和Redis，记录Java版本与切换点。
2. 执行重复检查：关注组合、用户券订单组合必须无重复。决定保留规则后，在备份上清理重复行。
3. 检查秒杀begin/end不存在`0000-00-00 00:00:00`。修正后执行`migrations/legacy/upgrade.sql`。
4. 旧SQL只有Blog点赞总数，没有“谁点赞”的行；`tb_blog_like`无法凭空还原身份。切换前应导出Java `blog:liked:*` ZSet并导入新表或明确只保留计数。
5. Go outbox只覆盖Go发布的Blog。旧`feed:*` Redis历史应原样保留或离线转为outbox，不能从Blog表准确推导当时谁已关注。
6. 停止Java秒杀入口，排空/核对RabbitMQ QA/QD；旧`stream.orders`可能已经由RabbitMQ路径落库，逐订单ID与数据库核对。Go使用新`stream:{seckill}:orders`，不要直接整体改名。
7. 按SECKILL.md恢复每张活动的库存、已购关系和未完成请求，再启动Go。

`legacy/upgrade.sql`不会删除行，但添加唯一索引会在重复数据存在时失败；这是保护，不应通过去掉索引绕过。

## 事务和索引为什么这样设计

订单事务内使用`UPDATE ... stock > 0`并检查RowsAffected，再插订单。插入失败返回error会让库存更新一起回滚。复合唯一索引处理两个消费者同时“先查都不存在”的竞争；应用查询不能代替数据库约束。

点赞事务锁Blog行，使明细存在性与liked计数一起切换。关注的唯一索引让重复true请求变成幂等。Blog发布和FeedOutbox在一个事务中，MySQL提交即代表投递意图不会因Redis短暂失败消失。

## 常用只读检查

```sql
SELECT user_id,voucher_id,COUNT(*) FROM tb_voucher_order GROUP BY user_id,voucher_id HAVING COUNT(*)>1;
SELECT voucher_id,stock FROM tb_seckill_voucher WHERE stock<0;
SELECT b.id,b.liked,COUNT(l.user_id) AS details FROM tb_blog b LEFT JOIN tb_blog_like l ON l.blog_id=b.id GROUP BY b.id HAVING b.liked<>details;
SELECT COUNT(*) FROM tb_feed_outbox WHERE delivered_at IS NULL;
```

排查时同时看Redis Stream Pending。Redis预扣库存暂时少于MySQL库存可能只是消息尚未落库；只有在Pending清空、失败补偿完成后才适合比较最终库存。
