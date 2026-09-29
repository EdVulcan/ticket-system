package model

// TenantMemberBenefit stores the tenant-wide membership pricing rule. A
// missing row means the tenant has not configured a member discount yet.
// DiscountPercent is a reduction percentage in the inclusive range 0..100.
type TenantMemberBenefit struct {
	Base
	TenantID        uint `gorm:"not null;uniqueIndex" json:"tenant_id"`
	DiscountPercent int  `gorm:"not null;check:chk_tenant_member_benefit_percent,discount_percent >= 0 AND discount_percent <= 100" json:"discount_percent"`
}
