package model

import (
	"time"

	"gorm.io/gorm"
)

type Share struct {
	ID             uint           `gorm:"primaryKey" json:"id"`
	UserID         uint           `gorm:"not null;index" json:"user_id"`
	FileID         uint           `gorm:"not null;index" json:"file_id"`
	ShareCode      string         `gorm:"size:50;uniqueIndex;not null" json:"share_code"`
	AccessPassword string         `gorm:"size:255" json:"-"`                // 访问密码（可选）
	DownloadLimit  int            `gorm:"default:-1" json:"download_limit"` // -1表示无限
	DownloadCount  int            `gorm:"default:0" json:"download_count"`
	ExpireAt       *time.Time     `json:"expire_at"` // 过期时间
	CreatedAt      time.Time      `json:"created_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`

	User User `gorm:"foreignKey:UserID" json:"-"`
	File File `gorm:"foreignKey:FileID" json:"file"`
}

// IsExpired 检查分享是否已过期
func (s *Share) IsExpired() bool {
	if s.ExpireAt == nil {
		return false
	}
	return time.Now().After(*s.ExpireAt)
}

// CanDownload 检查是否可以下载
func (s *Share) CanDownload() bool {
	if s.IsExpired() {
		return false
	}
	if s.DownloadLimit != -1 && s.DownloadCount >= s.DownloadLimit {
		return false
	}
	return true
}

// IncrementDownload 增加下载计数
func (s *Share) IncrementDownload() {
	s.DownloadCount++
}
