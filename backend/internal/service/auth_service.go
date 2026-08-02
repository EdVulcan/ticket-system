package service

import (
	"errors"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct{}

const passwordHashCost = 12

type Claims struct {
	UserID         uint   `json:"user_id"`
	Username       string `json:"username"`
	Role           string `json:"role"`
	TenantID       uint   `json:"tenant_id"`
	Scope          string `json:"scope"` // tenant, platform
	PlatformUserID uint   `json:"platform_user_id,omitempty"`
	TokenVersion   int    `json:"token_version"`
	jwt.RegisteredClaims
}

func (s *AuthService) Login(systemCode, username, password string) (string, *model.User, error) {
	// 1. Find Tenant by SystemCode
	var tenant model.Tenant
	if err := model.DB.Where("system_code = ?", systemCode).First(&tenant).Error; err != nil {
		return "", nil, errors.New("系统编号无效")
	}
	if tenant.Status != "" && tenant.Status != "active" {
		return "", nil, errors.New("租户已被停用")
	}

	// 2. Find User by Username AND TenantID
	var user model.User
	if err := model.DB.Preload("Tenant").Preload("Tenant.Capabilities").Where("username = ? AND tenant_id = ?", username, tenant.ID).First(&user).Error; err != nil {
		return "", nil, errors.New("用户名或密码错误")
	}

	// Compare password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", nil, errors.New("用户名或密码错误")
	}

	// Generate Token
	token, err := s.GenerateToken(&user)
	if err != nil {
		return "", nil, err
	}

	return token, &user, nil
}

func (s *AuthService) GenerateToken(user *model.User) (string, error) {
	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		UserID:       user.ID,
		Username:     user.Username,
		Role:         user.Role,
		TenantID:     user.TenantID,
		Scope:        "tenant",
		TokenVersion: user.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "ticket-system",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.GlobalConfig.Security.JWTSecret))
}

func (s *AuthService) StaffLogin(systemCode, jobNumber, password string) (string, *model.Staff, error) {
	// 1. Find Tenant
	var tenant model.Tenant
	if err := model.DB.Where("system_code = ?", systemCode).First(&tenant).Error; err != nil {
		return "", nil, errors.New("系统编号无效")
	}
	if tenant.Status != "" && tenant.Status != "active" {
		return "", nil, errors.New("租户已被停用")
	}

	// 2. Find Staff
	var staff model.Staff
	if err := model.DB.Where("job_number = ? AND tenant_id = ?", jobNumber, tenant.ID).First(&staff).Error; err != nil {
		return "", nil, errors.New("工号或密码错误")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(staff.Password), []byte(password)); err != nil {
		return "", nil, errors.New("invalid credentials")
	}

	if staff.Status != "active" {
		return "", nil, errors.New("account is not active")
	}

	token, err := s.GenerateStaffToken(&staff)
	if err != nil {
		return "", nil, err
	}

	return token, &staff, nil
}

func (s *AuthService) GenerateStaffToken(staff *model.Staff) (string, error) {
	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		UserID:       staff.ID,
		Username:     staff.Name, // Use Name as Username for claims
		Role:         staff.Roles,
		TenantID:     staff.TenantID,
		Scope:        "tenant",
		TokenVersion: staff.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "ticket-system",
			Subject:   "staff:" + staff.JobNumber,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.GlobalConfig.Security.JWTSecret))
}

func (s *AuthService) PlatformLogin(username, password string) (string, *model.PlatformUser, error) {
	var user model.PlatformUser
	if err := model.DB.Where("username = ? AND status = ?", username, "active").First(&user).Error; err != nil {
		return "", nil, errors.New("用户名或密码错误")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", nil, errors.New("用户名或密码错误")
	}
	token, err := s.GeneratePlatformToken(&user)
	if err != nil {
		return "", nil, err
	}
	return token, &user, nil
}

func (s *AuthService) GeneratePlatformToken(user *model.PlatformUser) (string, error) {
	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		Username: user.Username, Role: user.Role, Scope: "platform", PlatformUserID: user.ID, TokenVersion: user.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime), IssuedAt: jwt.NewNumericDate(time.Now()),
			Issuer: "ticket-system", Subject: "platform:" + user.Username,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.GlobalConfig.Security.JWTSecret))
}

// Helper to hash password (used for seeding or creating users)
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	return string(bytes), err
}
