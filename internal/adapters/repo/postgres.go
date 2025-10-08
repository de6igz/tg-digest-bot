package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tg-digest-bot/internal/domain"
)

// Postgres реализует репозитории на основе pgxpool.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres создаёт адаптер БД.
func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func (p *Postgres) connCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// UpsertByTGID реализует domain.UserRepo.
func (p *Postgres) UpsertByTGID(tgUserID int64, locale, tz string) (domain.User, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback(ctx)
	var user domain.User
	err = tx.QueryRow(ctx, `
INSERT INTO users (tg_user_id, locale, tz)
VALUES ($1, COALESCE(NULLIF($2,''),'ru-RU'), COALESCE(NULLIF($3,''),'Europe/Amsterdam'))
ON CONFLICT (tg_user_id) DO UPDATE SET locale = EXCLUDED.locale, tz = EXCLUDED.tz, updated_at = now()
RETURNING id, tg_user_id, locale, tz, daily_time, created_at, updated_at
`, tgUserID, locale, tz).Scan(&user.ID, &user.TGUserID, &user.Locale, &user.Timezone, &user.DailyTime, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

// GetByTGID возвращает пользователя по Telegram ID.
func (p *Postgres) GetByTGID(tgUserID int64) (domain.User, error) {
	var user domain.User
	ctx, cancel := p.connCtx()
	defer cancel()

	err := p.pool.QueryRow(ctx, `
SELECT id, tg_user_id, locale, tz, daily_time, created_at, updated_at
FROM users WHERE tg_user_id=$1
`, tgUserID).Scan(&user.ID, &user.TGUserID, &user.Locale, &user.Timezone, &user.DailyTime, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, fmt.Errorf("user not found")
	}
	return user, err
}

// ListForDailyTime выбирает пользователей, у которых локальное время совпадает.
func (p *Postgres) ListForDailyTime(now time.Time) ([]domain.User, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	rows, err := p.pool.Query(ctx, `
SELECT id, tg_user_id, locale, tz, daily_time, created_at, updated_at
FROM users WHERE daily_time = $1
`, now.Format("15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.TGUserID, &u.Locale, &u.Timezone, &u.DailyTime, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// UpdateDailyTime обновляет время.
func (p *Postgres) UpdateDailyTime(userID int64, daily time.Time) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	_, err := p.pool.Exec(ctx, `UPDATE users SET daily_time=$2, updated_at=now() WHERE id=$1`, userID, daily.Format("15:04:05"))
	return err
}

// DeleteUserData удаляет данные пользователя.
func (p *Postgres) DeleteUserData(userID int64) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	_, err := p.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	return err
}

// UpsertChannel сохраняет канал.
func (p *Postgres) UpsertChannel(meta domain.ChannelMeta) (domain.Channel, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	var ch domain.Channel
	err := p.pool.QueryRow(ctx, `
INSERT INTO channels (tg_channel_id, alias, title, is_allowed)
VALUES ($1,$2,$3,true)
ON CONFLICT(alias) DO UPDATE SET tg_channel_id=EXCLUDED.tg_channel_id, title=EXCLUDED.title, is_allowed=true
RETURNING id, tg_channel_id, alias, title, is_allowed, created_at
`, meta.ID, meta.Alias, meta.Title).Scan(&ch.ID, &ch.TGChannelID, &ch.Alias, &ch.Title, &ch.IsAllowed, &ch.CreatedAt)
	return ch, err
}

// ListUserChannels возвращает каналы пользователя.
func (p *Postgres) ListUserChannels(userID int64, limit, offset int) ([]domain.UserChannel, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	rows, err := p.pool.Query(ctx, `
SELECT uc.id, uc.user_id, uc.channel_id, uc.muted, uc.added_at,
       c.id, c.tg_channel_id, c.alias, c.title, c.is_allowed, c.created_at
FROM user_channels uc JOIN channels c ON c.id = uc.channel_id
WHERE uc.user_id=$1
ORDER BY uc.added_at DESC
LIMIT $2 OFFSET $3
`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []domain.UserChannel
	for rows.Next() {
		var uc domain.UserChannel
		if err := rows.Scan(&uc.ID, &uc.UserID, &uc.ChannelID, &uc.Muted, &uc.AddedAt,
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

	_, err := p.pool.Exec(ctx, `
INSERT INTO user_channels (user_id, channel_id)
VALUES ($1,$2)
ON CONFLICT (user_id, channel_id) DO NOTHING
`, userID, channelID)
	return err
}

// DetachChannelFromUser удаляет канал.
func (p *Postgres) DetachChannelFromUser(userID, channelID int64) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	_, err := p.pool.Exec(ctx, `DELETE FROM user_channels WHERE user_id=$1 AND channel_id=$2`, userID, channelID)
	return err
}

// SetMuted переключает mute.
func (p *Postgres) SetMuted(userID, channelID int64, muted bool) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	_, err := p.pool.Exec(ctx, `UPDATE user_channels SET muted=$3 WHERE user_id=$1 AND channel_id=$2`, userID, channelID, muted)
	return err
}

// CountUserChannels считает каналы пользователя.
func (p *Postgres) CountUserChannels(userID int64) (int, error) {
	var count int
	ctx, cancel := p.connCtx()
	defer cancel()

	err := p.pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_channels WHERE user_id=$1`, userID).Scan(&count)
	return count, err
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
	br := p.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range posts {
		if _, err := br.Exec(); err != nil {
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

	rows, err := p.pool.Query(ctx, `
SELECT id, channel_id, tg_msg_id, published_at, url, text_trunc, raw_meta_json, hash, created_at
FROM posts WHERE channel_id = ANY($1) AND published_at >= $2
ORDER BY published_at DESC
`, channelIDs, since)
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
	err = p.pool.QueryRow(ctx, `
        INSERT INTO post_summaries (post_id, headline, bullets_json, score)
        VALUES ($1,$2,$3,$4)
        RETURNING id
    `, postID, summary.Headline, bullets, summary.Score).Scan(&id)
	return id, err
}

// CreateDigest сохраняет дайджест и элементы.
func (p *Postgres) CreateDigest(d domain.Digest) (domain.Digest, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Digest{}, err
	}
	defer tx.Rollback(ctx)
	var digestID int64
	err = tx.QueryRow(ctx, `
INSERT INTO user_digests (user_id, date, items_count)
VALUES ($1,$2,$3)
ON CONFLICT (user_id, date) DO UPDATE SET items_count = EXCLUDED.items_count
RETURNING id
`, d.UserID, d.Date, len(d.Items)).Scan(&digestID)
	if err != nil {
		return domain.Digest{}, err
	}
	for _, item := range d.Items {
		_, err = tx.Exec(ctx, `
INSERT INTO user_digest_items (digest_id, post_id, rank)
VALUES ($1,$2,$3)
ON CONFLICT DO NOTHING
`, digestID, item.Post.ID, item.Rank)
		if err != nil {
			return domain.Digest{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Digest{}, err
	}
	d.ID = digestID
	return d, nil
}

// MarkDelivered помечает доставку.
func (p *Postgres) MarkDelivered(userID int64, date time.Time) error {
	ctx, cancel := p.connCtx()
	defer cancel()

	_, err := p.pool.Exec(ctx, `UPDATE user_digests SET delivered_at=now() WHERE user_id=$1 AND date=$2`, userID, date)
	return err
}

// WasDelivered проверяет наличие доставки.
func (p *Postgres) WasDelivered(userID int64, date time.Time) (bool, error) {
	var exists bool
	ctx, cancel := p.connCtx()
	defer cancel()

	err := p.pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM user_digests WHERE user_id=$1 AND date=$2`, userID, date).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return exists, err
}

// ListDigestHistory возвращает историю.
func (p *Postgres) ListDigestHistory(userID int64, fromDate time.Time) ([]domain.Digest, error) {
	ctx, cancel := p.connCtx()
	defer cancel()

	rows, err := p.pool.Query(ctx, `
        SELECT id, date, delivered_at
        FROM user_digests WHERE user_id=$1 AND date >= $2
        ORDER BY date DESC
    `, userID, fromDate)
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
