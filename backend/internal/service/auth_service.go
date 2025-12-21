package service

import (
	"errors"
	"ticket-backend/internal/model"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct{}

var jwtSecret = []byte("your_jwt_secret_key") // In production, load from config

type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	TenantID uint   `json:"tenant_id"`
	jwt.RegisteredClaims
}

func (s *AuthService) Login(username, password string) (string, *model.User, error) {
	var user model.User
	if err := model.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return "", nil, errors.New("invalid credentials")
	}

	// Compare password
	// For initial admin, we might not have hashed password yet, so handle plain text for "123456" if needed
	// But best practice is always hash. Let's assume we will seed with hash.
	// For now, if password matches plain text "123456" and db has it, it works.
	// But we should use bcrypt.

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", nil, errors.New("invalid credentials")
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
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		TenantID: user.TenantID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "ticket-system",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

func (s *AuthService) StaffLogin(jobNumber, password string) (string, *model.Staff, error) {
	var staff model.Staff
	if err := model.DB.Where("job_number = ?", jobNumber).First(&staff).Error; err != nil {
		return "", nil, errors.New("invalid credentials")
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
		UserID:   staff.ID,
		Username: staff.Name, // Use Name as Username for claims
		Role:     staff.Roles,
		TenantID: staff.TenantID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "ticket-system",
			Subject:   "staff:" + staff.JobNumber,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// Helper to hash password (used for seeding or creating users)
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}
