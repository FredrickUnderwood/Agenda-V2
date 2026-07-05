-- agenda-v2 runtime settings.
--
-- The `setting` table holds runtime-mutable configuration that would otherwise
-- force a process restart to change — e.g. rotating GitHub tokens
-- (git.token.<host>). Bootstrap config (DB DSN, listen addr, the encryption
-- master key) stays in agenda-v2.yaml; anything that rotates lives here so it
-- can be updated through the API without a redeploy.
--
-- Secret values are stored encrypted at rest as an "enc:v1:<base64>" blob when
-- security.master_key is configured (see internal/secret). No foreign keys, per
-- the project convention (see 0001_init_schema.sql).
--
-- Apply with e.g.:
--   mysql -h <host> -u <user> -p <database> < resources/migrations/0002_setting.sql

CREATE TABLE `setting`
(
    `id`          BIGINT       NOT NULL AUTO_INCREMENT,
    `setting_key` VARCHAR(191) NOT NULL,
    `value`       TEXT         NOT NULL,
    `type`        VARCHAR(16)  NOT NULL DEFAULT 'string',
    `is_secret`   TINYINT(1)   NOT NULL DEFAULT 0,
    `updated_by`  BIGINT       NOT NULL DEFAULT 0,
    `created_at`  DATETIME(3)  NULL,
    `updated_at`  DATETIME(3)  NULL,
    PRIMARY KEY (`id`),
    UNIQUE INDEX `idx_setting_key` (`setting_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
