package domain

import "time"

// ChannelMeta содержит метаданные канала из MTProto.
type ChannelMeta struct {
	ID     int64
	Alias  string
	Title  string
	Public bool
}

// ChannelResolver отвечает за проверку публичности и получение метаданных канала.
type ChannelResolver interface {
	ResolvePublic(alias string) (ChannelMeta, error)
}

// Collector выгружает сообщения за последние 24 часа.
type Collector interface {
	Collect24h(channel Channel) ([]Post, error)
}

// Ranker анализирует посты и возвращает дайджест.
type Ranker interface {
	Rank(posts []Post) (DigestOutline, error)
}

// Summarizer строит краткое содержание поста.
type Summarizer interface {
	Summarize(post Post) (Summary, error)
}

// DigestService отвечает за построение и доставку дайджестов.
type DigestService interface {
	BuildAndSendNow(userID int64) error
	BuildForDate(userID int64, date time.Time) (Digest, error)
	BuildChannelForDate(userID, channelID int64, date time.Time) (Digest, error)
	BuildTagsForDate(userID int64, tags []string, date time.Time) (Digest, error)
}

// UserRepo управляет пользователями.
type UserRepo interface {
	UpsertByTGID(tgUserID int64, locale, tz string) (User, error)
	GetByTGID(tgUserID int64) (User, error)
	ListForDailyTime(now time.Time) ([]User, error)
	UpdateDailyTime(userID int64, daily time.Time) error
	DeleteUserData(userID int64) error
}

// ChannelRepo управляет каналами.
type ChannelRepo interface {
	UpsertChannel(meta ChannelMeta) (Channel, error)
	ListUserChannels(userID int64, limit, offset int) ([]UserChannel, error)
	AttachChannelToUser(userID, channelID int64) error
	DetachChannelFromUser(userID, channelID int64) error
	SetMuted(userID, channelID int64, muted bool) error
	CountUserChannels(userID int64) (int, error)
	UpdateUserChannelTags(userID, channelID int64, tags []string) error
}

// PostRepo управляет постами и суммаризациями.
type PostRepo interface {
	SavePosts(channelID int64, posts []Post) error
	ListRecentPosts(channelIDs []int64, since time.Time) ([]Post, error)
	SaveSummary(postID int64, summary Summary) (int64, error)
}

// DigestRepo сохраняет и возвращает дайджесты.
type DigestRepo interface {
	CreateDigest(digest Digest) (Digest, error)
	MarkDelivered(userID int64, date time.Time) error
	WasDelivered(userID int64, date time.Time) (bool, error)
	ListDigestHistory(userID int64, fromDate time.Time) ([]Digest, error)
}

// Cache используется для простых TTL-хранилищ.
type Cache interface {
	Once(key string, ttl time.Duration, fn func() error) error
	Set(key string, value []byte, ttl time.Duration) error
	Get(key string) ([]byte, error)
}
