package model

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	Username     string         `gorm:"uniqueIndex;size:50;not null" json:"username"`
	Email        string         `gorm:"uniqueIndex;size:100;not null" json:"email"`
	PasswordHash string         `gorm:"size:255;not null" json:"-"`
	StorageUsed  int64          `gorm:"default:0" json:"storage_used"`
	StorageLimit int64          `gorm:"default:10737418240" json:"storage_limit"` // 默认10GB
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// BeforeCreate GORM钩子
func (u *User) BeforeCreate(tx *gorm.DB) error {
	return nil
}

// CanUpload 检查用户是否有足够空间上传文件
func (u *User) CanUpload(fileSize int64) bool {
	return u.StorageUsed+fileSize <= u.StorageLimit
}

// AddStorage 增加已用存储
func (u *User) AddStorage(size int64) {
	u.StorageUsed += size
}

// RemoveStorage 减少已用存储
func (u *User) RemoveStorage(size int64) {
	u.StorageUsed -= size
	if u.StorageUsed < 0 {
		u.StorageUsed = 0
	}
}
