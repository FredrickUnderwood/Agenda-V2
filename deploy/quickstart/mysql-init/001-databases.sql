-- Control plane and gateway are separate services with separate schemas
-- (AutoMigrate on each), so they get separate databases even though they
-- share one MySQL instance in this single-host quickstart.
CREATE DATABASE IF NOT EXISTS agenda_v2 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS agenda_gateway CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
GRANT ALL PRIVILEGES ON agenda_v2.* TO 'agenda'@'%';
GRANT ALL PRIVILEGES ON agenda_gateway.* TO 'agenda'@'%';
FLUSH PRIVILEGES;
