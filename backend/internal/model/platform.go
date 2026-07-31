package model

// PlatformUser is intentionally separate from tenant users. Platform actions
// may target another tenant only through an explicit platform-scoped request.
type PlatformUser struct {
	Base
	Username string `gorm:"size:50;uniqueIndex;not null" json:"username"`
	Password string `gorm:"size:100;not null" json:"-"`
	Role     string `gorm:"size:30;not null;default:'platform_admin'" json:"role"`
	Status   string `gorm:"size:20;not null;default:'active'" json:"status"`
}
