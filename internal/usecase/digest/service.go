package digest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"tg-digest-bot/internal/domain"
)

// ErrNoChannels возвращается если у пользователя нет каналов.
var ErrNoChannels = errors.New("у пользователя нет каналов для дайджеста")

// Service реализует бизнес-логику построения дайджестов.
type Service struct {
	users      domain.UserRepo
	channels   domain.ChannelRepo
	posts      domain.PostRepo
	digestRepo domain.DigestRepo
	summarizer domain.Summarizer
	ranker     domain.Ranker
	collector  domain.Collector
	maxItems   int
}

var _ domain.DigestService = (*Service)(nil)

// NewService создаёт сервис дайджестов.
func NewService(users domain.UserRepo, channels domain.ChannelRepo, posts domain.PostRepo, digestRepo domain.DigestRepo, summarizer domain.Summarizer, ranker domain.Ranker, collector domain.Collector, maxItems int) *Service {
	return &Service{users: users, channels: channels, posts: posts, digestRepo: digestRepo, summarizer: summarizer, ranker: ranker, collector: collector, maxItems: maxItems}
}

// BuildAndSendNow строит дайджест и помечает его доставленным.
func (s *Service) BuildAndSendNow(userID int64) error {
	digest, err := s.BuildForDate(userID, time.Now().UTC())
	if err != nil {
		return err
	}
	if _, err := s.digestRepo.CreateDigest(digest); err != nil {
		return fmt.Errorf("сохранение дайджеста: %w", err)
	}
	return s.digestRepo.MarkDelivered(userID, digest.Date)
}

// BuildForDate строит дайджест за указанный день.
func (s *Service) BuildForDate(userID int64, date time.Time) (domain.Digest, error) {
	user, err := s.users.GetByTGID(userID)
	if err != nil {
		return domain.Digest{}, fmt.Errorf("получение пользователя: %w", err)
	}
	channels, err := s.channels.ListUserChannels(user.ID, 100, 0)
	if err != nil {
		return domain.Digest{}, fmt.Errorf("каналы пользователя: %w", err)
	}
	if len(channels) == 0 {
		return domain.Digest{}, ErrNoChannels
	}
	var channelIDs []int64
	for _, ch := range channels {
		channelIDs = append(channelIDs, ch.ID)
	}
	since := date.Add(-24 * time.Hour)
	posts, err := s.posts.ListRecentPosts(channelIDs, since)
	if err != nil {
		return domain.Digest{}, fmt.Errorf("получение постов: %w", err)
	}
	if len(posts) == 0 {
		return domain.Digest{UserID: user.ID, Date: date, Items: nil}, nil
	}
	ranked, err := s.ranker.Rank(posts)
	if err != nil {
		return domain.Digest{}, fmt.Errorf("ранжирование: %w", err)
	}
	if len(ranked) == 0 {
		return domain.Digest{UserID: user.ID, Date: date, Items: nil}, nil
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	if s.maxItems > 0 && len(ranked) > s.maxItems {
		ranked = ranked[:s.maxItems]
	}
	items := make([]domain.DigestItem, 0, len(ranked))
	for idx, rp := range ranked {
		summary := rp.Summary
		if summary.Headline == "" {
			var err error
			summary, err = s.summarizer.Summarize(rp.Post)
			if err != nil {
				return domain.Digest{}, fmt.Errorf("суммаризация: %w", err)
			}
		}
		items = append(items, domain.DigestItem{Post: rp.Post, Summary: summary, Rank: idx + 1})
	}
	return domain.Digest{UserID: user.ID, Date: date.Truncate(24 * time.Hour), Items: items}, nil
}

// CollectNow запускает сбор постов у списка каналов.
func (s *Service) CollectNow(ctx context.Context, channels []domain.Channel) error {
	for _, ch := range channels {
		posts, err := s.collector.Collect24h(ch)
		if err != nil {
			return fmt.Errorf("сбор истории %s: %w", ch.Alias, err)
		}
		if err := s.posts.SavePosts(ch.ID, posts); err != nil {
			return fmt.Errorf("сохранение постов: %w", err)
		}
	}
	return nil
}
