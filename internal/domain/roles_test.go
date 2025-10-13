package domain

import "testing"

func TestRoleForReferralProgress(t *testing.T) {
	tests := []struct {
		name      string
		current   UserRole
		referrals int
		want      UserRole
	}{
		{name: "free without referrals", current: UserRoleFree, referrals: 0, want: UserRoleFree},
		{name: "free to plus", current: UserRoleFree, referrals: 3, want: UserRolePlus},
		{name: "free to pro", current: UserRoleFree, referrals: 5, want: UserRolePro},
		{name: "plus stays plus", current: UserRolePlus, referrals: 3, want: UserRolePlus},
		{name: "plus to pro", current: UserRolePlus, referrals: 5, want: UserRolePro},
		{name: "developer unaffected", current: UserRoleDeveloper, referrals: 10, want: UserRoleDeveloper},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RoleForReferralProgress(tt.current, tt.referrals); got != tt.want {
				t.Fatalf("RoleForReferralProgress(%v, %d) = %v, want %v", tt.current, tt.referrals, got, tt.want)
			}
		})
	}
}
