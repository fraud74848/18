-- backend/schema_postgresql.sql
-- PostgreSQL 完整数据库 Schema

-- ========== 启用扩展 ==========
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ========== 用户表 ==========
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    username VARCHAR(100) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    email VARCHAR(255),
    real_name VARCHAR(100),
    status SMALLINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE,
    last_login_at TIMESTAMP WITH TIME ZONE
);

-- ========== 角色表 ==========
CREATE TABLE IF NOT EXISTS roles (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) NOT NULL UNIQUE,
    description TEXT,
    permissions JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- ========== 用户角色关联表 ==========
CREATE TABLE IF NOT EXISTS user_roles (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, role_id)
);

-- ========== 设备表（完整字段） ==========
CREATE TABLE IF NOT EXISTS devices (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    employee_id VARCHAR(100),
    employee_name VARCHAR(255),
    employee_number VARCHAR(100),
    position VARCHAR(100),
    device_fingerprint VARCHAR(255) NOT NULL UNIQUE,
    hostname VARCHAR(255) NOT NULL,
    os_version VARCHAR(255),
    agent_version VARCHAR(50),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    bound_at TIMESTAMP WITH TIME ZONE,
    last_seen_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
    ,updated_at TIMESTAMP WITH TIME ZONE
    ,device_secret VARCHAR(128)
);

-- 设备表索引
CREATE INDEX IF NOT EXISTS idx_devices_device_fingerprint ON devices(device_fingerprint);
CREATE INDEX IF NOT EXISTS idx_devices_status ON devices(status);
CREATE INDEX IF NOT EXISTS idx_devices_last_seen ON devices(last_seen_at);
CREATE INDEX IF NOT EXISTS idx_devices_employee_id ON devices(employee_id);
CREATE INDEX IF NOT EXISTS idx_devices_employee_name ON devices(employee_name);
CREATE INDEX IF NOT EXISTS idx_devices_employee_number ON devices(employee_number);
CREATE INDEX IF NOT EXISTS idx_devices_position ON devices(position);

-- ========== 录屏记录表 ==========
CREATE TABLE IF NOT EXISTS recordings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    start_time TIMESTAMP WITH TIME ZONE NOT NULL,
    end_time TIMESTAMP WITH TIME ZONE NOT NULL,
    file_path TEXT NOT NULL,
    file_size BIGINT NOT NULL DEFAULT 0,
    sha256 VARCHAR(64),
    uploaded BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- 录屏表索引
CREATE INDEX IF NOT EXISTS idx_recordings_device_id ON recordings(device_id);
CREATE INDEX IF NOT EXISTS idx_recordings_start_time ON recordings(start_time);
CREATE INDEX IF NOT EXISTS idx_recordings_uploaded ON recordings(uploaded);
CREATE INDEX IF NOT EXISTS idx_recordings_device_start ON recordings(device_id, start_time DESC);

-- ========== 截图表 ==========
CREATE TABLE IF NOT EXISTS screenshots (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    captured_at TIMESTAMP WITH TIME ZONE NOT NULL,
    file_path TEXT NOT NULL,
    file_size BIGINT NOT NULL DEFAULT 0,
    uploaded BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- 截图表索引
CREATE INDEX IF NOT EXISTS idx_screenshots_device_id ON screenshots(device_id);
CREATE INDEX IF NOT EXISTS idx_screenshots_captured_at ON screenshots(captured_at);
CREATE INDEX IF NOT EXISTS idx_screenshots_device_captured ON screenshots(device_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_screenshots_uploaded ON screenshots(uploaded);

-- ========== 活动日志表 ==========
CREATE TABLE IF NOT EXISTS activity_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    process_name VARCHAR(255),
    window_title TEXT,
    browser_title TEXT,
    started_at TIMESTAMP WITH TIME ZONE NOT NULL,
    ended_at TIMESTAMP WITH TIME ZONE,
    is_idle BOOLEAN NOT NULL DEFAULT FALSE,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- 活动日志表索引
CREATE INDEX IF NOT EXISTS idx_activity_logs_device_id ON activity_logs(device_id);
CREATE INDEX IF NOT EXISTS idx_activity_logs_started_at ON activity_logs(started_at);
CREATE INDEX IF NOT EXISTS idx_activity_logs_process_name ON activity_logs(process_name);
CREATE INDEX IF NOT EXISTS idx_activity_logs_device_started ON activity_logs(device_id, started_at DESC);

-- ========== 策略表 ==========
CREATE TABLE IF NOT EXISTS policies (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(200) NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    content JSONB NOT NULL,
    signature VARCHAR(255),
    is_current BOOLEAN NOT NULL DEFAULT FALSE,
    status SMALLINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE
);

-- 策略表索引
CREATE INDEX IF NOT EXISTS idx_policies_is_current ON policies(is_current) WHERE is_current = TRUE;
CREATE INDEX IF NOT EXISTS idx_policies_version ON policies(version);

-- ========== 上传任务表 ==========
CREATE TABLE IF NOT EXISTS upload_tasks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    task_type VARCHAR(50) NOT NULL DEFAULT 'upload_recordings',
    start_time TIMESTAMP WITH TIME ZONE NOT NULL,
    end_time TIMESTAMP WITH TIME ZONE,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    progress INTEGER NOT NULL DEFAULT 0,
    result TEXT,
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- 上传任务表索引
CREATE INDEX IF NOT EXISTS idx_upload_tasks_device_id ON upload_tasks(device_id);
CREATE INDEX IF NOT EXISTS idx_upload_tasks_status ON upload_tasks(status);
CREATE INDEX IF NOT EXISTS idx_upload_tasks_device_status ON upload_tasks(device_id, status);
CREATE INDEX IF NOT EXISTS idx_upload_tasks_created_at ON upload_tasks(created_at);

-- ========== 清理策略表 ==========
CREATE TABLE IF NOT EXISTS cleanup_policy (
    id VARCHAR(50) PRIMARY KEY DEFAULT 'default',
    auto_cleanup_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    recording_retention_days INTEGER NOT NULL DEFAULT 30,
    screenshot_retention_days INTEGER NOT NULL DEFAULT 30,
    log_retention_days INTEGER NOT NULL DEFAULT 90,
    disk_usage_threshold_percent INTEGER NOT NULL DEFAULT 80,
    cleanup_interval_minutes INTEGER NOT NULL DEFAULT 60,
    last_run_at TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- ========== 清理日志表 ==========
CREATE TABLE IF NOT EXISTS cleanup_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trigger_type VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL,
    deleted_recordings INTEGER NOT NULL DEFAULT 0,
    deleted_screenshots INTEGER NOT NULL DEFAULT 0,
    deleted_logs INTEGER NOT NULL DEFAULT 0,
    deleted_bytes BIGINT NOT NULL DEFAULT 0,
    message TEXT,
    started_at TIMESTAMP WITH TIME ZONE NOT NULL,
    completed_at TIMESTAMP WITH TIME ZONE NOT NULL
);

-- 清理日志表索引
CREATE INDEX IF NOT EXISTS idx_cleanup_logs_started_at ON cleanup_logs(started_at);
CREATE INDEX IF NOT EXISTS idx_cleanup_logs_trigger_type ON cleanup_logs(trigger_type);
-- ========== 员工与考勤表 ==========
CREATE TABLE IF NOT EXISTS employees (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    employee_id VARCHAR(100),
    name VARCHAR(255),
    position VARCHAR(100),
    hire_date DATE,
    work_location VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_employees_employee_id ON employees(employee_id);

CREATE TABLE IF NOT EXISTS attendance_records (
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    year_month VARCHAR(7) NOT NULL,
    date DATE NOT NULL,
    status VARCHAR(50) NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (employee_id, year_month, date)
);
CREATE INDEX IF NOT EXISTS idx_attendance_records_employee_id ON attendance_records(employee_id);

CREATE TABLE IF NOT EXISTS performance_records (
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    employee_name VARCHAR(255),
    position VARCHAR(100),
    score_records JSONB,
    total_score INTEGER DEFAULT 0,
    grade VARCHAR(50),
    month VARCHAR(7) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (employee_id, month)
);
CREATE INDEX IF NOT EXISTS idx_performance_records_month ON performance_records(month);

CREATE TABLE IF NOT EXISTS penalty_records (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    employee_name VARCHAR(255),
    position VARCHAR(100),
    amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    category VARCHAR(100),
    reason TEXT,
    penalty_date DATE NOT NULL,
    created_by VARCHAR(100),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_penalty_records_employee_id ON penalty_records(employee_id);
CREATE INDEX IF NOT EXISTS idx_penalty_records_penalty_date ON penalty_records(penalty_date);

CREATE TABLE IF NOT EXISTS sites (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    code VARCHAR(100) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS employee_accounts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    name VARCHAR(255),
    account_name VARCHAR(255) NOT NULL,
    shift VARCHAR(50),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE,
    UNIQUE (site_id, account_name)
);
CREATE INDEX IF NOT EXISTS idx_employee_accounts_site_id ON employee_accounts(site_id);

CREATE TABLE IF NOT EXISTS site_stats (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    employee_account_id UUID NOT NULL REFERENCES employee_accounts(id) ON DELETE CASCADE,
    site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    stat_date DATE NOT NULL,
    shift VARCHAR(50),
    order_count INTEGER NOT NULL DEFAULT 0,
    avg_time_seconds INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE,
    UNIQUE (employee_account_id, stat_date, shift)
);
CREATE INDEX IF NOT EXISTS idx_site_stats_site_id ON site_stats(site_id);
CREATE INDEX IF NOT EXISTS idx_site_stats_employee_account_id ON site_stats(employee_account_id);
CREATE INDEX IF NOT EXISTS idx_site_stats_stat_date ON site_stats(stat_date);
-- ========== 更新 updated_at 的触发器函数 ==========
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- 为 users 表添加触发器
DROP TRIGGER IF EXISTS update_users_updated_at ON users;
CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- 为 policies 表添加触发器
DROP TRIGGER IF EXISTS update_policies_updated_at ON policies;
CREATE TRIGGER update_policies_updated_at
    BEFORE UPDATE ON policies
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- ========== 插入默认角色 ==========
INSERT INTO roles (id, name, description, permissions, created_at, updated_at) VALUES
    (uuid_generate_v4(), '管理员', '拥有所有权限', '["user:view","user:create","user:update","user:delete","user:manage","role:view","role:create","role:update","role:delete","device:view","device:manage","device:bind","device:unbind","recording:view","recording:play","recording:export","recording:delete","recording:upload","screenshot:view","screenshot:delete","activity:view","activity:export","activity:delete","policy:view","policy:create","policy:update","policy:delete","audit:view"]'::jsonb, NOW(), NOW())
ON CONFLICT (name) DO NOTHING;

-- ========== 插入默认管理员用户 ==========
-- 注意：密码需要是 bcrypt 哈希值，'admin123' 的哈希需要预先计算
-- 或者使用应用程序启动时的 seed 功能
INSERT INTO users (id, username, password_hash, email, real_name, status, created_at)
VALUES (uuid_generate_v4(), 'admin', '$2a$10$N9qo8uLOickgx2ZMRZoMy.MrJqL5YqFQfJkQwqM5qJqL5YqFQfJk', 'admin@example.com', '系统管理员', 1, NOW())
ON CONFLICT (username) DO NOTHING;

-- ========== 初始化清理策略 ==========
INSERT INTO cleanup_policy (id, auto_cleanup_enabled, recording_retention_days, screenshot_retention_days, log_retention_days, disk_usage_threshold_percent, cleanup_interval_minutes, updated_at)
VALUES ('default', TRUE, 3, 3, 7, 80, 60, NOW())
ON CONFLICT (id) DO NOTHING;

-- ========== 安全事件 ==========
CREATE TABLE IF NOT EXISTS security_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type VARCHAR(64) NOT NULL,
    subject_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    subject_id VARCHAR(128) NOT NULL DEFAULT '',
    ip_address VARCHAR(64),
    reason VARCHAR(255),
    event_count INTEGER NOT NULL DEFAULT 1,
    details JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_security_events_created ON security_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_security_events_type_created ON security_events(event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_security_events_subject ON security_events(subject_type, subject_id, created_at DESC);