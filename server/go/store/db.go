package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luvxinc/vpn/server/models"
)

// quotaPeriodSQL maps period names to SQL expressions for the start of the period.
const quotaPeriodSQL = `CASE
    WHEN $2 = 'daily'   THEN CURRENT_DATE::TIMESTAMP
    WHEN $2 = 'weekly'  THEN date_trunc('week', CURRENT_TIMESTAMP)
    WHEN $2 = 'monthly' THEN date_trunc('month', CURRENT_TIMESTAMP)
    ELSE CURRENT_TIMESTAMP
END`

type DB struct {
	pool *pgxpool.Pool
}

func NewDB(ctx context.Context, dsn string, poolSize int) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	cfg.MaxConns = int32(poolSize)
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe

	// Register uuid.UUID codec
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterDefaultPgType(uuid.UUID{}, "uuid")
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}
	return &DB{pool: pool}, nil
}

func MustNewDB(ctx context.Context, dsn string, poolSize int) *DB {
	db, err := NewDB(ctx, dsn, poolSize)
	if err != nil {
		panic(err)
	}
	return db
}

func (d *DB) Close() {
	d.pool.Close()
}

func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// GetUserByUsername returns (id, passwordHash, isActive) or pgx.ErrNoRows.
func (d *DB) GetUserByUsername(ctx context.Context, username string) (id uuid.UUID, hash string, active bool, err error) {
	row := d.pool.QueryRow(ctx,
		"SELECT id, password_hash, is_active FROM users WHERE username=$1",
		username,
	)
	err = row.Scan(&id, &hash, &active)
	return
}

// GetDeviceByFingerprint returns (id, isActive, userID, vlessUUID) or pgx.ErrNoRows.
// vlessUUID may be empty for legacy rows that predate migration_003.
func (d *DB) GetDeviceByFingerprint(ctx context.Context, fingerprint string) (id uuid.UUID, active bool, userID uuid.UUID, vlessUUID string, err error) {
	row := d.pool.QueryRow(ctx,
		"SELECT id, is_active, user_id, COALESCE(vless_uuid, '') FROM devices WHERE device_fingerprint=$1",
		fingerprint,
	)
	err = row.Scan(&id, &active, &userID, &vlessUUID)
	return
}

// AssignDeviceUUID stores a stable VLESS UUID for a device.
// Called once on first device registration (or for legacy devices without a UUID).
// The UUID is permanent until RotateDeviceUUID is called (i.e., on kick).
func (d *DB) AssignDeviceUUID(ctx context.Context, deviceID uuid.UUID, vlessUUID string) error {
	_, err := d.pool.Exec(ctx,
		"UPDATE devices SET vless_uuid=$1 WHERE id=$2",
		vlessUUID, deviceID,
	)
	return err
}

// RotateDeviceUUID replaces a device's VLESS UUID with a newly generated one.
// Called by KickUserSessions to immediately invalidate the existing VPN tunnel.
// Returns the new UUID so the caller can update the sing-box config.
func (d *DB) RotateDeviceUUID(ctx context.Context, deviceID uuid.UUID, newUUID string) error {
	_, err := d.pool.Exec(ctx,
		"UPDATE devices SET vless_uuid=$1 WHERE id=$2",
		newUUID, deviceID,
	)
	return err
}

// GetAllActiveDeviceUsers returns the VLESS UUID for every active device
// that belongs to an active user. Used to build the full user list for
// the sing-box config (multi-user SyncUsers call).
func (d *DB) GetAllActiveDeviceUsers(ctx context.Context) ([]string, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT d.vless_uuid
		 FROM devices d
		 JOIN users u ON u.id = d.user_id
		 WHERE d.is_active = true
		   AND u.is_active = true
		   AND d.vless_uuid IS NOT NULL
		   AND d.vless_uuid != ''`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpsertDevice inserts or re-activates a device. Returns (id, isActive).
func (d *DB) UpsertDevice(ctx context.Context, userID uuid.UUID, fingerprint, name string) (id uuid.UUID, active bool, err error) {
	row := d.pool.QueryRow(ctx,
		`INSERT INTO devices (user_id, device_fingerprint, device_name)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (device_fingerprint) DO UPDATE SET is_active=true, last_seen=NOW()
		 RETURNING id, is_active`,
		userID, fingerprint, name,
	)
	err = row.Scan(&id, &active)
	return
}

// DeactivateDeviceSessions marks all active sessions for a device as inactive.
func (d *DB) DeactivateDeviceSessions(ctx context.Context, deviceID uuid.UUID) error {
	_, err := d.pool.Exec(ctx,
		"UPDATE sessions SET is_active=false, disconnected_at=NOW() WHERE device_id=$1 AND is_active=true",
		deviceID,
	)
	return err
}

// CreateSession inserts a new session and returns its UUID.
func (d *DB) CreateSession(ctx context.Context, userID, deviceID uuid.UUID, vlessUUID, loginIP, country, city string) (uuid.UUID, error) {
	var id uuid.UUID
	err := d.pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id, device_id, vless_uuid, login_ip, login_country, login_city)
		 VALUES ($1, $2, $3, $4::inet, $5, $6) RETURNING id`,
		userID, deviceID, vlessUUID, loginIP, country, city,
	).Scan(&id)
	return id, err
}

// DeactivateSession marks one session inactive by session UUID.
func (d *DB) DeactivateSession(ctx context.Context, sessionID uuid.UUID) error {
	_, err := d.pool.Exec(ctx,
		"UPDATE sessions SET is_active=false, disconnected_at=NOW() WHERE id=$1",
		sessionID,
	)
	return err
}

// UpdateDeviceLastSeen sets last_seen = NOW() for a device.
func (d *DB) UpdateDeviceLastSeen(ctx context.Context, deviceID uuid.UUID) error {
	_, err := d.pool.Exec(ctx,
		"UPDATE devices SET last_seen=NOW() WHERE id=$1",
		deviceID,
	)
	return err
}

// GetActiveSessionsByUser returns all active sessions for a user (for kick).
type ActiveSessionRow struct {
	ID          uuid.UUID
	DeviceID    uuid.UUID
	VlessUUID   string
}

func (d *DB) GetActiveSessionsByUser(ctx context.Context, userID uuid.UUID) ([]ActiveSessionRow, error) {
	rows, err := d.pool.Query(ctx,
		"SELECT id, device_id, vless_uuid FROM sessions WHERE user_id=$1 AND is_active=true",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActiveSessionRow
	for rows.Next() {
		var r ActiveSessionRow
		if err := rows.Scan(&r.ID, &r.DeviceID, &r.VlessUUID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetDeviceFingerprintByID returns the device_fingerprint for a device ID.
func (d *DB) GetDeviceFingerprintByID(ctx context.Context, deviceID uuid.UUID) (string, error) {
	var fp string
	err := d.pool.QueryRow(ctx,
		"SELECT device_fingerprint FROM devices WHERE id=$1",
		deviceID,
	).Scan(&fp)
	return fp, err
}

// DeactivateUserSessions marks all active sessions for a user as inactive.
func (d *DB) DeactivateUserSessions(ctx context.Context, userID uuid.UUID) error {
	_, err := d.pool.Exec(ctx,
		"UPDATE sessions SET is_active=false, disconnected_at=NOW() WHERE user_id=$1 AND is_active=true",
		userID,
	)
	return err
}

// DeactivateUserDevices marks all devices belonging to a user as inactive (is_active=false).
// Called by KickUserSessions to remove the user's devices from the active device pool,
// ensuring GetAllActiveDeviceUsers excludes them from the sing-box user pool.
// The device UUID is preserved so the user can re-register without losing their identity.
func (d *DB) DeactivateUserDevices(ctx context.Context, userID uuid.UUID) error {
	_, err := d.pool.Exec(ctx,
		"UPDATE devices SET is_active=false WHERE user_id=$1",
		userID,
	)
	return err
}


// UpdateSessionHeartbeat refreshes last_heartbeat_at for an active session.
// Called by the /status endpoint so stale sessions can be detected.
func (d *DB) UpdateSessionHeartbeat(ctx context.Context, sessionID uuid.UUID) {
	_, _ = d.pool.Exec(ctx,
		"UPDATE sessions SET last_heartbeat_at=NOW() WHERE id=$1 AND is_active=true",
		sessionID,
	)
}

// DeactivateStaleSessions marks as inactive any active session whose heartbeat
// has not been updated in the last 5 minutes.
func (d *DB) DeactivateStaleSessions(ctx context.Context) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE sessions SET is_active=false, disconnected_at=NOW()
		 WHERE is_active=true
		   AND last_heartbeat_at < NOW() - INTERVAL '5 minutes'`)
	return err
}

// GetOnlineSessions returns sessions that are active AND have a recent heartbeat.
func (d *DB) GetOnlineSessions(ctx context.Context) ([]models.OnlineSession, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT s.id, u.username, s.login_ip::text, s.login_country, s.login_city,
		        s.connected_at, s.upload_bytes, s.download_bytes
		 FROM sessions s JOIN users u ON s.user_id = u.id
		 WHERE s.is_active = true
		   AND s.last_heartbeat_at > NOW() - INTERVAL '3 minutes'
		 ORDER BY s.connected_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OnlineSession
	for rows.Next() {
		var s models.OnlineSession
		if err := rows.Scan(&s.ID, &s.Username, &s.LoginIP, &s.LoginCountry, &s.LoginCity,
			&s.ConnectedAt, &s.UploadBytes, &s.DownloadBytes); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetTodayTrafficTotals returns sum upload and download bytes for today.
func (d *DB) GetTodayTrafficTotals(ctx context.Context) (upload, download int64, err error) {
	err = d.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(s.upload_bytes),0), COALESCE(SUM(s.download_bytes),0)
		 FROM sessions s
		 WHERE DATE(s.connected_at) = CURRENT_DATE`,
	).Scan(&upload, &download)
	return
}

// CountOnlineSessions returns active sessions with a recent heartbeat.
func (d *DB) CountOnlineSessions(ctx context.Context) (int64, error) {
	var n int64
	err := d.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sessions WHERE is_active = true
		   AND last_heartbeat_at > NOW() - INTERVAL '3 minutes'`,
	).Scan(&n)
	return n, err
}

// GetUsers returns all users with device/online info for the users page.
func (d *DB) GetUsers(ctx context.Context) ([]models.UserRow, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT u.id, u.username, u.is_active, u.created_at,
		        d.last_seen, d.device_name,
		        (SELECT COUNT(*) FROM sessions s2 WHERE s2.user_id=u.id AND s2.is_active=true
		           AND s2.last_heartbeat_at > NOW() - INTERVAL '3 minutes') AS online,
		        u.speed_limit_up_kbps, u.speed_limit_down_kbps, u.quota_bytes, u.quota_period
		 FROM users u
		 LEFT JOIN devices d ON d.user_id=u.id AND d.is_active=true
		 ORDER BY u.created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.UserRow
	for rows.Next() {
		var u models.UserRow
		if err := rows.Scan(&u.ID, &u.Username, &u.IsActive, &u.CreatedAt,
			&u.LastSeen, &u.DeviceName, &u.Online,
			&u.SpeedLimitUpKbps, &u.SpeedLimitDownKbps, &u.QuotaBytes, &u.QuotaPeriod); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUserLimits returns speed limits and quota settings for a user.
func (d *DB) GetUserLimits(ctx context.Context, userID uuid.UUID) (models.UserLimitsRow, error) {
	var r models.UserLimitsRow
	err := d.pool.QueryRow(ctx,
		`SELECT speed_limit_up_kbps, speed_limit_down_kbps, quota_bytes, quota_period
		 FROM users WHERE id=$1`,
		userID,
	).Scan(&r.SpeedLimitUpKbps, &r.SpeedLimitDownKbps, &r.QuotaBytes, &r.QuotaPeriod)
	return r, err
}

// SetUserLimits updates speed limits and quota for a user.
func (d *DB) SetUserLimits(ctx context.Context, userID uuid.UUID, speedUpKbps, speedDownKbps *int, quotaBytes *int64, quotaPeriod *string) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE users SET speed_limit_up_kbps=$1, speed_limit_down_kbps=$2,
		 quota_bytes=$3, quota_period=$4 WHERE id=$5`,
		speedUpKbps, speedDownKbps, quotaBytes, quotaPeriod, userID,
	)
	return err
}

// GetQuotaUsed returns total bytes used and the next reset time for the given period.
// period must be "daily", "weekly", or "monthly".
func (d *DB) GetQuotaUsed(ctx context.Context, userID uuid.UUID, period string) (used int64, resetsAt time.Time, err error) {
	err = d.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT COALESCE(SUM(upload_bytes + download_bytes), 0),
		        CASE WHEN $2 = 'daily'   THEN (CURRENT_DATE + INTERVAL '1 day')::TIMESTAMP
		             WHEN $2 = 'weekly'  THEN (date_trunc('week', CURRENT_TIMESTAMP) + INTERVAL '7 days')::TIMESTAMP
		             WHEN $2 = 'monthly' THEN (date_trunc('month', CURRENT_TIMESTAMP) + INTERVAL '1 month')::TIMESTAMP
		             ELSE NOW()
		        END
		 FROM sessions
		 WHERE user_id = $1 AND connected_at >= %s`, quotaPeriodSQL),
		userID, period,
	).Scan(&used, &resetsAt)
	return
}

// CreateUser inserts a new user. Returns error on duplicate username.
func (d *DB) CreateUser(ctx context.Context, username, passwordHash string, notes *string) error {
	_, err := d.pool.Exec(ctx,
		"INSERT INTO users (username, password_hash, notes) VALUES ($1, $2, $3)",
		username, passwordHash, notes,
	)
	return err
}

// DeleteUser removes a user by UUID.
func (d *DB) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	_, err := d.pool.Exec(ctx,
		"DELETE FROM users WHERE id=$1",
		userID,
	)
	return err
}

// UpdateUserPassword sets a new bcrypt hash for a user.
func (d *DB) UpdateUserPassword(ctx context.Context, userID uuid.UUID, hash string) error {
	_, err := d.pool.Exec(ctx,
		"UPDATE users SET password_hash=$1 WHERE id=$2",
		hash, userID,
	)
	return err
}

// GetUserActive returns the is_active flag for a user.
func (d *DB) GetUserActive(ctx context.Context, userID uuid.UUID) (bool, error) {
	var active bool
	err := d.pool.QueryRow(ctx,
		"SELECT is_active FROM users WHERE id=$1",
		userID,
	).Scan(&active)
	return active, err
}

// SetUserActive enables or disables a user.
func (d *DB) SetUserActive(ctx context.Context, userID uuid.UUID, active bool) error {
	_, err := d.pool.Exec(ctx,
		"UPDATE users SET is_active=$1 WHERE id=$2",
		active, userID,
	)
	return err
}

// GetUsernameByID returns the username for a user ID.
func (d *DB) GetUsernameByID(ctx context.Context, userID uuid.UUID) (string, error) {
	var name string
	err := d.pool.QueryRow(ctx,
		"SELECT username FROM users WHERE id=$1",
		userID,
	).Scan(&name)
	return name, err
}

// GetAllUsers returns a minimal list (id, username) for dropdown selects.
func (d *DB) GetAllUsers(ctx context.Context) ([]models.UserIDRow, error) {
	rows, err := d.pool.Query(ctx,
		"SELECT id, username FROM users ORDER BY username",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.UserIDRow
	for rows.Next() {
		var u models.UserIDRow
		if err := rows.Scan(&u.ID, &u.Username); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetAccessLogs returns access_log rows for a user in a time range (max 1000).
func (d *DB) GetAccessLogs(ctx context.Context, userID uuid.UUID, from, to string) ([]models.AccessLogRow, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT al.host, al.access_hour, al.request_count, al.upload_bytes, al.download_bytes
		 FROM access_log al
		 WHERE al.user_id=$1
		   AND al.access_hour >= $2::timestamp
		   AND al.access_hour < ($3::date + interval '1 day')::timestamp
		 ORDER BY al.access_hour DESC
		 LIMIT 1000`,
		userID, from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.AccessLogRow
	for rows.Next() {
		var r models.AccessLogRow
		if err := rows.Scan(&r.Host, &r.AccessHour, &r.RequestCount, &r.UploadBytes, &r.DownloadBytes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetDailyTraffic returns traffic_daily rows for a user in a date range.
func (d *DB) GetDailyTraffic(ctx context.Context, userID uuid.UUID, from, to string) ([]models.DailyTrafficRow, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT date, upload_bytes, download_bytes
		 FROM traffic_daily
		 WHERE user_id=$1 AND date >= $2::date AND date <= $3::date
		 ORDER BY date DESC`,
		userID, from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.DailyTrafficRow
	for rows.Next() {
		var r models.DailyTrafficRow
		if err := rows.Scan(&r.Date, &r.UploadBytes, &r.DownloadBytes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetTrafficSummary returns aggregate totals for a user in a date range.
func (d *DB) GetTrafficSummary(ctx context.Context, userID uuid.UUID, from, to string) (models.TrafficSummary, error) {
	var s models.TrafficSummary
	err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(upload_bytes),0), COALESCE(SUM(download_bytes),0), COUNT(*)
		 FROM traffic_daily
		 WHERE user_id=$1 AND date >= $2::date AND date <= $3::date`,
		userID, from, to,
	).Scan(&s.Upload, &s.Download, &s.Sessions)
	if err != nil {
		return s, err
	}
	// Also count actual sessions
	var sessions int64
	err = d.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sessions
		 WHERE user_id=$1 AND DATE(connected_at) >= $2::date AND DATE(connected_at) <= $3::date`,
		userID, from, to,
	).Scan(&sessions)
	if err == nil {
		s.Sessions = sessions
	}
	return s, nil
}

// UpsertAccessLog inserts or aggregates an access_log row.
func (d *DB) UpsertAccessLog(ctx context.Context, userID, sessionID uuid.UUID, host string, accessHour time.Time, upload, download int64) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO access_log (user_id, session_id, host, access_hour, upload_bytes, download_bytes)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (session_id, host, access_hour) DO UPDATE SET
		   request_count  = access_log.request_count + 1,
		   upload_bytes   = access_log.upload_bytes + EXCLUDED.upload_bytes,
		   download_bytes = access_log.download_bytes + EXCLUDED.download_bytes`,
		userID, sessionID, host, accessHour, upload, download,
	)
	return err
}

// UpdateSessionTraffic adds delta bytes to a session's running totals.
func (d *DB) UpdateSessionTraffic(ctx context.Context, sessionID uuid.UUID, upload, download int64) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE sessions SET upload_bytes = upload_bytes + $1, download_bytes = download_bytes + $2
		 WHERE id = $3`,
		upload, download, sessionID,
	)
	return err
}

// DeleteOldAccessLogs removes access_log rows older than the cutoff time.
func (d *DB) DeleteOldAccessLogs(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := d.pool.Exec(ctx,
		"DELETE FROM access_log WHERE access_hour < $1",
		cutoff,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// UpsertTrafficDaily aggregates yesterday's sessions into traffic_daily.
func (d *DB) UpsertTrafficDaily(ctx context.Context, date time.Time) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO traffic_daily (user_id, date, upload_bytes, download_bytes)
		 SELECT user_id, $1::date,
		        COALESCE(SUM(upload_bytes), 0),
		        COALESCE(SUM(download_bytes), 0)
		 FROM sessions
		 WHERE DATE(connected_at) = $1
		 GROUP BY user_id
		 ON CONFLICT (user_id, date) DO UPDATE SET
		   upload_bytes   = EXCLUDED.upload_bytes,
		   download_bytes = EXCLUDED.download_bytes`,
		date,
	)
	return err
}

// CapAccessLog removes excess access_log rows per user per day, keeping top maxDomains by traffic.
func (d *DB) CapAccessLog(ctx context.Context, cutoff time.Time, maxDomains int) error {
	_, err := d.pool.Exec(ctx,
		`DELETE FROM access_log
		 WHERE id IN (
		   SELECT id FROM (
		     SELECT id,
		            ROW_NUMBER() OVER (
		              PARTITION BY user_id, DATE(access_hour)
		              ORDER BY download_bytes DESC, upload_bytes DESC
		            ) AS rn
		     FROM access_log
		     WHERE access_hour >= $1
		   ) ranked
		   WHERE rn > $2
		 )`,
		cutoff, maxDomains,
	)
	return err
}
