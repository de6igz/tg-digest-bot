package pgxpool

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Pool реализует простое in-memory-хранилище, позволяющее отлаживать репозитории.
type Pool struct {
	mu sync.Mutex
	db *fakeDB
}

// Config хранит настройки пула (минимальный аналог).
type Config struct {
	MaxConns int32
}

// Tx представляет транзакцию поверх in-memory базы.
type Tx struct {
	pool   *Pool
	db     *fakeDB
	closed bool
}

// ParseConfig возвращает конфигурацию без парсинга dsn.
func ParseConfig(_ string) (*Config, error) {
	return &Config{MaxConns: 4}, nil
}

// NewWithConfig создаёт новый пул с чистой базой.
func NewWithConfig(_ context.Context, _ *Config) (*Pool, error) {
	return &Pool{db: newFakeDB()}, nil
}

// Close для памяти ничего не делает.
func (p *Pool) Close() {}

// BeginTx копирует состояние базы для транзакции.
func (p *Pool) BeginTx(_ context.Context, _ pgx.TxOptions) (*Tx, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return &Tx{pool: p, db: p.db.clone()}, nil
}

// Rollback игнорирует изменения транзакции.
func (t *Tx) Rollback(_ context.Context) error {
	if t == nil || t.closed {
		return nil
	}
	t.closed = true
	return nil
}

// Commit применяет изменения транзакции.
func (t *Tx) Commit(_ context.Context) error {
	if t == nil || t.closed {
		return errors.New("tx closed")
	}
	t.pool.mu.Lock()
	defer t.pool.mu.Unlock()
	t.pool.db = t.db
	t.closed = true
	return nil
}

// Exec выполняет запрос в транзакции.
func (t *Tx) Exec(_ context.Context, query string, args ...any) (pgx.CommandTag, error) {
	if t == nil || t.closed {
		return pgx.CommandTag{}, errors.New("tx closed")
	}
	return t.db.exec(query, args...)
}

// QueryRow выполняет SELECT ... LIMIT 1 в транзакции.
func (t *Tx) QueryRow(_ context.Context, query string, args ...any) *pgx.Row {
	if t == nil || t.closed {
		return &pgx.Row{Err: errors.New("tx closed")}
	}
	return t.db.queryRow(query, args...)
}

// Query возвращает набор строк внутри транзакции (не используется, но предоставляется для совместимости).
func (t *Tx) Query(_ context.Context, query string, args ...any) (*pgx.Rows, error) {
	if t == nil || t.closed {
		return pgx.NewRows(nil, errors.New("tx closed")), nil
	}
	return t.db.query(query, args...)
}

// QueryRow выполняет запрос и возвращает одну строку.
func (p *Pool) QueryRow(_ context.Context, query string, args ...any) *pgx.Row {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.db.queryRow(query, args...)
}

// Exec выполняет DML запрос.
func (p *Pool) Exec(_ context.Context, query string, args ...any) (pgx.CommandTag, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.db.exec(query, args...)
}

// Query возвращает множество строк.
func (p *Pool) Query(_ context.Context, query string, args ...any) (*pgx.Rows, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.db.query(query, args...)
}

// SendBatch обрабатывает батч последовательно.
func (p *Pool) SendBatch(_ context.Context, batch *pgx.Batch) *pgx.BatchResults {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := batch.Items()
	results := make([]pgx.BatchExecResult, 0, len(items))
	for _, it := range items {
		tag, err := p.db.exec(it.Query, it.Args...)
		results = append(results, pgx.BatchExecResult{Tag: tag, Err: err})
	}
	return pgx.NewBatchResults(results)
}

// ------- in-memory реализации -------

type userRow struct {
	ID        int64
	TGUserID  int64
	Locale    string
	TZ        string
	DailyTime time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

type channelRow struct {
	ID          int64
	TGChannelID int64
	Alias       string
	Title       string
	IsAllowed   bool
	CreatedAt   time.Time
}

type userChannelRow struct {
	ID        int64
	ChannelID int64
	Muted     bool
	AddedAt   time.Time
}

type postRow struct {
	ID          int64
	ChannelID   int64
	TGMsgID     int64
	PublishedAt time.Time
	URL         string
	Text        string
	RawMeta     []byte
	Hash        string
	CreatedAt   time.Time
}

type summaryRow struct {
	ID          int64
	PostID      int64
	Headline    string
	BulletsJSON []byte
	Score       float64
	CreatedAt   time.Time
}

type digestRow struct {
	ID          int64
	UserID      int64
	Date        time.Time
	DeliveredAt *time.Time
	ItemsCount  int
}

type digestItemRow struct {
	PostID    int64
	Rank      int
	SummaryID int64
}

type fakeDB struct {
	nextUserID        int64
	users             map[int64]*userRow
	userByTG          map[int64]int64
	nextChannelID     int64
	channels          map[int64]*channelRow
	channelByAlias    map[string]int64
	userChannels      map[int64]map[int64]*userChannelRow
	nextUserChannelID int64
	nextPostID        int64
	posts             map[int64]*postRow
	postByKey         map[string]int64
	nextSummaryID     int64
	summaries         map[int64]*summaryRow
	nextDigestID      int64
	digests           map[int64]*digestRow
	digestByKey       map[string]int64
	digestItems       map[int64]map[int64]*digestItemRow
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		users:          make(map[int64]*userRow),
		userByTG:       make(map[int64]int64),
		channels:       make(map[int64]*channelRow),
		channelByAlias: make(map[string]int64),
		userChannels:   make(map[int64]map[int64]*userChannelRow),
		posts:          make(map[int64]*postRow),
		postByKey:      make(map[string]int64),
		summaries:      make(map[int64]*summaryRow),
		digests:        make(map[int64]*digestRow),
		digestByKey:    make(map[string]int64),
		digestItems:    make(map[int64]map[int64]*digestItemRow),
	}
}

func (db *fakeDB) clone() *fakeDB {
	cp := newFakeDB()
	cp.nextUserID = db.nextUserID
	cp.nextChannelID = db.nextChannelID
	cp.nextPostID = db.nextPostID
	cp.nextSummaryID = db.nextSummaryID
	cp.nextDigestID = db.nextDigestID
	for id, u := range db.users {
		du := *u
		cp.users[id] = &du
	}
	for k, v := range db.userByTG {
		cp.userByTG[k] = v
	}
	for id, ch := range db.channels {
		dc := *ch
		cp.channels[id] = &dc
	}
	for alias, id := range db.channelByAlias {
		cp.channelByAlias[alias] = id
	}
	for uid, m := range db.userChannels {
		inner := make(map[int64]*userChannelRow, len(m))
		for cid, uc := range m {
			d := *uc
			inner[cid] = &d
		}
		cp.userChannels[uid] = inner
	}
	for id, post := range db.posts {
		dp := *post
		if post.RawMeta != nil {
			buf := make([]byte, len(post.RawMeta))
			copy(buf, post.RawMeta)
			dp.RawMeta = buf
		}
		cp.posts[id] = &dp
	}
	for k, id := range db.postByKey {
		cp.postByKey[k] = id
	}
	for id, s := range db.summaries {
		ds := *s
		if s.BulletsJSON != nil {
			buf := make([]byte, len(s.BulletsJSON))
			copy(buf, s.BulletsJSON)
			ds.BulletsJSON = buf
		}
		cp.summaries[id] = &ds
	}
	for id, d := range db.digests {
		dd := *d
		if d.DeliveredAt != nil {
			t := *d.DeliveredAt
			dd.DeliveredAt = &t
		}
		cp.digests[id] = &dd
	}
	for key, id := range db.digestByKey {
		cp.digestByKey[key] = id
	}
	for did, items := range db.digestItems {
		inner := make(map[int64]*digestItemRow, len(items))
		for pid, item := range items {
			di := *item
			inner[pid] = &di
		}
		cp.digestItems[did] = inner
	}
	return cp
}

func (db *fakeDB) defaultDaily() time.Time {
	return time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC)
}

func (db *fakeDB) upsertUser(tgUserID int64, locale, tz string) *userRow {
	if locale == "" {
		locale = "ru-RU"
	}
	if tz == "" {
		tz = "Europe/Amsterdam"
	}
	now := time.Now().UTC()
	if id, ok := db.userByTG[tgUserID]; ok {
		u := db.users[id]
		u.Locale = locale
		u.TZ = tz
		u.UpdatedAt = now
		return u
	}
	db.nextUserID++
	u := &userRow{
		ID:        db.nextUserID,
		TGUserID:  tgUserID,
		Locale:    locale,
		TZ:        tz,
		DailyTime: db.defaultDaily(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	db.users[u.ID] = u
	db.userByTG[tgUserID] = u.ID
	return u
}

func (db *fakeDB) getUserByTGID(tgUserID int64) (*userRow, bool) {
	id, ok := db.userByTG[tgUserID]
	if !ok {
		return nil, false
	}
	return db.users[id], true
}

func (db *fakeDB) listUsersByTime(timeStr string) []*userRow {
	var result []*userRow
	for _, u := range db.users {
		if u.DailyTime.Format("15:04:05") == timeStr {
			result = append(result, u)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (db *fakeDB) updateUserDailyTime(userID int64, timeStr string) bool {
	u, ok := db.users[userID]
	if !ok {
		return false
	}
	parsed, _ := time.Parse("15:04:05", timeStr)
	u.DailyTime = time.Date(0, 1, 1, parsed.Hour(), parsed.Minute(), parsed.Second(), 0, time.UTC)
	u.UpdatedAt = time.Now().UTC()
	return true
}

func (db *fakeDB) deleteUser(userID int64) bool {
	u, ok := db.users[userID]
	if !ok {
		return false
	}
	delete(db.users, userID)
	delete(db.userByTG, u.TGUserID)
	delete(db.userChannels, userID)
	// удалить дайджесты
	for key, id := range db.digestByKey {
		if strings.HasPrefix(key, fmt.Sprintf("%d:", userID)) {
			delete(db.digestByKey, key)
			delete(db.digests, id)
			delete(db.digestItems, id)
		}
	}
	return true
}

func (db *fakeDB) upsertChannel(tgID int64, alias, title string) *channelRow {
	now := time.Now().UTC()
	if id, ok := db.channelByAlias[alias]; ok {
		ch := db.channels[id]
		ch.TGChannelID = tgID
		if title != "" {
			ch.Title = title
		}
		ch.IsAllowed = true
		return ch
	}
	db.nextChannelID++
	ch := &channelRow{
		ID:          db.nextChannelID,
		TGChannelID: tgID,
		Alias:       alias,
		Title:       title,
		IsAllowed:   true,
		CreatedAt:   now,
	}
	db.channels[ch.ID] = ch
	db.channelByAlias[alias] = ch.ID
	return ch
}

type userChannelEntry struct {
	link *userChannelRow
	ch   *channelRow
}

func (db *fakeDB) listUserChannels(userID int64) []userChannelEntry {
	links := db.userChannels[userID]
	if len(links) == 0 {
		return nil
	}
	var res []userChannelEntry
	for cid, link := range links {
		if ch, ok := db.channels[cid]; ok {
			res = append(res, userChannelEntry{link: link, ch: ch})
		}
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].link.AddedAt.Equal(res[j].link.AddedAt) {
			return res[i].ch.ID > res[j].ch.ID
		}
		return res[i].link.AddedAt.After(res[j].link.AddedAt)
	})
	return res
}

func (db *fakeDB) attachChannel(userID, channelID int64) bool {
	if _, ok := db.channels[channelID]; !ok {
		return false
	}
	m := db.userChannels[userID]
	if m == nil {
		m = make(map[int64]*userChannelRow)
		db.userChannels[userID] = m
	}
	if _, exists := m[channelID]; exists {
		return false
	}
	db.nextUserChannelID++
	m[channelID] = &userChannelRow{ID: db.nextUserChannelID, ChannelID: channelID, AddedAt: time.Now().UTC()}
	return true
}

func (db *fakeDB) detachChannel(userID, channelID int64) bool {
	m := db.userChannels[userID]
	if m == nil {
		return false
	}
	if _, ok := m[channelID]; !ok {
		return false
	}
	delete(m, channelID)
	if len(m) == 0 {
		delete(db.userChannels, userID)
	}
	return true
}

func (db *fakeDB) setMuted(userID, channelID int64, muted bool) bool {
	m := db.userChannels[userID]
	if m == nil {
		return false
	}
	if uc, ok := m[channelID]; ok {
		uc.Muted = muted
		return true
	}
	return false
}

func (db *fakeDB) countUserChannels(userID int64) int {
	return len(db.userChannels[userID])
}

func postKey(channelID, msgID int64) string {
	return fmt.Sprintf("%d:%d", channelID, msgID)
}

func (db *fakeDB) insertOrUpdatePost(channelID, msgID int64, publishedAt time.Time, url, text string, raw []byte, hash string) {
	key := postKey(channelID, msgID)
	if id, ok := db.postByKey[key]; ok {
		p := db.posts[id]
		p.PublishedAt = publishedAt
		p.URL = url
		p.Text = text
		if raw != nil {
			buf := make([]byte, len(raw))
			copy(buf, raw)
			p.RawMeta = buf
		} else {
			p.RawMeta = nil
		}
		p.Hash = hash
		return
	}
	db.nextPostID++
	created := time.Now().UTC()
	var rawCopy []byte
	if raw != nil {
		rawCopy = make([]byte, len(raw))
		copy(rawCopy, raw)
	}
	p := &postRow{
		ID:          db.nextPostID,
		ChannelID:   channelID,
		TGMsgID:     msgID,
		PublishedAt: publishedAt,
		URL:         url,
		Text:        text,
		RawMeta:     rawCopy,
		Hash:        hash,
		CreatedAt:   created,
	}
	db.posts[p.ID] = p
	db.postByKey[key] = p.ID
}

func (db *fakeDB) listRecentPosts(channelIDs []int64, since time.Time) []*postRow {
	set := make(map[int64]struct{}, len(channelIDs))
	for _, id := range channelIDs {
		set[id] = struct{}{}
	}
	var result []*postRow
	for _, p := range db.posts {
		if _, ok := set[p.ChannelID]; !ok {
			continue
		}
		if p.PublishedAt.Before(since) {
			continue
		}
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].PublishedAt.Equal(result[j].PublishedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].PublishedAt.After(result[j].PublishedAt)
	})
	return result
}

func (db *fakeDB) insertSummary(postID int64, headline string, bullets []byte, score float64) (int64, error) {
	if _, ok := db.posts[postID]; !ok {
		return 0, fmt.Errorf("post %d not found", postID)
	}
	db.nextSummaryID++
	var buf []byte
	if bullets != nil {
		buf = make([]byte, len(bullets))
		copy(buf, bullets)
	}
	s := &summaryRow{
		ID:          db.nextSummaryID,
		PostID:      postID,
		Headline:    headline,
		BulletsJSON: buf,
		Score:       score,
		CreatedAt:   time.Now().UTC(),
	}
	db.summaries[s.ID] = s
	return s.ID, nil
}

func digestKey(userID int64, date time.Time) string {
	return fmt.Sprintf("%d:%s", userID, date.Format("2006-01-02"))
}

func (db *fakeDB) upsertDigest(userID int64, date time.Time, items int) int64 {
	key := digestKey(userID, date)
	if id, ok := db.digestByKey[key]; ok {
		d := db.digests[id]
		d.ItemsCount = items
		return id
	}
	db.nextDigestID++
	d := &digestRow{ID: db.nextDigestID, UserID: userID, Date: date, ItemsCount: items}
	db.digests[d.ID] = d
	db.digestByKey[key] = d.ID
	return d.ID
}

func (db *fakeDB) insertDigestItem(digestID, postID int64, rank int) {
	items := db.digestItems[digestID]
	if items == nil {
		items = make(map[int64]*digestItemRow)
		db.digestItems[digestID] = items
	}
	if _, exists := items[postID]; exists {
		return
	}
	items[postID] = &digestItemRow{PostID: postID, Rank: rank}
}

func (db *fakeDB) markDelivered(userID int64, date time.Time) bool {
	key := digestKey(userID, date)
	id, ok := db.digestByKey[key]
	if !ok {
		return false
	}
	d := db.digests[id]
	now := time.Now().UTC()
	d.DeliveredAt = &now
	return true
}

func (db *fakeDB) wasDelivered(userID int64, date time.Time) (bool, bool) {
	key := digestKey(userID, date)
	id, ok := db.digestByKey[key]
	if !ok {
		return false, false
	}
	d := db.digests[id]
	if d.DeliveredAt == nil {
		return false, true
	}
	return true, true
}

func (db *fakeDB) listDigestHistory(userID int64, fromDate time.Time) []*digestRow {
	var result []*digestRow
	for _, d := range db.digests {
		if d.UserID != userID {
			continue
		}
		if d.Date.Before(fromDate) {
			continue
		}
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Date.Equal(result[j].Date) {
			return result[i].ID > result[j].ID
		}
		return result[i].Date.After(result[j].Date)
	})
	return result
}

// --- обработка SQL-запросов ---

func normalize(query string) string {
	return strings.ToLower(strings.TrimSpace(query))
}

func (db *fakeDB) queryRow(query string, args ...any) *pgx.Row {
	q := normalize(query)
	switch {
	case strings.HasPrefix(q, "insert into users"):
		tgID := pgx.ToInt64(args[0])
		locale := fmt.Sprint(args[1])
		tz := fmt.Sprint(args[2])
		u := db.upsertUser(tgID, locale, tz)
		return &pgx.Row{Values: []any{u.ID, u.TGUserID, u.Locale, u.TZ, u.DailyTime, u.CreatedAt, u.UpdatedAt}}
	case strings.HasPrefix(q, "select id, tg_user_id, locale, tz, daily_time, created_at, updated_at from users where tg_user_id"):
		tgID := pgx.ToInt64(args[0])
		if u, ok := db.getUserByTGID(tgID); ok {
			return &pgx.Row{Values: []any{u.ID, u.TGUserID, u.Locale, u.TZ, u.DailyTime, u.CreatedAt, u.UpdatedAt}}
		}
		return &pgx.Row{Err: pgx.ErrNoRows}
	case strings.HasPrefix(q, "insert into channels"):
		tgID := pgx.ToInt64(args[0])
		alias := fmt.Sprint(args[1])
		title := fmt.Sprint(args[2])
		ch := db.upsertChannel(tgID, alias, title)
		return &pgx.Row{Values: []any{ch.ID, ch.TGChannelID, ch.Alias, ch.Title, ch.IsAllowed, ch.CreatedAt}}
	case strings.HasPrefix(q, "select count(*) from user_channels"):
		userID := pgx.ToInt64(args[0])
		count := db.countUserChannels(userID)
		return &pgx.Row{Values: []any{int64(count)}}
	case strings.HasPrefix(q, "insert into post_summaries"):
		postID := pgx.ToInt64(args[0])
		headline := fmt.Sprint(args[1])
		bullets, _ := args[2].([]byte)
		score := 0.0
		if f, ok := args[3].(float64); ok {
			score = f
		}
		id, err := db.insertSummary(postID, headline, bullets, score)
		return &pgx.Row{Values: []any{id}, Err: err}
	case strings.HasPrefix(q, "insert into user_digests"):
		userID := pgx.ToInt64(args[0])
		date := args[1].(time.Time)
		items := int(pgx.ToInt64(args[2]))
		id := db.upsertDigest(userID, date, items)
		return &pgx.Row{Values: []any{id}}
	case strings.HasPrefix(q, "select delivered_at is not null from user_digests"):
		userID := pgx.ToInt64(args[0])
		date := args[1].(time.Time)
		delivered, ok := db.wasDelivered(userID, date)
		if !ok {
			return &pgx.Row{Err: pgx.ErrNoRows}
		}
		return &pgx.Row{Values: []any{delivered}}
	default:
		return &pgx.Row{Err: fmt.Errorf("unsupported query: %s", query)}
	}
}

func (db *fakeDB) exec(query string, args ...any) (pgx.CommandTag, error) {
	q := normalize(query)
	switch {
	case strings.HasPrefix(q, "update users set daily_time"):
		if ok := db.updateUserDailyTime(pgx.ToInt64(args[0]), fmt.Sprint(args[1])); !ok {
			return pgx.CommandTag{}, pgx.ErrNoRows
		}
		return pgx.CommandTag{RowsAffected: 1}, nil
	case strings.HasPrefix(q, "delete from users"):
		if db.deleteUser(pgx.ToInt64(args[0])) {
			return pgx.CommandTag{RowsAffected: 1}, nil
		}
		return pgx.CommandTag{}, pgx.ErrNoRows
	case strings.HasPrefix(q, "insert into user_channels"):
		added := db.attachChannel(pgx.ToInt64(args[0]), pgx.ToInt64(args[1]))
		if added {
			return pgx.CommandTag{RowsAffected: 1}, nil
		}
		return pgx.CommandTag{RowsAffected: 0}, nil
	case strings.HasPrefix(q, "delete from user_channels"):
		if db.detachChannel(pgx.ToInt64(args[0]), pgx.ToInt64(args[1])) {
			return pgx.CommandTag{RowsAffected: 1}, nil
		}
		return pgx.CommandTag{}, pgx.ErrNoRows
	case strings.HasPrefix(q, "update user_channels set muted"):
		if db.setMuted(pgx.ToInt64(args[0]), pgx.ToInt64(args[1]), args[2].(bool)) {
			return pgx.CommandTag{RowsAffected: 1}, nil
		}
		return pgx.CommandTag{}, pgx.ErrNoRows
	case strings.HasPrefix(q, "insert into posts"):
		db.insertOrUpdatePost(pgx.ToInt64(args[0]), pgx.ToInt64(args[1]), args[2].(time.Time), fmt.Sprint(args[3]), fmt.Sprint(args[4]), copyBytes(args[5]), fmt.Sprint(args[6]))
		return pgx.CommandTag{RowsAffected: 1}, nil
	case strings.HasPrefix(q, "insert into user_digest_items"):
		db.insertDigestItem(pgx.ToInt64(args[0]), pgx.ToInt64(args[1]), int(pgx.ToInt64(args[2])))
		return pgx.CommandTag{RowsAffected: 1}, nil
	case strings.HasPrefix(q, "update user_digests set delivered_at"):
		if db.markDelivered(pgx.ToInt64(args[0]), args[1].(time.Time)) {
			return pgx.CommandTag{RowsAffected: 1}, nil
		}
		return pgx.CommandTag{}, pgx.ErrNoRows
	default:
		return pgx.CommandTag{}, fmt.Errorf("unsupported exec query: %s", query)
	}
}

func copyBytes(v any) []byte {
	if v == nil {
		return nil
	}
	if b, ok := v.([]byte); ok {
		buf := make([]byte, len(b))
		copy(buf, b)
		return buf
	}
	return nil
}

func (db *fakeDB) query(query string, args ...any) (*pgx.Rows, error) {
	q := normalize(query)
	switch {
	case strings.HasPrefix(q, "select id, tg_user_id, locale, tz, daily_time, created_at, updated_at from users where daily_time"):
		timeStr := fmt.Sprint(args[0])
		users := db.listUsersByTime(timeStr)
		rows := make([][]any, 0, len(users))
		for _, u := range users {
			rows = append(rows, []any{u.ID, u.TGUserID, u.Locale, u.TZ, u.DailyTime, u.CreatedAt, u.UpdatedAt})
		}
		return pgx.NewRows(rows, nil), nil
	case strings.Contains(q, "from user_channels uc join channels"):
		userID := pgx.ToInt64(args[0])
		limit := int(pgx.ToInt64(args[1]))
		offset := int(pgx.ToInt64(args[2]))
		entries := db.listUserChannels(userID)
		if offset > len(entries) {
			offset = len(entries)
		}
		end := offset + limit
		if limit == 0 || end > len(entries) {
			end = len(entries)
		}
		subset := entries[offset:end]
		rows := make([][]any, 0, len(subset))
		for _, entry := range subset {
			rows = append(rows, []any{
				entry.link.ID,
				userID,
				entry.link.ChannelID,
				entry.link.Muted,
				entry.link.AddedAt,
				entry.ch.ID,
				entry.ch.TGChannelID,
				entry.ch.Alias,
				entry.ch.Title,
				entry.ch.IsAllowed,
				entry.ch.CreatedAt,
			})
		}
		return pgx.NewRows(rows, nil), nil
	case strings.HasPrefix(q, "select id, channel_id, tg_msg_id, published_at, url, text_trunc, raw_meta_json, hash, created_at from posts"):
		channelIDs, _ := args[0].([]int64)
		since := args[1].(time.Time)
		posts := db.listRecentPosts(channelIDs, since)
		rows := make([][]any, 0, len(posts))
		for _, p := range posts {
			raw := copyBytes(p.RawMeta)
			rows = append(rows, []any{p.ID, p.ChannelID, p.TGMsgID, p.PublishedAt, p.URL, p.Text, raw, p.Hash, p.CreatedAt})
		}
		return pgx.NewRows(rows, nil), nil
	case strings.HasPrefix(q, "select id, date, delivered_at from user_digests"):
		userID := pgx.ToInt64(args[0])
		fromDate := args[1].(time.Time)
		digests := db.listDigestHistory(userID, fromDate)
		rows := make([][]any, 0, len(digests))
		for _, d := range digests {
			var delivered any
			if d.DeliveredAt != nil {
				delivered = *d.DeliveredAt
			} else {
				delivered = nil
			}
			rows = append(rows, []any{d.ID, d.Date, delivered})
		}
		return pgx.NewRows(rows, nil), nil
	default:
		return pgx.NewRows(nil, fmt.Errorf("unsupported query: %s", query)), nil
	}
}
