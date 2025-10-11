package digest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/metrics"
)

// ErrNoChannels возвращается если у пользователя нет каналов.
var ErrNoChannels = errors.New("у пользователя нет каналов для дайджеста")

// ErrChannelNotFound возвращается если канал не прикреплён к пользователю.
var ErrChannelNotFound = errors.New("канал недоступен для пользователя")

const topPostsPerChannel = 10

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
	saved, err := s.digestRepo.CreateDigest(digest)
	if err != nil {
		return fmt.Errorf("сохранение дайджеста: %w", err)
	}
	return s.digestRepo.MarkDelivered(saved.UserID, saved.Date)
}

// BuildForDate строит дайджест за указанный день.
func (s *Service) BuildForDate(userID int64, date time.Time) (domain.Digest, error) {
	user, userChannels, err := s.loadUserAndChannels(userID)
	if err != nil {
		return domain.Digest{}, err
	}

	var channelIDs []int64
	for _, ch := range userChannels {
		channelIDs = append(channelIDs, ch.ChannelID)
	}

	since := date.Add(-24 * time.Hour)
	posts, err := s.posts.ListRecentPosts(channelIDs, since)
	if err != nil {
		return domain.Digest{}, fmt.Errorf("получение постов: %w", err)
	}

	posts = filterTopPosts(posts, topPostsPerChannel)

	return s.buildDigestFromPosts(user, date, posts)
}

// BuildChannelForDate строит дайджест за указанный день по конкретному каналу.
func (s *Service) BuildChannelForDate(userID, channelID int64, date time.Time) (domain.Digest, error) {
	user, userChannels, err := s.loadUserAndChannels(userID)
	if err != nil {
		return domain.Digest{}, err
	}

	if channelID == 0 {
		return domain.Digest{}, ErrChannelNotFound
	}

	found := false
	for _, ch := range userChannels {
		if ch.ChannelID == channelID {
			found = true
			break
		}
	}
	if !found {
		return domain.Digest{}, ErrChannelNotFound
	}

	since := date.Add(-24 * time.Hour)
	posts, err := s.posts.ListRecentPosts([]int64{channelID}, since)
	if err != nil {
		return domain.Digest{}, fmt.Errorf("получение постов: %w", err)
	}

	return s.buildDigestFromPosts(user, date, posts)
}

// CollectNow запускает сбор постов у списка каналов.
func (s *Service) CollectNow(ctx context.Context, channels []domain.Channel) error {
	for _, ch := range channels {
		metrics.IncDigestForChannel(ch.ID)
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

func (s *Service) loadUserAndChannels(userTGID int64) (domain.User, []domain.UserChannel, error) {
	user, err := s.users.GetByTGID(userTGID)
	if err != nil {
		return domain.User{}, nil, fmt.Errorf("получение пользователя: %w", err)
	}
	userChannels, err := s.channels.ListUserChannels(user.ID, 100, 0)
	if err != nil {
		return domain.User{}, nil, fmt.Errorf("каналы пользователя: %w", err)
	}
	if len(userChannels) == 0 {
		return domain.User{}, nil, ErrNoChannels
	}
	return user, userChannels, nil
}

func (s *Service) buildDigestFromPosts(user domain.User, date time.Time, posts []domain.Post) (domain.Digest, error) {
	if len(posts) == 0 {
		return domain.Digest{UserID: user.ID, Date: date, Items: nil}, nil
	}

	outline, err := s.ranker.Rank(posts)
	if err != nil {
		return domain.Digest{}, fmt.Errorf("ранжирование: %w", err)
	}

	if len(outline.Items) == 0 {
		return domain.Digest{UserID: user.ID, Date: date, Overview: outline.Overview, Theses: outline.Theses, Items: nil}, nil
	}

	sort.SliceStable(outline.Items, func(i, j int) bool { return outline.Items[i].Score > outline.Items[j].Score })
	if s.maxItems > 0 && len(outline.Items) > s.maxItems {
		outline.Items = outline.Items[:s.maxItems]
	}

	items := make([]domain.DigestItem, 0, len(outline.Items))
	for idx, rp := range outline.Items {
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

	return domain.Digest{UserID: user.ID, Date: date.Truncate(24 * time.Hour), Overview: outline.Overview, Theses: outline.Theses, Items: items}, nil
}

func filterTopPosts(posts []domain.Post, perChannelLimit int) []domain.Post {
	if perChannelLimit <= 0 {
		return posts
	}

	grouped := make(map[int64][]domain.Post)
	order := make([]int64, 0)
	for _, post := range posts {
		if _, ok := grouped[post.ChannelID]; !ok {
			order = append(order, post.ChannelID)
		}
		grouped[post.ChannelID] = append(grouped[post.ChannelID], post)
	}

	filtered := make([]domain.Post, 0, len(posts))
	for _, channelID := range order {
		channelPosts := grouped[channelID]
		sort.SliceStable(channelPosts, func(i, j int) bool {
			return engagementScore(channelPosts[i]) > engagementScore(channelPosts[j])
		})
		if len(channelPosts) > perChannelLimit {
			channelPosts = channelPosts[:perChannelLimit]
		}
		filtered = append(filtered, channelPosts...)
	}

	return filtered
}

func engagementScore(post domain.Post) float64 {
	if len(post.RawMetaJSON) == 0 {
		return 0
	}

	var meta struct {
		Views     int `json:"views"`
		Forwards  int `json:"forwards"`
		Replies   int `json:"replies"`
		Reactions int `json:"reactions"`
	}

	if err := json.Unmarshal(post.RawMetaJSON, &meta); err != nil {
		return 0
	}

	interactions := meta.Reactions + meta.Forwards + meta.Replies
	return float64(meta.Views) + float64(interactions*100)
}
