package digest

import (
	"testing"
	"time"

	"tg-digest-bot/internal/domain"
)

type stubRepo struct {
	user  domain.User
	posts []domain.Post
}

func (s *stubRepo) UpsertByTGID(int64, string, string) (domain.User, error) { return s.user, nil }
func (s *stubRepo) GetByTGID(int64) (domain.User, error)                    { return s.user, nil }
func (s *stubRepo) ListForDailyTime(time.Time) ([]domain.User, error) {
	return []domain.User{s.user}, nil
}
func (s *stubRepo) UpdateDailyTime(int64, time.Time) error { return nil }
func (s *stubRepo) DeleteUserData(int64) error             { return nil }
func (s *stubRepo) UpsertChannel(domain.ChannelMeta) (domain.Channel, error) {
	return domain.Channel{}, nil
}
func (s *stubRepo) ListUserChannels(int64, int, int) ([]domain.UserChannel, error) {
	return []domain.UserChannel{{ChannelID: 1, Channel: domain.Channel{ID: 1, Alias: "demo"}}}, nil
}
func (s *stubRepo) AttachChannelToUser(int64, int64) error                      { return nil }
func (s *stubRepo) DetachChannelFromUser(int64, int64) error                    { return nil }
func (s *stubRepo) SetMuted(int64, int64, bool) error                           { return nil }
func (s *stubRepo) CountUserChannels(int64) (int, error)                        { return 1, nil }
func (s *stubRepo) SavePosts(int64, []domain.Post) error                        { return nil }
func (s *stubRepo) ListRecentPosts([]int64, time.Time) ([]domain.Post, error)   { return s.posts, nil }
func (s *stubRepo) SaveSummary(int64, domain.Summary) (int64, error)            { return 1, nil }
func (s *stubRepo) CreateDigest(d domain.Digest) (domain.Digest, error)         { return d, nil }
func (s *stubRepo) MarkDelivered(int64, time.Time) error                        { return nil }
func (s *stubRepo) WasDelivered(int64, time.Time) (bool, error)                 { return false, nil }
func (s *stubRepo) ListDigestHistory(int64, time.Time) ([]domain.Digest, error) { return nil, nil }

func TestBuildForDate(t *testing.T) {
	repo := &stubRepo{user: domain.User{ID: 1, TGUserID: 42}, posts: []domain.Post{{ID: 1, ChannelID: 1, URL: "https://t.me/a/1", Text: "пример", PublishedAt: time.Now()}}}
	sum := &fakeSummarizer{}
	ranker := &fakeRanker{}
	service := NewService(repo, repo, repo, repo, sum, ranker, nil, 10)
	digest, err := service.BuildForDate(42, time.Now())
	if err != nil {
		t.Fatalf("не ожидали ошибку: %v", err)
	}
	if len(digest.Items) != 1 {
		t.Fatalf("ожидали 1 пункт")
	}
	if digest.Overview != "главное" {
		t.Fatalf("ожидали overview от ранкера")
	}
}

type fakeSummarizer struct{}

func (f *fakeSummarizer) Summarize(post domain.Post) (domain.Summary, error) {
	return domain.Summary{Headline: "ok"}, nil
}

type fakeRanker struct{}

func (f *fakeRanker) Rank(posts []domain.Post) (domain.DigestOutline, error) {
	return domain.DigestOutline{
		Overview: "главное",
		Theses:   []string{"тезис"},
		Items:    []domain.RankedPost{{Post: posts[0], Score: 1, Summary: domain.Summary{Headline: "ok"}}},
	}, nil
}
