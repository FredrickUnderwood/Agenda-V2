-- agenda-v2 initial schema.
--
-- No foreign keys anywhere in this schema by design: every relation
-- (application_id, deploy_target_id, deploy_log_id, previous_release_id...)
-- is a plain integer resolved in the application/service layer. This is a
-- deliberate convention carried over from agenda v1 — see
-- doc/deploy-blue-green-tech-design.md and the project's "DB 不用外键"
-- guideline: keep join/consistency logic in Go, keep only the indexes that
-- make the actual read patterns fast.
--
-- There is no AutoMigrate anywhere in this codebase (see
-- internal/repository/db.go) — this file, applied by hand (or by your own
-- CI/deploy tooling) against the target MySQL instance, is the only source
-- of schema. Apply with e.g.:
--   mysql -h <host> -u <user> -p <database> < resources/migrations/0001_init_schema.sql

CREATE TABLE `machine`
(
    `id`             BIGINT       NOT NULL AUTO_INCREMENT,
    `name`           VARCHAR(128) NOT NULL,
    `machine_type`   VARCHAR(16)  NOT NULL DEFAULT 'prod',
    `host`           VARCHAR(255) NOT NULL,
    `port`           INT          NOT NULL DEFAULT 22,
    `user`           VARCHAR(64)  NOT NULL DEFAULT '',
    `auth_type`      VARCHAR(16)  NOT NULL DEFAULT 'sshkey',
    `ssh_key_path`   VARCHAR(512) NOT NULL DEFAULT '',
    `password`       VARCHAR(255) NOT NULL DEFAULT '',
    `workspace_root` VARCHAR(512) NOT NULL DEFAULT '',
    `created_at`     DATETIME(3)  NULL,
    `updated_at`     DATETIME(3)  NULL,
    PRIMARY KEY (`id`),
    UNIQUE INDEX `idx_machine_name` (`name`),
    INDEX `idx_machine_type` (`machine_type`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `application`
(
    `id`            BIGINT       NOT NULL AUTO_INCREMENT,
    `name`          VARCHAR(128) NOT NULL,
    `repo_url`      VARCHAR(512) NOT NULL,
    `deploy_method` VARCHAR(16)  NOT NULL,
    `deploy_config` TEXT         NOT NULL,
    `description`   TEXT         NOT NULL,
    `created_at`    DATETIME(3)  NULL,
    `updated_at`    DATETIME(3)  NULL,
    PRIMARY KEY (`id`),
    UNIQUE INDEX `idx_application_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `application_env_target`
(
    `id`                              BIGINT       NOT NULL AUTO_INCREMENT,
    `application_id`                  BIGINT       NOT NULL,
    `env`                             VARCHAR(16)  NOT NULL,
    `instance_name`                   VARCHAR(64)  NOT NULL DEFAULT 'default',
    `display_name`                    VARCHAR(128) NOT NULL DEFAULT '',
    `machine_id`                      BIGINT       NOT NULL DEFAULT 0,
    `port`                            INT          NOT NULL DEFAULT 0,
    `enabled`                         TINYINT(1)   NOT NULL DEFAULT 1,
    `health_check_enabled`            TINYINT(1)   NOT NULL DEFAULT 0,
    `health_check_type`               VARCHAR(16)  NOT NULL DEFAULT 'http',
    `health_check_scheme`             VARCHAR(16)  NOT NULL DEFAULT 'http',
    `health_check_host`               VARCHAR(255) NOT NULL DEFAULT '',
    `health_check_url`                VARCHAR(1024) NOT NULL DEFAULT '',
    `health_check_path`               VARCHAR(255) NOT NULL DEFAULT '/healthz',
    `health_check_method`             VARCHAR(16)  NOT NULL DEFAULT 'GET',
    `health_check_expected_status`    INT          NOT NULL DEFAULT 200,
    `health_check_timeout_ms`         INT          NOT NULL DEFAULT 3000,
    `health_check_interval_sec`       INT          NOT NULL DEFAULT 30,
    `health_check_failure_threshold`  INT          NOT NULL DEFAULT 3,
    `health_check_success_threshold`  INT          NOT NULL DEFAULT 1,
    `env_override_json`               TEXT         NOT NULL,
    `created_at`                      DATETIME(3)  NULL,
    `updated_at`                      DATETIME(3)  NULL,
    PRIMARY KEY (`id`),
    UNIQUE INDEX `idx_app_env_target_app_env_instance` (`application_id`, `env`, `instance_name`),
    INDEX `idx_app_env_target_application_id` (`application_id`),
    INDEX `idx_app_env_target_env` (`env`),
    INDEX `idx_app_env_target_machine_port` (`machine_id`, `port`),
    INDEX `idx_app_env_target_health` (`enabled`, `health_check_enabled`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `application_instance_health`
(
    `id`                     BIGINT      NOT NULL AUTO_INCREMENT,
    `target_id`              BIGINT      NOT NULL DEFAULT 0,
    `application_id`         BIGINT      NOT NULL DEFAULT 0,
    `env`                    VARCHAR(16) NOT NULL DEFAULT 'prod',
    `instance_name`          VARCHAR(64) NOT NULL DEFAULT 'default',
    `status`                 VARCHAR(16) NOT NULL DEFAULT 'unknown',
    `checked_at`             DATETIME(3) NULL,
    `http_status`            INT         NOT NULL DEFAULT 0,
    `latency_ms`             INT         NOT NULL DEFAULT 0,
    `error_msg`              TEXT        NOT NULL,
    `consecutive_successes`  INT         NOT NULL DEFAULT 0,
    `consecutive_failures`   INT         NOT NULL DEFAULT 0,
    `last_success_at`        DATETIME(3) NULL,
    `last_failure_at`        DATETIME(3) NULL,
    `created_at`             DATETIME(3) NULL,
    `updated_at`             DATETIME(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE INDEX `idx_app_instance_health_target` (`target_id`),
    INDEX `idx_app_instance_health_app_env` (`application_id`, `env`),
    INDEX `idx_app_instance_health_status` (`status`, `checked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `application_gateway_route`
(
    `id`                   BIGINT       NOT NULL AUTO_INCREMENT,
    `application_id`       BIGINT       NOT NULL DEFAULT 0,
    `env`                  VARCHAR(16)  NOT NULL,
    `route_key`            VARCHAR(128) NOT NULL,
    `host`                 VARCHAR(255) NOT NULL,
    `path_prefix`          VARCHAR(255) NOT NULL DEFAULT '/',
    `strip_prefix`         TINYINT(1)   NOT NULL DEFAULT 0,
    `backend_path`         VARCHAR(255) NOT NULL DEFAULT '',
    `enabled`              TINYINT(1)   NOT NULL DEFAULT 1,
    `backend_mode`         VARCHAR(16)  NOT NULL DEFAULT 'single',
    `instance_select_mode` VARCHAR(16)  NOT NULL DEFAULT 'disabled',
    `instance_header`      VARCHAR(64)  NOT NULL DEFAULT 'X-Agenda-Instance',
    `sort_order`           INT          NOT NULL DEFAULT 0,
    `created_at`           DATETIME(3)  NULL,
    `updated_at`           DATETIME(3)  NULL,
    PRIMARY KEY (`id`),
    UNIQUE INDEX `idx_app_gateway_route_key` (`route_key`),
    UNIQUE INDEX `idx_app_gateway_host_path` (`host`, `path_prefix`),
    INDEX `idx_app_gateway_app_env` (`application_id`, `env`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `application_gateway_route_backend`
(
    `id`         BIGINT      NOT NULL AUTO_INCREMENT,
    `route_id`   BIGINT      NOT NULL DEFAULT 0,
    `target_id`  BIGINT      NOT NULL DEFAULT 0,
    `weight`     INT         NOT NULL DEFAULT 1,
    `enabled`    TINYINT(1)  NOT NULL DEFAULT 1,
    `created_at` DATETIME(3) NULL,
    `updated_at` DATETIME(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE INDEX `idx_app_gateway_route_backend_route_target` (`route_id`, `target_id`),
    INDEX `idx_app_gateway_route_backend_route` (`route_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `application_environment`
(
    `id`             BIGINT      NOT NULL AUTO_INCREMENT,
    `application_id` BIGINT      NOT NULL,
    `env`            VARCHAR(16) NOT NULL,
    `env_vars_json`  TEXT        NOT NULL,
    `config_json`    TEXT        NOT NULL,
    `created_at`     DATETIME(3) NULL,
    `updated_at`     DATETIME(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE INDEX `idx_app_environment_app_env` (`application_id`, `env`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- application_release is the append-only "what got deployed and is it
-- confirmed good" record. "Currently live" = the newest row for
-- (application_id, env, instance_name) with status='verified'. See
-- doc/release-console-tech-design.md section 2.1 F-02 for the full state
-- machine (draft -> deploying -> pending_verify -> verified, and the
-- rolling_back/rolled_back pair on the release a rollback supersedes).
CREATE TABLE `application_release`
(
    `id`                     BIGINT       NOT NULL AUTO_INCREMENT,
    `application_id`         BIGINT       NOT NULL,
    `env`                    VARCHAR(16)  NOT NULL,
    `instance_name`          VARCHAR(64)  NOT NULL DEFAULT 'default',
    `machine_id`             BIGINT       NOT NULL DEFAULT 0,
    `branch`                 VARCHAR(128) NOT NULL DEFAULT 'main',
    `commit_sha`             VARCHAR(64)  NOT NULL DEFAULT '',
    `previous_release_id`    BIGINT       NOT NULL DEFAULT 0,
    `previous_commit_sha`    VARCHAR(64)  NOT NULL DEFAULT '',
    `deploy_log_id`          BIGINT       NOT NULL DEFAULT 0,
    `status`                 VARCHAR(16)  NOT NULL DEFAULT 'draft',
    `deploy_config_snapshot` TEXT         NOT NULL,
    `env_config_snapshot`    TEXT         NOT NULL,
    `route_snapshot`         TEXT         NOT NULL,
    `operator`               VARCHAR(128) NOT NULL DEFAULT '',
    `deployed_at`            DATETIME(3)  NULL,
    `verified_at`            DATETIME(3)  NULL,
    `created_at`             DATETIME(3)  NULL,
    `updated_at`             DATETIME(3)  NULL,
    PRIMARY KEY (`id`),
    INDEX `idx_app_release_app_env_instance` (`application_id`, `env`, `instance_name`),
    INDEX `idx_app_release_status` (`status`),
    INDEX `idx_app_release_deploy_log` (`deploy_log_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- deploy_log is one physical pipeline execution, always driven by a release
-- (release_id). branch/trigger_sha are snapshotted here (rather than joined
-- from application_release) because a retry/resume must see the exact
-- values the run started with even if the release row changes later.
CREATE TABLE `deploy_log`
(
    `id`               BIGINT      NOT NULL AUTO_INCREMENT,
    `application_id`   BIGINT      NOT NULL,
    `release_id`       BIGINT      NOT NULL DEFAULT 0,
    `env`              VARCHAR(16) NOT NULL DEFAULT 'prod',
    `deploy_target_id` BIGINT      NOT NULL DEFAULT 0,
    `instance_name`    VARCHAR(64) NOT NULL DEFAULT 'default',
    `machine_id`       BIGINT      NOT NULL DEFAULT 0,
    `deploy_port`      INT         NOT NULL DEFAULT 0,
    `branch`           VARCHAR(128) NOT NULL DEFAULT '',
    `trigger_sha`      VARCHAR(64) NOT NULL DEFAULT '',
    `status`           VARCHAR(16) NOT NULL DEFAULT 'pending',
    `pause_requested`  TINYINT(1)  NOT NULL DEFAULT 0,
    `output`           TEXT        NOT NULL,
    `error_msg`        TEXT        NOT NULL,
    `started_at`       DATETIME(3) NOT NULL,
    `finished_at`      DATETIME(3) NULL,
    `duration_ms`      BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    INDEX `idx_deploy_log_application_id` (`application_id`),
    INDEX `idx_deploy_log_release_id` (`release_id`),
    INDEX `idx_deploy_log_env` (`env`),
    INDEX `idx_deploy_log_target_instance` (`deploy_target_id`, `instance_name`),
    INDEX `idx_deploy_log_started` (`started_at` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `pipeline_step`
(
    `id`            BIGINT      NOT NULL AUTO_INCREMENT,
    `deploy_log_id` BIGINT      NOT NULL,
    `idx`           INT         NOT NULL DEFAULT 0,
    `name`          VARCHAR(64) NOT NULL,
    `step_type`     VARCHAR(32) NOT NULL,
    `status`        VARCHAR(16) NOT NULL DEFAULT 'pending',
    `output`        TEXT        NOT NULL,
    `error_msg`     TEXT        NOT NULL,
    `started_at`    DATETIME(3) NULL,
    `finished_at`   DATETIME(3) NULL,
    `duration_ms`   BIGINT      NOT NULL DEFAULT 0,
    `attempt`       INT         NOT NULL DEFAULT 1,
    PRIMARY KEY (`id`),
    INDEX `idx_pipeline_step_deploy_log_id` (`deploy_log_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
