"""Reproduce fresh-database DDL from the checked-in Java SQL without DROP or personal data."""
from pathlib import Path
import re

root = Path(__file__).resolve().parents[1]
source = (root.parent / 'src/main/resources/db/hmdp.sql').read_text(encoding='utf-8')
tables = re.findall(r'CREATE TABLE `([^`]+)`\s*\(.*?\) ENGINE[^;]+;', source, re.S)
ddl = re.findall(r'CREATE TABLE `[^`]+`\s*\(.*?\) ENGINE[^;]+;', source, re.S)
indexes = {
    'tb_follow': ',\n  UNIQUE KEY uq_follow_user_target (user_id,follow_user_id),\n  KEY idx_follow_fans (follow_user_id,user_id)',
    'tb_voucher_order': ',\n  UNIQUE KEY uq_order_user_voucher (user_id,voucher_id),\n  KEY idx_order_voucher (voucher_id)',
    'tb_blog': ',\n  KEY idx_blog_user (user_id,id),\n  KEY idx_blog_hot (liked,id)',
    'tb_voucher': ',\n  KEY idx_voucher_shop_status (shop_id,status)',
}
out = ['-- Derived from the Java schema. Fresh database only; no DROP statements.\nSET NAMES utf8mb4;']
for name, sql in zip(tables, ddl):
    sql = sql.replace('CREATE TABLE ', 'CREATE TABLE IF NOT EXISTS ', 1)
    sql = sql.replace("DEFAULT '0000-00-00 00:00:00'", '')
    sql = sql.replace('double UNSIGNED', 'double')
    sql = re.sub(r'timestamp\b', 'timestamp(3)', sql)
    sql = sql.replace('CURRENT_TIMESTAMP', 'CURRENT_TIMESTAMP(3)')
    sql = re.sub(r'AUTO_INCREMENT\s*=\s*\d+', '', sql)
    if name in indexes:
        pos = sql.rfind(') ENGINE')
        sql = sql[:pos].rstrip() + indexes[name] + '\n' + sql[pos:]
    out.append(sql)
out.append('''CREATE TABLE IF NOT EXISTS tb_blog_like (
 blog_id bigint unsigned NOT NULL, user_id bigint unsigned NOT NULL,
 create_time timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
 PRIMARY KEY(blog_id,user_id), KEY idx_like_ranking(blog_id,create_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS tb_feed_outbox (
 id bigint NOT NULL AUTO_INCREMENT, user_id bigint unsigned NOT NULL,
 blog_id bigint unsigned NOT NULL, score bigint NOT NULL,
 delivered_at timestamp(3) NULL,
 create_time timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
 PRIMARY KEY(id), UNIQUE KEY uq_feed_user_blog(user_id,blog_id),
 KEY idx_feed_pending(delivered_at,id), KEY idx_feed_history(user_id,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;''')
(root/'migrations').mkdir(exist_ok=True)
(root/'migrations/001_schema.sql').write_text('\n\n'.join(out)+'\n',encoding='utf-8')
seed = ['-- Development fixtures; no original user phone numbers.\nSET NAMES utf8mb4;']
for table in ['tb_shop_type','tb_shop']:
    for line in source.splitlines():
        if line.startswith('INSERT INTO `'+table+'`'):
            seed.append(line.replace('INSERT INTO','INSERT IGNORE INTO',1))
seed.append('''INSERT IGNORE INTO tb_user(id,phone,nick_name,icon) VALUES
 (1,'13900000001','学习用户一',''),(2,'13900000002','学习用户二',''),(3,'13900000003','学习用户三','');
INSERT IGNORE INTO tb_blog(id,shop_id,user_id,title,images,content,liked,comments) VALUES
 (1,1,1,'从这篇探店开始学习 Go','/imgs/demo.png','先登录，再点赞、关注和查看 Feed。',0,0);
INSERT IGNORE INTO tb_voucher(id,shop_id,title,sub_title,rules,pay_value,actual_value,type,status) VALUES
 (1,1,'学习普通券','金额单位为分','仅供本地测试',8000,10000,0,1),
 (2,1,'学习秒杀券','100 份库存','每人限购一份，仅供本地测试',5000,10000,1,1);
INSERT IGNORE INTO tb_seckill_voucher(voucher_id,stock,begin_time,end_time) VALUES
 (2,100,'2020-01-01 00:00:00','2037-01-01 00:00:00');''')
(root/'migrations/002_seed.sql').write_text('\n'.join(seed)+'\n',encoding='utf-8')
print('Generated 13 tables and development fixtures.')
