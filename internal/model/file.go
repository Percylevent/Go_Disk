package model

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

type File struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	UserID      uint           `gorm:"not null;index" json:"user_id"`
	ParentID    uint           `gorm:"default:0;index" json:"parent_id"` // 0表示根目录
	FileName    string         `gorm:"size:255;not null" json:"file_name"`
	FilePath    string         `gorm:"size:500;not null" json:"file_path"` // 实际存储路径
	FileHash    string         `gorm:"size:64;index" json:"file_hash"`     // SHA256哈希值
	FileSize    int64          `gorm:"default:0" json:"file_size"`
	MimeType    string         `gorm:"size:100" json:"mime_type"`
	IsDirectory bool           `gorm:"default:false" json:"is_directory"`
	EmbeddingID string         `gorm:"size:100" json:"embedding_id"` // 关联的向量ID
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`

	// 关联
	User   User   `gorm:"foreignKey:UserID" json:"-"`
	Parent *File  `gorm:"foreignKey:ParentID" json:"parent,omitempty"`
	Files  []File `gorm:"foreignKey:ParentID" json:"files,omitempty"`
}

type FileChunk struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	UserID         uint      `gorm:"not null;index" json:"user_id"`
	UploadID       string    `gorm:"size:100;not null;index" json:"upload_id"`
	FileName       string    `gorm:"size:255;not null" json:"file_name"`
	FileHash       string    `gorm:"size:64" json:"file_hash"`
	TotalSize      int64     `gorm:"default:0" json:"total_size"`
	ChunkSize      int       `gorm:"default:0" json:"chunk_size"`
	UploadedChunks string    `gorm:"type:text" json:"uploaded_chunks"`      // JSON数组
	Status         string    `gorm:"size:20;default:pending" json:"status"` // pending, uploading, completed, failed
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	User User `gorm:"foreignKey:UserID" json:"-"`
}

// GetUploadedChunks 获取已上传的分片列表
func (fc *FileChunk) GetUploadedChunks() ([]int, error) {
	var chunks []int
	if err := json.Unmarshal([]byte(fc.UploadedChunks), &chunks); err != nil {
		return nil, err
	}
	return chunks, nil
}

// SetUploadedChunks 设置已上传的分片列表
func (fc *FileChunk) SetUploadedChunks(chunks []int) error {
	data, err := json.Marshal(chunks)
	if err != nil {
		return err
	}
	fc.UploadedChunks = string(data)
	return nil
}

// AddUploadedChunk 添加已上传的分片
func (fc *FileChunk) AddUploadedChunk(chunkIndex int) error {
	chunks, err := fc.GetUploadedChunks()
	if err != nil {
		return err
	}

	// 检查是否已存在
	for _, c := range chunks {
		if c == chunkIndex {
			return nil
		}
	}

	chunks = append(chunks, chunkIndex)
	return fc.SetUploadedChunks(chunks)
}

// IsAllUploaded 检查所有分片是否已上传
func (fc *FileChunk) IsAllUploaded(totalChunks int) bool {
	chunks, err := fc.GetUploadedChunks()
	if err != nil {
		return false
	}
	return len(chunks) == totalChunks
}
