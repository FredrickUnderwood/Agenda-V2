-- agenda-v2 built-in users.
--
-- The platform's own identity layer (see internal/auth + internal/service/
-- user_service.go), replacing the external user-core dependency so the platform
-- is self-contained. Passwords are bcrypt hashes. No foreign keys, per the
-- project convention. Applied by hand like 0001/0002.
--
--   mysql -h <host> -u <user> -p <database> < resources/migrations/0003_user.sql

CREATE TABLE `user`
(
    `id`            BIGINT       NOT NULL AUTO_INCREMENT,
    `username`      VARCHAR(64)  NOT NULL,
    `password_hash` VARCHAR(255) NOT NULL,
    `display_name`  VARCHAR(128) NOT NULL DEFAULT '',
    `role`          VARCHAR(16)  NOT NULL DEFAULT 'member',
    `is_active`     TINYINT(1)   NOT NULL DEFAULT 1,
    `created_at`    DATETIME(3)  NULL,
    `updated_at`    DATETIME(3)  NULL,
    PRIMARY KEY (`id`),
    UNIQUE INDEX `idx_user_username` (`username`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
