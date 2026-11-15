-- FO3 Go server schema (MariaDB, utf8mb4).
-- User payload documents live in user_state.doc as AES-256-GCM
-- ciphertext (base64), sealed by the application: the database itself
-- never holds readable user data.

CREATE TABLE IF NOT EXISTS users (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  username    VARCHAR(64)  NOT NULL,
  pass_hash   VARCHAR(255) NOT NULL,             -- Argon2id PHC
  legacy_hash VARCHAR(64)  NULL,                 -- Python-era SHA-256, cleared on upgrade
  pass2_hash  VARCHAR(255) NULL,                 -- second password, Argon2id PHC
  coach_name  VARCHAR(64)  NOT NULL DEFAULT '',
  elo         INT          NOT NULL DEFAULT 1000,
  token_hash  CHAR(64)     NULL,                 -- SHA-256 of the session token
  last_ip     VARCHAR(45)  NULL,
  disabled    TINYINT(1)   NOT NULL DEFAULT 0,
  created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_users_username (username)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS coaches (
  user_id     BIGINT UNSIGNED NOT NULL,
  slot        INT NOT NULL DEFAULT 0,
  coach_id    CHAR(24) NOT NULL,                 -- hex id the client knows
  name        VARCHAR(64) NOT NULL,
  teamid      INT NOT NULL DEFAULT 0,
  level       INT NOT NULL DEFAULT 1,
  exp         INT NOT NULL DEFAULT 0,
  ep          BIGINT NOT NULL DEFAULT 0,
  gp          INT NOT NULL DEFAULT 0,            -- quest GP, weekly
  badge       INT NOT NULL DEFAULT 1001,
  PRIMARY KEY (user_id, slot),
  UNIQUE KEY uq_coaches_coachid (coach_id)
) ENGINE=InnoDB;

-- Encrypted per-user, per-domain state documents (teams, league2, cups,
-- achievements, quests, inventory, ...). doc = base64 AES-256-GCM.
CREATE TABLE IF NOT EXISTS user_state (
  user_id     BIGINT UNSIGNED NOT NULL,
  domain      VARCHAR(32) NOT NULL,
  doc         MEDIUMTEXT NOT NULL,
  updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, domain)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS friends (
  coach_id        CHAR(24) NOT NULL,
  friend_coach_id CHAR(24) NOT NULL,
  group_name      VARCHAR(64) NULL,
  status          VARCHAR(16) NOT NULL DEFAULT 'accepted',
  PRIMARY KEY (coach_id, friend_coach_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS contact_groups (
  coach_id CHAR(24) NOT NULL,
  name     VARCHAR(64) NOT NULL,
  PRIMARY KEY (coach_id, name)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS clubs (
  clubid     CHAR(24) NOT NULL,
  name       VARCHAR(64) NOT NULL,
  master_id  CHAR(24) NOT NULL,
  doc        MEDIUMTEXT NOT NULL,   -- encrypted club document
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (clubid)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS club_members (
  clubid   CHAR(24) NOT NULL,
  coach_id CHAR(24) NOT NULL,
  grade    INT NOT NULL DEFAULT 1,
  PRIMARY KEY (clubid, coach_id),
  KEY idx_member_coach (coach_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS club_requests (
  clubid   CHAR(24) NOT NULL,
  coach_id CHAR(24) NOT NULL,
  PRIMARY KEY (clubid, coach_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS club_posts (
  clubid   CHAR(24) NOT NULL,
  post_id  CHAR(24) NOT NULL,
  doc      MEDIUMTEXT NOT NULL,   -- encrypted post document
  at_ts    DOUBLE NOT NULL DEFAULT 0,
  PRIMARY KEY (clubid, post_id),
  KEY idx_posts_at (clubid, at_ts)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS settings (
  name  VARCHAR(64) NOT NULL,
  value TEXT NOT NULL,              -- JSON
  PRIMARY KEY (name)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS telemetry_errors (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    BIGINT UNSIGNED NULL,
  uri        VARCHAR(255) NULL,
  category   VARCHAR(32) NULL,
  message    TEXT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_telemetry_created (created_at)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS admin_audit (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  admin      VARCHAR(64) NOT NULL,
  action     VARCHAR(64) NOT NULL,
  target     VARCHAR(128) NULL,
  detail     TEXT NULL,             -- JSON {old, new}
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS admins (
  id        BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  username  VARCHAR(64) NOT NULL,
  pass_hash VARCHAR(255) NOT NULL,  -- Argon2id PHC
  role      VARCHAR(16) NOT NULL DEFAULT 'admin',
  PRIMARY KEY (id),
  UNIQUE KEY uq_admins_username (username)
) ENGINE=InnoDB;
