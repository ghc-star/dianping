-- Upgrade an existing Java hmdp database after backup and duplicate checks.
-- Run this manually; the Go migration command deliberately refuses legacy DBs.
SET NAMES utf8mb4;

-- These queries must return zero rows before adding the unique constraints.
SELECT user_id, follow_user_id, COUNT(*) AS duplicates
FROM tb_follow GROUP BY user_id, follow_user_id HAVING COUNT(*) > 1;
SELECT user_id, voucher_id, COUNT(*) AS duplicates
FROM tb_voucher_order GROUP BY user_id, voucher_id HAVING COUNT(*) > 1;

CREATE TABLE IF NOT EXISTS tb_blog_like (
  blog_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  create_time TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (blog_id, user_id),
  KEY idx_like_ranking (blog_id, create_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS tb_feed_outbox (
  id BIGINT NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  blog_id BIGINT UNSIGNED NOT NULL,
  score BIGINT NOT NULL,
  delivered_at TIMESTAMP(3) NULL,
  create_time TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_feed_user_blog (user_id, blog_id),
  KEY idx_feed_pending (delivered_at, id),
  KEY idx_feed_history (user_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Re-runnable index creation for MySQL 8.0.
SET @sql = IF(
  EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='tb_follow' AND index_name='uq_follow_user_target'),
  'SELECT 1',
  'ALTER TABLE tb_follow ADD UNIQUE KEY uq_follow_user_target (user_id,follow_user_id), ADD KEY idx_follow_fans (follow_user_id,user_id)'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
  EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='tb_voucher_order' AND index_name='uq_order_user_voucher'),
  'SELECT 1',
  'ALTER TABLE tb_voucher_order ADD UNIQUE KEY uq_order_user_voucher (user_id,voucher_id), ADD KEY idx_order_voucher (voucher_id)'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Fix the MySQL 8 strict-mode-incompatible zero-date defaults if the legacy
-- table was created with them. Existing zero dates must be corrected first.
ALTER TABLE tb_seckill_voucher
  MODIFY begin_time TIMESTAMP(3) NOT NULL,
  MODIFY end_time TIMESTAMP(3) NOT NULL;

CREATE TABLE IF NOT EXISTS schema_migrations (
  name VARCHAR(128) PRIMARY KEY,
  checksum VARCHAR(64) NOT NULL,
  applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Do not insert a checksum here: this manual schema is not byte-identical to
-- the fresh migration. Record the operational change in your own release log.
