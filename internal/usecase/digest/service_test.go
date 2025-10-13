package digest

import (
	"encoding/json"
	"testing"
	"time"

	"tg-digest-bot/internal/domain"
)

type stubRepo struct {
	user         domain.User
	posts        []domain.Post
	userChannels []domain.UserChannel
}

func (s *stubRepo) UpsertByTGID(_ int64, _ string, _ string) (domain.User, bool, error) {
	return s.user, false, nil
}
func (s *stubRepo) GetByTGID(_ int64) (domain.User, error) { return s.user, nil }
func (s *stubRepo) ListForDailyTime(_ time.Time) ([]domain.User, error) {
	return []domain.User{s.user}, nil
}
func (s *stubRepo) UpdateDailyTime(_ int64, _ time.Time) error { return nil }
func (s *stubRepo) DeleteUserData(_ int64) error               { return nil }
func (s *stubRepo) ReserveManualRequest(_ int64, _ time.Time) (domain.ManualRequestState, error) {
	return domain.ManualRequestState{Allowed: true, Plan: domain.PlanForRole(domain.UserRoleFree)}, nil
}
func (s *stubRepo) ApplyReferral(_ string, _ int64) (domain.ReferralResult, error) {
	return domain.ReferralResult{User: s.user}, nil
}
func (s *stubRepo) UpsertChannel(_ domain.ChannelMeta) (domain.Channel, error) {
	return domain.Channel{}, nil
}
func (s *stubRepo) ListUserChannels(_ int64, _ int, _ int) ([]domain.UserChannel, error) {
	if len(s.userChannels) == 0 {
		return []domain.UserChannel{{ChannelID: 1, Channel: domain.Channel{ID: 1, Alias: "demo"}}}, nil
	}
	return s.userChannels, nil
}
func (s *stubRepo) AttachChannelToUser(_ int64, _ int64) error   { return nil }
func (s *stubRepo) DetachChannelFromUser(_ int64, _ int64) error { return nil }
func (s *stubRepo) SetMuted(_ int64, _ int64, _ bool) error      { return nil }
func (s *stubRepo) CountUserChannels(_ int64) (int, error)       { return len(s.userChannels), nil }
func (s *stubRepo) UpdateUserChannelTags(userID, channelID int64, tags []string) error {
	return nil
}
func (s *stubRepo) SavePosts(_ int64, _ []domain.Post) error { return nil }
func (s *stubRepo) ListRecentPosts(channelIDs []int64, _ time.Time) ([]domain.Post, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}
	lookup := make(map[int64]struct{}, len(channelIDs))
	for _, id := range channelIDs {
		lookup[id] = struct{}{}
	}
	var filtered []domain.Post
	for _, post := range s.posts {
		if _, ok := lookup[post.ChannelID]; ok {
			filtered = append(filtered, post)
		}
	}
	return filtered, nil
}
func (s *stubRepo) SaveSummary(_ int64, _ domain.Summary) (int64, error) { return 1, nil }
func (s *stubRepo) CreateDigest(d domain.Digest) (domain.Digest, error)  { return d, nil }
func (s *stubRepo) MarkDelivered(_ int64, _ time.Time) error             { return nil }
func (s *stubRepo) WasDelivered(_ int64, _ time.Time) (bool, error)      { return false, nil }
func (s *stubRepo) ListDigestHistory(_ int64, _ time.Time) ([]domain.Digest, error) {
	return nil, nil
}

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
	if len(ranker.captured) != 1 {
		t.Fatalf("ожидали, что ранкер получил 1 пост")
	}
}

func TestBuildForDateFiltersTopPosts(t *testing.T) {
	var posts []domain.Post
	for i := 0; i < 12; i++ {
		raw := mustJSON(map[string]int{"views": i})
		posts = append(posts, domain.Post{ID: int64(i + 1), ChannelID: 1, RawMetaJSON: raw})
	}
	repo := &stubRepo{
		user:         domain.User{ID: 1, TGUserID: 42},
		posts:        posts,
		userChannels: []domain.UserChannel{{ChannelID: 1}},
	}
	sum := &fakeSummarizer{}
	ranker := &fakeRanker{}
	service := NewService(repo, repo, repo, repo, sum, ranker, nil, 10)

	_, err := service.BuildForDate(42, time.Now())
	if err != nil {
		t.Fatalf("не ожидали ошибку: %v", err)
	}

	if len(ranker.captured) != 10 {
		t.Fatalf("ожидали 10 постов после фильтра, получили %d", len(ranker.captured))
	}

	if ranker.captured[0].ID != 12 {
		t.Fatalf("ожидали, что первым будет пост с наибольшими просмотрами")
	}
}

func TestBuildForDateFiltersTopPostsPerChannel(t *testing.T) {
	var posts []domain.Post
	for i := 0; i < 12; i++ {
		raw := mustJSON(map[string]int{"views": i})
		posts = append(posts, domain.Post{ID: int64(i + 1), ChannelID: 1, RawMetaJSON: raw})
	}
	for i := 0; i < 12; i++ {
		raw := mustJSON(map[string]int{"views": i})
		posts = append(posts, domain.Post{ID: int64(100 + i), ChannelID: 2, RawMetaJSON: raw})
	}

	repo := &stubRepo{
		user:  domain.User{ID: 1, TGUserID: 42},
		posts: posts,
		userChannels: []domain.UserChannel{
			{ChannelID: 1, Channel: domain.Channel{ID: 1, Alias: "first"}},
			{ChannelID: 2, Channel: domain.Channel{ID: 2, Alias: "second"}},
		},
	}
	sum := &fakeSummarizer{}
	ranker := &fakeRanker{}
	service := NewService(repo, repo, repo, repo, sum, ranker, nil, 10)

	_, err := service.BuildForDate(42, time.Now())
	if err != nil {
		t.Fatalf("не ожидали ошибку: %v", err)
	}

	if len(ranker.captured) != 20 {
		t.Fatalf("ожидали 20 постов после фильтра, получили %d", len(ranker.captured))
	}

	if ranker.captured[0].ID != 12 {
		t.Fatalf("ожидали, что первым будет самый популярный пост первого канала")
	}
	if ranker.captured[9].ID != 3 {
		t.Fatalf("ожидали, что последний в первой группе будет пост с ID 3, получили %d", ranker.captured[9].ID)
	}
	if ranker.captured[10].ID != 111 {
		t.Fatalf("ожидали, что первым во второй группе будет пост с ID 111, получили %d", ranker.captured[10].ID)
	}
}

func TestBuildChannelForDate(t *testing.T) {
	var posts []domain.Post
	for i := 0; i < 5; i++ {
		raw := mustJSON(map[string]int{"views": i})
		posts = append(posts, domain.Post{ID: int64(i + 1), ChannelID: 1, RawMetaJSON: raw})
	}
	repo := &stubRepo{
		user:         domain.User{ID: 1, TGUserID: 42},
		posts:        posts,
		userChannels: []domain.UserChannel{{ChannelID: 1}},
	}
	sum := &fakeSummarizer{}
	ranker := &fakeRanker{}
	service := NewService(repo, repo, repo, repo, sum, ranker, nil, 10)

	_, err := service.BuildChannelForDate(42, 1, time.Now())
	if err != nil {
		t.Fatalf("не ожидали ошибку: %v", err)
	}

	if len(ranker.captured) != len(posts) {
		t.Fatalf("ожидали передачу всех постов канала")
	}
}

func TestBuildTagsForDateFiltersChannels(t *testing.T) {
	repo := &stubRepo{
		user: domain.User{ID: 1, TGUserID: 42},
		userChannels: []domain.UserChannel{
			{ChannelID: 1, Tags: []string{"Новости"}},
			{ChannelID: 2, Tags: []string{"Игры"}},
		},
		posts: []domain.Post{
			{ID: 1, ChannelID: 1, RawMetaJSON: mustJSON(map[string]int{"views": 10})},
			{ID: 2, ChannelID: 2, RawMetaJSON: mustJSON(map[string]int{"views": 10})},
		},
	}
	sum := &fakeSummarizer{}
	ranker := &fakeRanker{}
	service := NewService(repo, repo, repo, repo, sum, ranker, nil, 10)

	_, err := service.BuildTagsForDate(42, []string{"Новости"}, time.Now())
	if err != nil {
		t.Fatalf("не ожидали ошибку: %v", err)
	}

	if len(ranker.captured) != 1 {
		t.Fatalf("ожидали один пост, получили %d", len(ranker.captured))
	}
	if ranker.captured[0].ChannelID != 1 {
		t.Fatalf("ожидали пост только из канала с тегом 'Новости'")
	}
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

type fakeSummarizer struct{}

func (f *fakeSummarizer) Summarize(post domain.Post) (domain.Summary, error) {
	return domain.Summary{Headline: "ok"}, nil
}

type fakeRanker struct {
	captured []domain.Post
}

func (f *fakeRanker) Rank(posts []domain.Post) (domain.DigestOutline, error) {
	f.captured = append([]domain.Post(nil), posts...)
	if len(posts) == 0 {
		return domain.DigestOutline{}, nil
	}
	return domain.DigestOutline{
		Overview: "главное",
		Theses:   []string{"тезис"},
		Items:    []domain.RankedPost{{Post: posts[0], Score: 1, Summary: domain.Summary{Headline: "ok"}}},
	}, nil
}
