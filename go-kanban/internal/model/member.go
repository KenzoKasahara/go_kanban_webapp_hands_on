package model

// Role は Project 内での権限。
const (
	RoleOwner  = "owner"
	RoleMember = "member"
	RoleViewer = "viewer"
)

func IsValidRole(role string) bool {
	switch role {
	case RoleOwner, RoleMember, RoleViewer:
		return true
	default:
		return false
	}
}

// CanWriteTask は Task を作成・更新できる Role かを判断する。
func CanWriteTask(role string) bool {
	return role == RoleOwner || role == RoleMember
}

// CanManageMembers は Member を追加・変更できる Role かを判断する。
func CanManageMembers(role string) bool {
	return role == RoleOwner
}
