package domain

import "strings"

// UserRole описывает тариф пользователя.
type UserRole string

const (
	UserRoleFree      UserRole = "free"
	UserRolePlus      UserRole = "plus"
	UserRolePro       UserRole = "pro"
	UserRoleDeveloper UserRole = "developer"
)

// UserPlan описывает ограничения тарифа.
type UserPlan struct {
	Role             UserRole
	Name             string
	ChannelLimit     int
	ManualDailyLimit int
	ManualIntroTotal int
}

var plans = map[UserRole]UserPlan{
	UserRoleFree: {
		Role:             UserRoleFree,
		Name:             "Free",
		ChannelLimit:     3,
		ManualDailyLimit: 1,
		ManualIntroTotal: 5,
	},
	UserRolePlus: {
		Role:             UserRolePlus,
		Name:             "Plus",
		ChannelLimit:     10,
		ManualDailyLimit: 3,
	},
	UserRolePro: {
		Role:             UserRolePro,
		Name:             "Pro",
		ChannelLimit:     15,
		ManualDailyLimit: 6,
	},
	UserRoleDeveloper: {
		Role:             UserRoleDeveloper,
		Name:             "Developer",
		ChannelLimit:     0,
		ManualDailyLimit: 0,
	},
}

// PlanForRole возвращает тариф для роли.
func PlanForRole(role UserRole) UserPlan {
	if plan, ok := plans[UserRole(strings.ToLower(string(role)))]; ok {
		return plan
	}
	return plans[UserRoleFree]
}

// Plan возвращает тариф пользователя.
func (u User) Plan() UserPlan {
	return PlanForRole(u.Role)
}

// ManualRequestState описывает результат попытки зарезервировать ручной запрос.
type ManualRequestState struct {
	Allowed   bool
	Plan      UserPlan
	TotalUsed int
	UsedToday int
}

// RemainingToday возвращает оставшееся количество запросов на сегодня. -1 означает отсутствие лимитов.
func (s ManualRequestState) RemainingToday() int {
	if s.Plan.ManualDailyLimit <= 0 {
		return -1
	}
	remaining := s.Plan.ManualDailyLimit - s.UsedToday
	if remaining < 0 {
		return 0
	}
	return remaining
}

// IntroRemaining возвращает количество оставшихся стартовых запросов.
func (s ManualRequestState) IntroRemaining() int {
	if s.Plan.ManualIntroTotal <= 0 {
		return 0
	}
	remaining := s.Plan.ManualIntroTotal - s.TotalUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}
