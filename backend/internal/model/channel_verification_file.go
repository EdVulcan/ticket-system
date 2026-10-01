package model

import "time"

// ChannelVerificationFile is a platform-managed public-domain verification
// file. The content is intentionally public once published, but the upload
// operation and tenant/channel association are platform scoped.
type ChannelVerificationFile struct {
	Base
	TenantID         uint      `gorm:"index;not null" json:"tenant_id"`
	ChannelAccountID uint      `gorm:"index;not null" json:"channel_account_id"`
	Kind             string    `gorm:"size:30;not null;index" json:"kind"` // wechat_miniapp, xiaohongshu
	Filename         string    `gorm:"size:255;uniqueIndex;not null" json:"filename"`
	StoragePath      string    `gorm:"size:500;not null" json:"-"`
	ContentHash      string    `gorm:"size:64;not null" json:"content_hash"`
	ContentSize      int64     `gorm:"not null" json:"content_size"`
	UploadedBy       uint      `gorm:"index;not null" json:"uploaded_by"`
	UploadedAt       time.Time `gorm:"not null" json:"uploaded_at"`
}
