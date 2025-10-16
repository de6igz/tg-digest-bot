package repo

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/metrics"
)

// Postgres реализует репозитории на основе pgxpool.
type Postgres struct {
	pool *pgxpool.Pool
}

var _ domain.BusinessMetricRepo = (*Postgres)(nil)

const (
	referralAlphabet   = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	referralCodeLength = 8
	referralRetryMax   = 5
)

// NewPostgres создаёт адаптер БД.
func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func generateReferralCode() (string, error) {
	buf := make([]byte, referralCodeLength)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(referralCodeLength)
	for _, raw := range buf {
		idx := int(raw) % len(referralAlphabet)
		b.WriteByte(referralAlphabet[idx])
	}
	return b.String(), nil
}

func (p *Postgres) connCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func (p *Postgres) connCtxWithParent(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return p.connCtx()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, 5*time.Second)
}

func (p *Postgres) saveBusinessMetric(ctx context.Context, metric domain.BusinessMetric) error {
	if metric.Event == "" {
		return nil
	}

	if metric.OccurredAt.IsZero() {
		metric.OccurredAt = time.Now().UTC()
	}

	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	var userID sql.NullInt64
	if metric.UserID != nil {
		userID = sql.NullInt64{Int64: *metric.UserID, Valid: true}
	}

	var channelID sql.NullInt64
	if metric.ChannelID != nil {
		channelID = sql.NullInt64{Int64: *metric.ChannelID, Valid: true}
	}

	var payload []byte
	if metric.Metadata != nil {
		if data, err := json.Marshal(metric.Metadata); err == nil {
			payload = data
		}
	}

	start := time.Now()
	_, err := p.pool.Exec(ctx, `
INSERT INTO business_metrics (event, user_id, channel_id, metadata, occurred_at)
VALUES ($1, $2, $3, $4, $5)
`, metric.Event, userID, channelID, payload, metric.OccurredAt)
	metrics.ObserveNetworkRequest("postgres", "business_metrics_insert", "business_metrics", start, err)
	return err
}

// RecordBusinessMetric сохраняет бизнесовую метрику в БД.
func (p *Postgres) RecordBusinessMetric(ctx context.Context, metric domain.BusinessMetric) error {
	return p.saveBusinessMetric(ctx, metric)
}

// UpsertByTGID реализует domain.UserRepo.
func (p *Postgres) UpsertByTGID(profile domain.TelegramProfile) (domain.User, bool, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	locale := strings.TrimSpace(profile.Locale)
	timezone := strings.TrimSpace(profile.Timezone)
	firstNameValue := strings.TrimSpace(profile.FirstName)
	lastNameValue := strings.TrimSpace(profile.LastName)
	usernameValue := strings.TrimSpace(profile.Username)

	for attempt := 0; attempt < referralRetryMax; attempt++ {
		code, err := generateReferralCode()
		if err != nil {
			return domain.User{}, false, err
		}

		start := time.Now()
		tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
		metrics.ObserveNetworkRequest("postgres", "begin_tx", "users", start, err)
		if err != nil {
			return domain.User{}, false, err
		}

		var (
			user         domain.User
			manualDate   sql.NullTime
			referredBy   sql.NullInt64
			tzValue      sql.NullString
			firstNameSQL sql.NullString
			lastNameSQL  sql.NullString
			usernameSQL  sql.NullString
			created      bool
		)
		start = time.Now()
		err = tx.QueryRow(ctx, `
INSERT INTO users (tg_user_id, locale, tz, first_name, last_name, username, is_bot, referral_code)
VALUES ($1, COALESCE(NULLIF($2,''),'ru-RU'), NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), NULLIF($6,''), $7, $8)
ON CONFLICT (tg_user_id) DO UPDATE SET locale = EXCLUDED.locale, tz = COALESCE(EXCLUDED.tz, users.tz), first_name = EXCLUDED.first_name, last_name = EXCLUDED.last_name, username = EXCLUDED.username, is_bot = EXCLUDED.is_bot, updated_at = now()
RETURNING id, tg_user_id, locale, tz, daily_time, created_at, updated_at, role, manual_requests_total, manual_requests_today, manual_requests_date, referral_code, referrals_count, referred_by, first_name, last_name, username, is_bot, (xmax = 0) AS inserted
`, profile.TGUserID, locale, timezone, firstNameValue, lastNameValue, usernameValue, profile.IsBot, code).Scan(&user.ID, &user.TGUserID, &user.Locale, &tzValue, &user.DailyTime, &user.CreatedAt, &user.UpdatedAt, &user.Role, &user.ManualRequestsTotal, &user.ManualRequestsToday, &manualDate, &user.ReferralCode, &user.ReferralsCount, &referredBy, &firstNameSQL, &lastNameSQL, &usernameSQL, &user.IsBot, &created)
		metrics.ObserveNetworkRequest("postgres", "users_upsert", "users", start, err)
		if err != nil {
			_ = tx.Rollback(ctx)
			if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" && pgErr.ConstraintName == "users_referral_code_key" {
				continue
			}
			return domain.User{}, false, err
		}
		if manualDate.Valid {
			ts := manualDate.Time
			user.ManualRequestsDate = &ts
		}
		if referredBy.Valid {
			id := referredBy.Int64
			user.ReferredByID = &id
		}
		if tzValue.Valid {
			user.Timezone = tzValue.String
		}
		if firstNameSQL.Valid {
			user.FirstName = firstNameSQL.String
		}
		if lastNameSQL.Valid {
			user.LastName = lastNameSQL.String
		}
		if usernameSQL.Valid {
			user.Username = usernameSQL.String
		}
		start = time.Now()
		err = tx.Commit(ctx)
		metrics.ObserveNetworkRequest("postgres", "commit", "users", start, err)
		if err != nil {
			return domain.User{}, false, err
		}
		if created {
			userID := user.ID
			meta := map[string]any{
				"tg_user_id": user.TGUserID,
				"locale":     user.Locale,
			}
			if user.Timezone != "" {
				meta["timezone"] = user.Timezone
			}
			if user.ReferralCode != "" {
				meta["referral_code"] = user.ReferralCode
			}
			if user.ReferredByID != nil {
				meta["referred_by"] = *user.ReferredByID
			}
			_ = p.saveBusinessMetric(ctx, domain.BusinessMetric{
				Event:    domain.BusinessMetricEventUserRegistered,
				UserID:   &userID,
				Metadata: meta,
			})
		}
		return user, created, nil
	}
	return domain.User{}, false, fmt.Errorf("could not generate unique referral code")
}

// GetByTGID возвращает пользователя по Telegram ID.
func (p *Postgres) GetByTGID(tgUserID int64) (domain.User, error) {
	var user domain.User
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	var (
		manualDate sql.NullTime
		referredBy sql.NullInt64
		tzValue    sql.NullString
		firstName  sql.NullString
		lastName   sql.NullString
		username   sql.NullString
	)
	err := p.pool.QueryRow(ctx, `
SELECT id, tg_user_id, locale, tz, daily_time, created_at, updated_at, role, manual_requests_total, manual_requests_today, manual_requests_date, referral_code, referrals_count, referred_by, first_name, last_name, username, is_bot
FROM users WHERE tg_user_id=$1
`, tgUserID).Scan(&user.ID, &user.TGUserID, &user.Locale, &tzValue, &user.DailyTime, &user.CreatedAt, &user.UpdatedAt, &user.Role, &user.ManualRequestsTotal, &user.ManualRequestsToday, &manualDate, &user.ReferralCode, &user.ReferralsCount, &referredBy, &firstName, &lastName, &username, &user.IsBot)
	metrics.ObserveNetworkRequest("postgres", "users_get_by_tgid", "users", start, err)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, fmt.Errorf("user not found")
	}
	if manualDate.Valid {
		ts := manualDate.Time
		user.ManualRequestsDate = &ts
	}
	if referredBy.Valid {
		id := referredBy.Int64
		user.ReferredByID = &id
	}
	if tzValue.Valid {
		user.Timezone = tzValue.String
	}
	if firstName.Valid {
		user.FirstName = firstName.String
	}
	if lastName.Valid {
		user.LastName = lastName.String
	}
	if username.Valid {
		user.Username = username.String
	}
	return user, err
}

// ListForDailyTime возвращает пользователей с настроенным временем доставки.
// Параметр now сохраняется для совместимости интерфейса и может использоваться в будущем для оптимизации выборки.
func (p *Postgres) ListForDailyTime(now time.Time) ([]domain.User, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	rows, err := p.pool.Query(ctx, `
SELECT id, tg_user_id, locale, tz, daily_time, created_at, updated_at, role, manual_requests_total, manual_requests_today, manual_requests_date, referral_code, referrals_count, referred_by, first_name, last_name, username, is_bot
FROM users WHERE daily_time IS NOT NULL
`)
	metrics.ObserveNetworkRequest("postgres", "users_list_for_daily_time", "users", start, err)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []domain.User
	for rows.Next() {
		var u domain.User
		var (
			manualDate sql.NullTime
			referredBy sql.NullInt64
			tzValue    sql.NullString
			firstName  sql.NullString
			lastName   sql.NullString
			username   sql.NullString
		)
		if err := rows.Scan(&u.ID, &u.TGUserID, &u.Locale, &tzValue, &u.DailyTime, &u.CreatedAt, &u.UpdatedAt, &u.Role, &u.ManualRequestsTotal, &u.ManualRequestsToday, &manualDate, &u.ReferralCode, &u.ReferralsCount, &referredBy, &firstName, &lastName, &username, &u.IsBot); err != nil {
			return nil, err
		}
		if manualDate.Valid {
			ts := manualDate.Time
			u.ManualRequestsDate = &ts
		}
		if referredBy.Valid {
			id := referredBy.Int64
			u.ReferredByID = &id
		}
		if tzValue.Valid {
			u.Timezone = tzValue.String
		}
		if firstName.Valid {
			u.FirstName = firstName.String
		}
		if lastName.Valid {
			u.LastName = lastName.String
		}
		if username.Valid {
			u.Username = username.String
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// AcquireScheduleTask вставляет запись о поставленной задаче и возвращает true, если удалось.
func (p *Postgres) AcquireScheduleTask(userID int64, scheduledFor time.Time) (bool, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	res, err := p.pool.Exec(ctx, `
INSERT INTO schedule_tasks (user_id, scheduled_for)
VALUES ($1, $2)
ON CONFLICT (user_id, scheduled_for) DO NOTHING
`, userID, scheduledFor)
	metrics.ObserveNetworkRequest("postgres", "schedule_tasks_acquire", "schedule_tasks", start, err)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() > 0, nil
}

// UpdateDailyTime обновляет время.
func (p *Postgres) UpdateDailyTime(userID int64, daily time.Time) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	_, err := p.pool.Exec(ctx, `UPDATE users SET daily_time=$2, updated_at=now() WHERE id=$1`, userID, daily.Format("15:04:05"))
	metrics.ObserveNetworkRequest("postgres", "users_update_daily_time", "users", start, err)
	return err
}

// UpdateTimezone обновляет часовой пояс пользователя.
func (p *Postgres) UpdateTimezone(userID int64, timezone string) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	var tzArg any
	if strings.TrimSpace(timezone) == "" {
		tzArg = nil
	} else {
		tzArg = timezone
	}

	start := time.Now()
	_, err := p.pool.Exec(ctx, `UPDATE users SET tz=$2, updated_at=now() WHERE id=$1`, userID, tzArg)
	metrics.ObserveNetworkRequest("postgres", "users_update_timezone", "users", start, err)
	return err
}

// DeleteUserData удаляет данные пользователя.
func (p *Postgres) DeleteUserData(userID int64) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	_, err := p.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	metrics.ObserveNetworkRequest("postgres", "users_delete", "users", start, err)
	return err
}

// ReserveManualRequest резервирует ручной запрос для пользователя при наличии лимита.
func (p *Postgres) ReserveManualRequest(userID int64, now time.Time) (domain.ManualRequestState, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	metrics.ObserveNetworkRequest("postgres", "begin_tx", "users", start, err)
	if err != nil {
		return domain.ManualRequestState{}, err
	}
	defer tx.Rollback(ctx)

	var (
		user       domain.User
		manualDate sql.NullTime
		tzValue    sql.NullString
	)

	start = time.Now()
	err = tx.QueryRow(ctx, `
SELECT id, tg_user_id, locale, tz, daily_time, created_at, updated_at, role, manual_requests_total, manual_requests_today, manual_requests_date
FROM users WHERE id=$1 FOR UPDATE
`, userID).Scan(&user.ID, &user.TGUserID, &user.Locale, &tzValue, &user.DailyTime, &user.CreatedAt, &user.UpdatedAt, &user.Role, &user.ManualRequestsTotal, &user.ManualRequestsToday, &manualDate)
	metrics.ObserveNetworkRequest("postgres", "users_get_for_update", "users", start, err)
	if err != nil {
		return domain.ManualRequestState{}, err
	}
	if manualDate.Valid {
		ts := manualDate.Time
		user.ManualRequestsDate = &ts
	}
	if tzValue.Valid {
		user.Timezone = tzValue.String
	}

	plan := user.Plan()
	state := domain.ManualRequestState{
		Plan:      plan,
		TotalUsed: user.ManualRequestsTotal,
		UsedToday: user.ManualRequestsToday,
	}

	today := now.UTC().Truncate(24 * time.Hour)
	usedToday := user.ManualRequestsToday
	if user.ManualRequestsDate == nil || !sameDay(*user.ManualRequestsDate, today) {
		usedToday = 0
	}
	state.UsedToday = usedToday

	allowed := false
	newTotal := user.ManualRequestsTotal
	newToday := usedToday

	switch {
	case plan.ManualDailyLimit <= 0:
		allowed = true
		newTotal++
		newToday = 0
	case plan.ManualIntroTotal > 0 && user.ManualRequestsTotal < plan.ManualIntroTotal:
		allowed = true
		newTotal++
		newToday = usedToday + 1
	case usedToday < plan.ManualDailyLimit:
		allowed = true
		newTotal++
		newToday = usedToday + 1
	}

	if !allowed {
		return state, nil
	}

	state.Allowed = true
	state.TotalUsed = newTotal
	state.UsedToday = newToday

	var dateArg any
	if plan.ManualDailyLimit <= 0 {
		dateArg = nil
	} else {
		dateArg = today
	}

	start = time.Now()
	_, err = tx.Exec(ctx, `
UPDATE users
SET manual_requests_total=$2, manual_requests_today=$3, manual_requests_date=$4, updated_at=now()
WHERE id=$1
`, userID, newTotal, newToday, dateArg)
	metrics.ObserveNetworkRequest("postgres", "users_update_manual_requests", "users", start, err)
	if err != nil {
		return domain.ManualRequestState{}, err
	}

	start = time.Now()
	err = tx.Commit(ctx)
	metrics.ObserveNetworkRequest("postgres", "commit", "users", start, err)
	if err != nil {
		return domain.ManualRequestState{}, err
	}

	return state, nil
}

// ApplyReferral закрепляет реферала за пользователем и обновляет награды.
func (p *Postgres) ApplyReferral(code string, newUserID int64) (domain.ReferralResult, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	metrics.ObserveNetworkRequest("postgres", "begin_tx", "users", start, err)
	if err != nil {
		return domain.ReferralResult{}, err
	}
	defer tx.Rollback(ctx)

	var (
		user       domain.User
		manualDate sql.NullTime
		referredBy sql.NullInt64
		tzValue    sql.NullString
		firstName  sql.NullString
		lastName   sql.NullString
		username   sql.NullString
	)

	start = time.Now()
	err = tx.QueryRow(ctx, `
SELECT id, tg_user_id, locale, tz, daily_time, created_at, updated_at, role, manual_requests_total, manual_requests_today, manual_requests_date, referral_code, referrals_count, referred_by, first_name, last_name, username, is_bot
FROM users WHERE id=$1 FOR UPDATE
`, newUserID).Scan(&user.ID, &user.TGUserID, &user.Locale, &tzValue, &user.DailyTime, &user.CreatedAt, &user.UpdatedAt, &user.Role, &user.ManualRequestsTotal, &user.ManualRequestsToday, &manualDate, &user.ReferralCode, &user.ReferralsCount, &referredBy, &firstName, &lastName, &username, &user.IsBot)
	metrics.ObserveNetworkRequest("postgres", "users_get_for_update", "users", start, err)
	if err != nil {
		return domain.ReferralResult{}, err
	}
	if manualDate.Valid {
		ts := manualDate.Time
		user.ManualRequestsDate = &ts
	}
	if referredBy.Valid {
		id := referredBy.Int64
		user.ReferredByID = &id
	}
	if tzValue.Valid {
		user.Timezone = tzValue.String
	}
	if firstName.Valid {
		user.FirstName = firstName.String
	}
	if lastName.Valid {
		user.LastName = lastName.String
	}
	if username.Valid {
		user.Username = username.String
	}

	normalized := strings.ToUpper(strings.TrimSpace(code))
	if normalized == "" || user.ReferredByID != nil {
		start = time.Now()
		err = tx.Commit(ctx)
		metrics.ObserveNetworkRequest("postgres", "commit", "users", start, err)
		if err != nil {
			return domain.ReferralResult{}, err
		}
		return domain.ReferralResult{User: user}, nil
	}

	var (
		referrer      domain.User
		refManualDate sql.NullTime
		refReferredBy sql.NullInt64
		refTZ         sql.NullString
		refFirstName  sql.NullString
		refLastName   sql.NullString
		refUsername   sql.NullString
	)

	start = time.Now()
	err = tx.QueryRow(ctx, `
SELECT id, tg_user_id, locale, tz, daily_time, created_at, updated_at, role, manual_requests_total, manual_requests_today, manual_requests_date, referral_code, referrals_count, referred_by, first_name, last_name, username, is_bot
FROM users WHERE referral_code=$1 FOR UPDATE
`, normalized).Scan(&referrer.ID, &referrer.TGUserID, &referrer.Locale, &refTZ, &referrer.DailyTime, &referrer.CreatedAt, &referrer.UpdatedAt, &referrer.Role, &referrer.ManualRequestsTotal, &referrer.ManualRequestsToday, &refManualDate, &referrer.ReferralCode, &referrer.ReferralsCount, &refReferredBy, &refFirstName, &refLastName, &refUsername, &referrer.IsBot)
	metrics.ObserveNetworkRequest("postgres", "users_get_by_ref_code", "users", start, err)
	if errors.Is(err, pgx.ErrNoRows) {
		start = time.Now()
		err = tx.Commit(ctx)
		metrics.ObserveNetworkRequest("postgres", "commit", "users", start, err)
		if err != nil {
			return domain.ReferralResult{}, err
		}
		return domain.ReferralResult{User: user}, nil
	}
	if err != nil {
		return domain.ReferralResult{}, err
	}
	if refManualDate.Valid {
		ts := refManualDate.Time
		referrer.ManualRequestsDate = &ts
	}
	if refReferredBy.Valid {
		id := refReferredBy.Int64
		referrer.ReferredByID = &id
	}
	if refTZ.Valid {
		referrer.Timezone = refTZ.String
	}
	if refFirstName.Valid {
		referrer.FirstName = refFirstName.String
	}
	if refLastName.Valid {
		referrer.LastName = refLastName.String
	}
	if refUsername.Valid {
		referrer.Username = refUsername.String
	}
	if referrer.ID == user.ID {
		start = time.Now()
		err = tx.Commit(ctx)
		metrics.ObserveNetworkRequest("postgres", "commit", "users", start, err)
		if err != nil {
			return domain.ReferralResult{}, err
		}
		return domain.ReferralResult{User: user}, nil
	}

	start = time.Now()
	res, err := tx.Exec(ctx, `UPDATE users SET referred_by=$2, updated_at=now() WHERE id=$1 AND referred_by IS NULL`, user.ID, referrer.ID)
	metrics.ObserveNetworkRequest("postgres", "users_apply_referral", "users", start, err)
	if err != nil {
		return domain.ReferralResult{}, err
	}
	if res.RowsAffected() == 0 {
		start = time.Now()
		err = tx.Commit(ctx)
		metrics.ObserveNetworkRequest("postgres", "commit", "users", start, err)
		if err != nil {
			return domain.ReferralResult{}, err
		}
		return domain.ReferralResult{User: user}, nil
	}

	newCount := referrer.ReferralsCount + 1
	newRole := domain.RoleForReferralProgress(referrer.Role, newCount)
	previousRole := referrer.Role
	upgraded := newRole != referrer.Role
	if upgraded {
		start = time.Now()
		_, err = tx.Exec(ctx, `UPDATE users SET referrals_count=$2, role=$3, updated_at=now() WHERE id=$1`, referrer.ID, newCount, newRole)
		metrics.ObserveNetworkRequest("postgres", "users_update_referrer_with_role", "users", start, err)
	} else {
		start = time.Now()
		_, err = tx.Exec(ctx, `UPDATE users SET referrals_count=$2, updated_at=now() WHERE id=$1`, referrer.ID, newCount)
		metrics.ObserveNetworkRequest("postgres", "users_update_referrer", "users", start, err)
	}
	if err != nil {
		return domain.ReferralResult{}, err
	}

	start = time.Now()
	err = tx.QueryRow(ctx, `
SELECT id, tg_user_id, locale, tz, daily_time, created_at, updated_at, role, manual_requests_total, manual_requests_today, manual_requests_date, referral_code, referrals_count, referred_by, first_name, last_name, username, is_bot
FROM users WHERE id=$1
`, user.ID).Scan(&user.ID, &user.TGUserID, &user.Locale, &tzValue, &user.DailyTime, &user.CreatedAt, &user.UpdatedAt, &user.Role, &user.ManualRequestsTotal, &user.ManualRequestsToday, &manualDate, &user.ReferralCode, &user.ReferralsCount, &referredBy, &firstName, &lastName, &username, &user.IsBot)
	metrics.ObserveNetworkRequest("postgres", "users_get_after_referral", "users", start, err)
	if err != nil {
		return domain.ReferralResult{}, err
	}
	if manualDate.Valid {
		ts := manualDate.Time
		user.ManualRequestsDate = &ts
	} else {
		user.ManualRequestsDate = nil
	}
	if referredBy.Valid {
		id := referredBy.Int64
		user.ReferredByID = &id
	} else {
		user.ReferredByID = nil
	}
	if tzValue.Valid {
		user.Timezone = tzValue.String
	} else {
		user.Timezone = ""
	}
	if firstName.Valid {
		user.FirstName = firstName.String
	} else {
		user.FirstName = ""
	}
	if lastName.Valid {
		user.LastName = lastName.String
	} else {
		user.LastName = ""
	}
	if username.Valid {
		user.Username = username.String
	} else {
		user.Username = ""
	}

	start = time.Now()
	err = tx.QueryRow(ctx, `
SELECT id, tg_user_id, locale, tz, daily_time, created_at, updated_at, role, manual_requests_total, manual_requests_today, manual_requests_date, referral_code, referrals_count, referred_by, first_name, last_name, username, is_bot
FROM users WHERE id=$1
`, referrer.ID).Scan(&referrer.ID, &referrer.TGUserID, &referrer.Locale, &refTZ, &referrer.DailyTime, &referrer.CreatedAt, &referrer.UpdatedAt, &referrer.Role, &referrer.ManualRequestsTotal, &referrer.ManualRequestsToday, &refManualDate, &referrer.ReferralCode, &referrer.ReferralsCount, &refReferredBy, &refFirstName, &refLastName, &refUsername, &referrer.IsBot)
	metrics.ObserveNetworkRequest("postgres", "users_get_referrer_after_update", "users", start, err)
	if err != nil {
		return domain.ReferralResult{}, err
	}
	if refManualDate.Valid {
		ts := refManualDate.Time
		referrer.ManualRequestsDate = &ts
	} else {
		referrer.ManualRequestsDate = nil
	}
	if refReferredBy.Valid {
		id := refReferredBy.Int64
		referrer.ReferredByID = &id
	} else {
		referrer.ReferredByID = nil
	}
	if refTZ.Valid {
		referrer.Timezone = refTZ.String
	} else {
		referrer.Timezone = ""
	}
	if refFirstName.Valid {
		referrer.FirstName = refFirstName.String
	} else {
		referrer.FirstName = ""
	}
	if refLastName.Valid {
		referrer.LastName = refLastName.String
	} else {
		referrer.LastName = ""
	}
	if refUsername.Valid {
		referrer.Username = refUsername.String
	} else {
		referrer.Username = ""
	}

	start = time.Now()
	err = tx.Commit(ctx)
	metrics.ObserveNetworkRequest("postgres", "commit", "users", start, err)
	if err != nil {
		return domain.ReferralResult{}, err
	}

	result := domain.ReferralResult{
		User:     user,
		Applied:  true,
		Referrer: &referrer,
	}
	if upgraded {
		result.ReferrerUpgraded = true
		result.PreviousRole = previousRole
	}
	return result, nil
}

// UpdateRole обновляет тариф пользователя.
func (p *Postgres) UpdateRole(userID int64, role domain.UserRole) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	_, err := p.pool.Exec(ctx, `UPDATE users SET role=$2, updated_at=now() WHERE id=$1`, userID, role)
	metrics.ObserveNetworkRequest("postgres", "users_update_role", "users", start, err)
	return err
}

func sameDay(a, b time.Time) bool {
	a = a.UTC()
	b = b.UTC()
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// UpsertChannel сохраняет канал.
func (p *Postgres) UpsertChannel(meta domain.ChannelMeta) (domain.Channel, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	var ch domain.Channel
	start := time.Now()
	err := p.pool.QueryRow(ctx, `
INSERT INTO channels (tg_channel_id, alias, title, is_allowed)
VALUES ($1,$2,$3,true)
ON CONFLICT(alias) DO UPDATE SET tg_channel_id=EXCLUDED.tg_channel_id, title=EXCLUDED.title, is_allowed=true
RETURNING id, tg_channel_id, alias, title, is_allowed, created_at
`, meta.ID, meta.Alias, meta.Title).Scan(&ch.ID, &ch.TGChannelID, &ch.Alias, &ch.Title, &ch.IsAllowed, &ch.CreatedAt)
	metrics.ObserveNetworkRequest("postgres", "channels_upsert", "channels", start, err)
	return ch, err
}

// ListUserChannels возвращает каналы пользователя.
func (p *Postgres) ListUserChannels(userID int64, limit, offset int) ([]domain.UserChannel, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	rows, err := p.pool.Query(ctx, `
SELECT uc.id, uc.user_id, uc.channel_id, uc.muted, uc.added_at, uc.tags,
       c.id, c.tg_channel_id, c.alias, c.title, c.is_allowed, c.created_at
FROM user_channels uc JOIN channels c ON c.id = uc.channel_id
WHERE uc.user_id=$1
ORDER BY uc.added_at DESC
LIMIT $2 OFFSET $3
`, userID, limit, offset)
	metrics.ObserveNetworkRequest("postgres", "user_channels_list", "user_channels", start, err)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []domain.UserChannel
	for rows.Next() {
		var uc domain.UserChannel
		if err := rows.Scan(&uc.ID, &uc.UserID, &uc.ChannelID, &uc.Muted, &uc.AddedAt, &uc.Tags,
			&uc.Channel.ID, &uc.Channel.TGChannelID, &uc.Channel.Alias, &uc.Channel.Title, &uc.Channel.IsAllowed, &uc.Channel.CreatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, uc)
	}
	return channels, rows.Err()
}

// AttachChannelToUser привязывает канал к пользователю.
func (p *Postgres) AttachChannelToUser(userID, channelID int64) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	tag, err := p.pool.Exec(ctx, `
INSERT INTO user_channels (user_id, channel_id)
VALUES ($1,$2)
ON CONFLICT (user_id, channel_id) DO NOTHING
`, userID, channelID)
	metrics.ObserveNetworkRequest("postgres", "user_channels_attach", "user_channels", start, err)
	if err == nil && tag.RowsAffected() > 0 {
		uID := userID
		chID := channelID
		_ = p.saveBusinessMetric(ctx, domain.BusinessMetric{
			Event:     domain.BusinessMetricEventChannelAttached,
			UserID:    &uID,
			ChannelID: &chID,
		})
	}
	return err
}

// DetachChannelFromUser удаляет канал.
func (p *Postgres) DetachChannelFromUser(userID, channelID int64) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	_, err := p.pool.Exec(ctx, `DELETE FROM user_channels WHERE user_id=$1 AND channel_id=$2`, userID, channelID)
	metrics.ObserveNetworkRequest("postgres", "user_channels_detach", "user_channels", start, err)
	return err
}

// SetMuted переключает mute.
func (p *Postgres) SetMuted(userID, channelID int64, muted bool) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	_, err := p.pool.Exec(ctx, `UPDATE user_channels SET muted=$3 WHERE user_id=$1 AND channel_id=$2`, userID, channelID, muted)
	metrics.ObserveNetworkRequest("postgres", "user_channels_set_muted", "user_channels", start, err)
	return err
}

// CountUserChannels считает каналы пользователя.
func (p *Postgres) CountUserChannels(userID int64) (int, error) {
	var count int
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	err := p.pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_channels WHERE user_id=$1`, userID).Scan(&count)
	metrics.ObserveNetworkRequest("postgres", "user_channels_count", "user_channels", start, err)
	return count, err
}

// UpdateUserChannelTags обновляет список тегов пользовательского канала.
func (p *Postgres) UpdateUserChannelTags(userID, channelID int64, tags []string) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	_, err := p.pool.Exec(ctx, `UPDATE user_channels SET tags=$3 WHERE user_id=$1 AND channel_id=$2`, userID, channelID, tags)
	metrics.ObserveNetworkRequest("postgres", "user_channels_update_tags", "user_channels", start, err)
	return err
}

// SavePosts сохраняет посты батчем.
func (p *Postgres) SavePosts(channelID int64, posts []domain.Post) error {
	if len(posts) == 0 {
		return nil
	}
	ctx, cancel := p.connCtx()
	defer cancel()

	batch := &pgx.Batch{}
	for _, post := range posts {
		batch.Queue(`
INSERT INTO posts (channel_id, tg_msg_id, published_at, url, text_trunc, raw_meta_json, hash)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (channel_id, tg_msg_id) DO UPDATE SET text_trunc=EXCLUDED.text_trunc, raw_meta_json=EXCLUDED.raw_meta_json, hash=EXCLUDED.hash
`, channelID, post.TGMsgID, post.PublishedAt, post.URL, post.Text, post.RawMetaJSON, post.Hash)
	}
	start := time.Now()
	br := p.pool.SendBatch(ctx, batch)
	metrics.ObserveNetworkRequest("postgres", "posts_send_batch", "posts", start, nil)
	defer br.Close()
	for range posts {
		start = time.Now()
		_, err := br.Exec()
		metrics.ObserveNetworkRequest("postgres", "posts_batch_exec", "posts", start, err)
		if err != nil {
			return err
		}
	}
	return nil
}

// ListRecentPosts возвращает посты.
func (p *Postgres) ListRecentPosts(channelIDs []int64, since time.Time) ([]domain.Post, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	rows, err := p.pool.Query(ctx, `
SELECT id, channel_id, tg_msg_id, published_at, url, text_trunc, raw_meta_json, hash, created_at
FROM posts WHERE channel_id = ANY($1) AND published_at >= $2
ORDER BY published_at DESC
`, channelIDs, since)
	metrics.ObserveNetworkRequest("postgres", "posts_list_recent", "posts", start, err)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []domain.Post
	for rows.Next() {
		var pPost domain.Post
		if err := rows.Scan(&pPost.ID, &pPost.ChannelID, &pPost.TGMsgID, &pPost.PublishedAt, &pPost.URL, &pPost.Text, &pPost.RawMetaJSON, &pPost.Hash, &pPost.CreatedAt); err != nil {
			return nil, err
		}
		posts = append(posts, pPost)
	}
	return posts, rows.Err()
}

// SaveSummary сохраняет суммаризацию.
func (p *Postgres) SaveSummary(postID int64, summary domain.Summary) (int64, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	var id int64
	bullets, err := json.Marshal(summary.Bullets)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	err = p.pool.QueryRow(ctx, `
        INSERT INTO post_summaries (post_id, headline, bullets_json, score)
        VALUES ($1,$2,$3,$4)
        RETURNING id
    `, postID, summary.Headline, bullets, summary.Score).Scan(&id)
	metrics.ObserveNetworkRequest("postgres", "post_summaries_insert", "post_summaries", start, err)
	return id, err
}

// CreateDigest сохраняет дайджест и элементы.
func (p *Postgres) CreateDigest(d domain.Digest) (domain.Digest, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	metrics.ObserveNetworkRequest("postgres", "begin_tx", "user_digests", start, err)
	if err != nil {
		return domain.Digest{}, err
	}
	defer tx.Rollback(ctx)
	var digestID int64
	start = time.Now()
	err = tx.QueryRow(ctx, `
INSERT INTO user_digests (user_id, date, items_count)
VALUES ($1,$2,$3)
ON CONFLICT (user_id, date) DO UPDATE SET items_count = EXCLUDED.items_count
RETURNING id
`, d.UserID, d.Date, len(d.Items)).Scan(&digestID)
	metrics.ObserveNetworkRequest("postgres", "user_digests_upsert", "user_digests", start, err)
	if err != nil {
		return domain.Digest{}, err
	}
	for _, item := range d.Items {
		start = time.Now()
		_, err = tx.Exec(ctx, `
INSERT INTO user_digest_items (digest_id, post_id, rank)
VALUES ($1,$2,$3)
ON CONFLICT DO NOTHING
`, digestID, item.Post.ID, item.Rank)
		metrics.ObserveNetworkRequest("postgres", "user_digest_items_insert", "user_digest_items", start, err)
		if err != nil {
			return domain.Digest{}, err
		}
	}
	start = time.Now()
	err = tx.Commit(ctx)
	metrics.ObserveNetworkRequest("postgres", "commit", "user_digests", start, err)
	if err != nil {
		return domain.Digest{}, err
	}
	d.ID = digestID
	userID := d.UserID
	meta := map[string]any{
		"date":        d.Date,
		"items_count": len(d.Items),
	}
	if d.Overview != "" {
		meta["has_overview"] = true
	}
	if len(d.Theses) > 0 {
		meta["theses_count"] = len(d.Theses)
	}
	_ = p.saveBusinessMetric(ctx, domain.BusinessMetric{
		Event:    domain.BusinessMetricEventDigestBuilt,
		UserID:   &userID,
		Metadata: meta,
	})
	return d, nil
}

// MarkDelivered помечает доставку.
func (p *Postgres) MarkDelivered(userID int64, date time.Time) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	_, err := p.pool.Exec(ctx, `UPDATE user_digests SET delivered_at=now() WHERE user_id=$1 AND date=$2`, userID, date)
	metrics.ObserveNetworkRequest("postgres", "user_digests_mark_delivered", "user_digests", start, err)
	return err
}

// EnsureDigestJob регистрирует попытку обработки задачи дайджеста.
func (p *Postgres) EnsureDigestJob(jobID string) (bool, int, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	var (
		delivered sql.NullTime
		attempts  int
	)

	start := time.Now()
	err := p.pool.QueryRow(ctx, `
INSERT INTO digest_job_statuses (job_id, attempts, updated_at)
VALUES ($1, 1, now())
ON CONFLICT (job_id) DO UPDATE
    SET attempts = digest_job_statuses.attempts + 1,
        updated_at = now()
RETURNING delivered_at, attempts
`, jobID).Scan(&delivered, &attempts)
	metrics.ObserveNetworkRequest("postgres", "digest_job_statuses_upsert", "digest_job_statuses", start, err)
	if err != nil {
		return false, 0, err
	}

	return delivered.Valid, attempts, nil
}

// MarkDigestJobDelivered помечает задачу как доставленную.
func (p *Postgres) MarkDigestJobDelivered(jobID string) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	_, err := p.pool.Exec(ctx, `
UPDATE digest_job_statuses
SET delivered_at = COALESCE(delivered_at, now()),
    updated_at = now()
WHERE job_id = $1
`, jobID)
	metrics.ObserveNetworkRequest("postgres", "digest_job_statuses_mark_delivered", "digest_job_statuses", start, err)
	return err
}

// WasDelivered проверяет наличие доставки.
func (p *Postgres) WasDelivered(userID int64, date time.Time) (bool, error) {
	var exists bool
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	err := p.pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM user_digests WHERE user_id=$1 AND date=$2`, userID, date).Scan(&exists)
	metrics.ObserveNetworkRequest("postgres", "user_digests_was_delivered", "user_digests", start, err)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return exists, err
}

// ListDigestHistory возвращает историю.
func (p *Postgres) ListDigestHistory(userID int64, fromDate time.Time) ([]domain.Digest, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	start := time.Now()
	rows, err := p.pool.Query(ctx, `
        SELECT id, date, delivered_at
        FROM user_digests WHERE user_id=$1 AND date >= $2
        ORDER BY date DESC
    `, userID, fromDate)
	metrics.ObserveNetworkRequest("postgres", "user_digests_list_history", "user_digests", start, err)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var digests []domain.Digest
	for rows.Next() {
		var d domain.Digest
		var delivered sql.NullTime
		if err := rows.Scan(&d.ID, &d.Date, &delivered); err != nil {
			return nil, err
		}
		if delivered.Valid {
			t := delivered.Time
			d.DeliveredAt = &t
		}
		d.UserID = userID
		digests = append(digests, d)
	}
	return digests, rows.Err()
}

// LoadMTProtoSession загружает сохранённую MTProto-сессию.
func (p *Postgres) LoadMTProtoSession(ctx context.Context, name string) ([]byte, error) {
	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	if name == "" {
		name = "default"
	}

	var data []byte
	start := time.Now()
	err := p.pool.QueryRow(ctx, `SELECT data FROM mtproto_sessions WHERE name = $1`, name).Scan(&data)
	metrics.ObserveNetworkRequest("postgres", "mtproto_sessions_load", "mtproto_sessions", start, err)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	clone := make([]byte, len(data))
	copy(clone, data)
	return clone, nil
}

// StoreMTProtoSession сохраняет MTProto-сессию.
func (p *Postgres) StoreMTProtoSession(ctx context.Context, name string, data []byte) error {
	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	if name == "" {
		name = "default"
	}

	tmp := make([]byte, len(data))
	copy(tmp, data)

	start := time.Now()
	_, err := p.pool.Exec(ctx, `
INSERT INTO mtproto_sessions (name, data, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (name) DO UPDATE SET data = EXCLUDED.data, updated_at = now()
`, name, tmp)
	metrics.ObserveNetworkRequest("postgres", "mtproto_sessions_store", "mtproto_sessions", start, err)
	return err
}

// ListMTProtoAccounts возвращает список MTProto-аккаунтов в указанном пуле.
func (p *Postgres) ListMTProtoAccounts(ctx context.Context, pool string) ([]domain.MTProtoAccount, error) {
	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	if pool == "" {
		pool = "default"
	}

	start := time.Now()
	rows, err := p.pool.Query(ctx, `
SELECT name, pool, api_id, api_hash, phone, username, raw_json
FROM mtproto_accounts
WHERE pool = $1
ORDER BY name
`, pool)
	metrics.ObserveNetworkRequest("postgres", "mtproto_accounts_list", "mtproto_accounts", start, err)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []domain.MTProtoAccount
	for rows.Next() {
		var (
			account  domain.MTProtoAccount
			phone    sql.NullString
			username sql.NullString
			rawJSON  []byte
		)
		if scanErr := rows.Scan(&account.Name, &account.Pool, &account.APIID, &account.APIHash, &phone, &username, &rawJSON); scanErr != nil {
			return nil, scanErr
		}
		if phone.Valid {
			account.Phone = phone.String
		}
		if username.Valid {
			account.Username = username.String
		}
		if len(rawJSON) > 0 {
			account.RawJSON = make([]byte, len(rawJSON))
			copy(account.RawJSON, rawJSON)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accounts, nil
}

// UpsertMTProtoAccount сохраняет или обновляет MTProto-аккаунт.
func (p *Postgres) UpsertMTProtoAccount(ctx context.Context, account domain.MTProtoAccount) error {
	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	if account.Pool == "" {
		account.Pool = "default"
	}
	if account.Name == "" {
		return fmt.Errorf("account name is required")
	}
	if account.APIID == 0 {
		return fmt.Errorf("account api_id is required")
	}
	if account.APIHash == "" {
		return fmt.Errorf("account api_hash is required")
	}

	phone := sql.NullString{}
	if account.Phone != "" {
		phone = sql.NullString{String: account.Phone, Valid: true}
	}
	username := sql.NullString{}
	if account.Username != "" {
		username = sql.NullString{String: account.Username, Valid: true}
	}

	var rawJSON interface{}
	if len(account.RawJSON) > 0 {
		rawJSON = account.RawJSON
	}

	start := time.Now()
	_, err := p.pool.Exec(ctx, `
INSERT INTO mtproto_accounts (pool, name, api_id, api_hash, phone, username, raw_json, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (pool, name) DO UPDATE
SET api_id = EXCLUDED.api_id,
    api_hash = EXCLUDED.api_hash,
    phone = EXCLUDED.phone,
    username = EXCLUDED.username,
    raw_json = EXCLUDED.raw_json,
    updated_at = now()
`, account.Pool, account.Name, account.APIID, account.APIHash, phone, username, rawJSON)
	metrics.ObserveNetworkRequest("postgres", "mtproto_accounts_upsert", "mtproto_accounts", start, err)
	return err
}
