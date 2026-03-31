package store

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/redis/rueidis"
)

type Redis struct {
	client rueidis.Client
}

// parseRedisURL converts a redis:// URL to rueidis.ClientOption.
// rueidis does not accept URL strings directly.
func parseRedisURL(redisURL string) (rueidis.ClientOption, error) {
	u, err := url.Parse(redisURL)
	if err != nil {
		return rueidis.ClientOption{}, fmt.Errorf("redis: parse url: %w", err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "6379"
	}
	db := 0
	if len(u.Path) > 1 {
		db, err = strconv.Atoi(u.Path[1:])
		if err != nil {
			return rueidis.ClientOption{}, fmt.Errorf("redis: invalid db in url: %w", err)
		}
	}
	opt := rueidis.ClientOption{
		InitAddress:  []string{host + ":" + port},
		SelectDB:     db,
		DisableCache: true,
	}
	if u.User != nil {
		if p, ok := u.User.Password(); ok {
			opt.Password = p
		}
	}
	return opt, nil
}

func NewRedis(ctx context.Context, redisURL string) (*Redis, error) {
	opt, err := parseRedisURL(redisURL)
	if err != nil {
		return nil, err
	}
	client, err := rueidis.NewClient(opt)
	if err != nil {
		return nil, fmt.Errorf("redis: connect: %w", err)
	}
	// Ping to verify connection
	if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
		return nil, fmt.Errorf("redis: ping failed: %w", err)
	}
	return &Redis{client: client}, nil
}

func MustNewRedis(ctx context.Context, redisURL string) *Redis {
	r, err := NewRedis(ctx, redisURL)
	if err != nil {
		panic(err)
	}
	return r
}

func (r *Redis) Close() {
	r.client.Close()
}

func (r *Redis) Client() rueidis.Client {
	return r.client
}

// IncrRateLimit increments the failed-auth counter for an IP. Sets TTL on first hit.
// Should only be called on credential failure (wrong password).
func (r *Redis) IncrRateLimit(ctx context.Context, ip string) (int64, error) {
	key := "rate:" + ip + ":auth"
	resp := r.client.Do(ctx, r.client.B().Incr().Key(key).Build())
	count, err := resp.AsInt64()
	if err != nil {
		return 0, err
	}
	if count == 1 {
		r.client.Do(ctx, r.client.B().Expire().Key(key).Seconds(900).Build())
	}
	return count, nil
}

// GetRateLimit returns the current failed-auth count for an IP without incrementing.
func (r *Redis) GetRateLimit(ctx context.Context, ip string) (int64, error) {
	key := "rate:" + ip + ":auth"
	resp := r.client.Do(ctx, r.client.B().Get().Key(key).Build())
	if resp.Error() != nil {
		return 0, nil // key doesn't exist → count is 0
	}
	return resp.AsInt64()
}

// GetActiveSession returns the raw JSON stored at active_session:{fingerprint}.
func (r *Redis) GetActiveSession(ctx context.Context, fingerprint string) (string, error) {
	resp := r.client.Do(ctx, r.client.B().Get().Key("active_session:"+fingerprint).Build())
	if rueidis.IsRedisNil(resp.Error()) {
		return "", nil
	}
	return resp.ToString()
}

// SetSession pipelines SET active_session + SET vless_map (no TTL).
func (r *Redis) SetSession(ctx context.Context, fingerprint, vlessUUID, jsonData string) error {
	cmds := []rueidis.Completed{
		r.client.B().Set().Key("active_session:" + fingerprint).Value(jsonData).Build(),
		r.client.B().Set().Key("vless_map:" + vlessUUID).Value(jsonData).Build(),
	}
	for _, resp := range r.client.DoMulti(ctx, cmds...) {
		if err := resp.Error(); err != nil {
			return err
		}
	}
	return nil
}

// DeleteSession removes active_session and vless_map keys.
func (r *Redis) DeleteSession(ctx context.Context, fingerprint, vlessUUID string) error {
	cmds := []rueidis.Completed{
		r.client.B().Del().Key("active_session:" + fingerprint).Build(),
		r.client.B().Del().Key("vless_map:" + vlessUUID).Build(),
	}
	for _, resp := range r.client.DoMulti(ctx, cmds...) {
		if err := resp.Error(); err != nil {
			return err
		}
	}
	return nil
}

// DeleteKey removes a single key.
func (r *Redis) DeleteKey(ctx context.Context, key string) error {
	return r.client.Do(ctx, r.client.B().Del().Key(key).Build()).Error()
}

// GetVlessMap returns the session JSON for a vless UUID.
func (r *Redis) GetVlessMap(ctx context.Context, vlessUUID string) (string, error) {
	resp := r.client.Do(ctx, r.client.B().Get().Key("vless_map:"+vlessUUID).Build())
	if rueidis.IsRedisNil(resp.Error()) {
		return "", nil
	}
	return resp.ToString()
}

// SetRefreshToken stores session JSON under refresh:{token} with a TTL.
func (r *Redis) SetRefreshToken(ctx context.Context, token, jsonData string, ttl time.Duration) error {
	return r.client.Do(ctx,
		r.client.B().Setex().Key("refresh:"+token).Seconds(int64(ttl.Seconds())).Value(jsonData).Build(),
	).Error()
}

// GetRefreshToken returns the session JSON for a refresh token.
func (r *Redis) GetRefreshToken(ctx context.Context, token string) (string, error) {
	resp := r.client.Do(ctx, r.client.B().Get().Key("refresh:"+token).Build())
	if rueidis.IsRedisNil(resp.Error()) {
		return "", nil
	}
	return resp.ToString()
}

// SetVerifCode stores user_id under verif:{code} with 15-minute TTL.
func (r *Redis) SetVerifCode(ctx context.Context, code, userID string) error {
	return r.client.Do(ctx,
		r.client.B().Setex().Key("verif:"+code).Seconds(900).Value(userID).Build(),
	).Error()
}

// GetVerifCode returns the user_id stored for a verification code.
func (r *Redis) GetVerifCode(ctx context.Context, code string) (string, error) {
	resp := r.client.Do(ctx, r.client.B().Get().Key("verif:"+code).Build())
	if rueidis.IsRedisNil(resp.Error()) {
		return "", nil
	}
	return resp.ToString()
}

// TTL returns the remaining TTL for a key in seconds (-2 if not exists, -1 if no TTL).
func (r *Redis) TTL(ctx context.Context, key string) (int64, error) {
	return r.client.Do(ctx, r.client.B().Ttl().Key(key).Build()).AsInt64()
}

// FlushDB removes all keys in the current database (for testing only).
func (r *Redis) FlushDB(ctx context.Context) error {
	return r.client.Do(ctx, r.client.B().Flushdb().Build()).Error()
}

// SetPolicyChanged marks that a user's policy was recently changed by admin.
// The flag expires after 5 minutes so clients that are offline won't see stale flags.
func (r *Redis) SetPolicyChanged(ctx context.Context, userID string) {
	r.client.Do(ctx, r.client.B().Setex().Key("policy_changed:"+userID).Seconds(300).Value("1").Build())
}

// GetAndDeletePolicyChanged returns true and removes the flag if it exists.
func (r *Redis) GetAndDeletePolicyChanged(ctx context.Context, userID string) bool {
	key := "policy_changed:" + userID
	resp := r.client.Do(ctx, r.client.B().Getdel().Key(key).Build())
	val, err := resp.ToString()
	return err == nil && val == "1"
}
