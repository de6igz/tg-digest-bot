package domain

import "time"

// User описывает пользователя Telegram в системе.
type User struct {
	ID        int64
	TGUserID  int64
	Locale    string
	Timezone  string
	DailyTime time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Channel описывает публичный канал Telegram.
type Channel struct {
	ID          int64
	TGChannelID int64
	Alias       string
	Title       string
	IsAllowed   bool
	CreatedAt   time.Time
}

// UserChannel хранит состояние подписки пользователя на канал.
type UserChannel struct {
	ID        int64
	UserID    int64
	ChannelID int64
	Muted     bool
	AddedAt   time.Time
	Channel   Channel
}

// Post представляет сообщение канала.
type Post struct {
	ID          int64
	ChannelID   int64
	TGMsgID     int64
	PublishedAt time.Time
	URL         string
	Text        string
	RawMetaJSON []byte
	Hash        string
	CreatedAt   time.Time
}

// Summary содержит краткое содержание поста.
type Summary struct {
	Headline string
	Bullets  []string
	Score    float64
}

// RankedPost хранит оценённый пост после ранжирования.
type RankedPost struct {
	Post    Post
	Score   float64
	Summary Summary
}

// DigestItem описывает одну позицию в дайджесте.
type DigestItem struct {
	Post    Post
	Summary Summary
	Rank    int
}

// Digest представляет собой итоговый дайджест пользователя.
type Digest struct {
	ID          int64
	UserID      int64
	Date        time.Time
	Items       []DigestItem
	DeliveredAt *time.Time
}
