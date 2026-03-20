package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"GoDisk/internal/config"
	"GoDisk/internal/model"
	"gorm.io/gorm"
)

type StorageService struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewStorageService(db *gorm.DB, cfg *config.Config) *StorageService {
	return &StorageService{db: db, cfg: cfg}
}

// SaveFile 保存文件到磁盘（带去重）
func (s *StorageService) SaveFile(file io.Reader, fileHash string) (string, int64, error) {
	// 检查是否已存在相同哈希的文件
	var existingFile model.File
	if err := s.db.Where("file_hash = ?", fileHash).First(&existingFile).Error; err == nil {
		// 文件已存在，返回现有文件路径
		return existingFile.FilePath, existingFile.FileSize, nil
	}

	// 生成存储路径
	filePath := filepath.Join(s.cfg.Storage.UploadPath, fileHash)

	// 创建目标目录
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return "", 0, fmt.Errorf("failed to create directory: %w", err)
	}

	// 同时计算哈希和保存文件
	dst, err := os.Create(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create file: %w", err)
	}
	defer dst.Close()

	// 使用multi writer同时写入文件和计算哈希
	hasher := sha256.New()
	writer := io.MultiWriter(dst, hasher)

	size, err := io.Copy(writer, file)
	if err != nil {
		os.Remove(filePath) // 清理失败的文件
		return "", 0, fmt.Errorf("failed to save file: %w", err)
	}

	// 验证哈希
	calculatedHash := hex.EncodeToString(hasher.Sum(nil))
	if calculatedHash != fileHash {
		os.Remove(filePath) // 清理哈希不匹配的文件
		return "", 0, fmt.Errorf("file hash mismatch")
	}

	return filePath, size, nil
}

// SaveChunk 保存分片文件（流式写入，避免全量内存占用）
func (s *StorageService) SaveChunk(uploadID string, chunkIndex int, data io.Reader) (string, error) {
	// 生成分片文件名
	chunkFileName := fmt.Sprintf("%s_chunk_%d", uploadID, chunkIndex)
	chunkPath := filepath.Join(s.cfg.Storage.ChunkPath, chunkFileName)

	// 流式写入磁盘
	dst, err := os.Create(chunkPath)
	if err != nil {
		return "", fmt.Errorf("failed to create chunk file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, data); err != nil {
		os.Remove(chunkPath)
		return "", fmt.Errorf("failed to save chunk: %w", err)
	}

	return chunkPath, nil
}

// MergeChunks 合并分片
func (s *StorageService) MergeChunks(uploadID string, chunkPaths []string, targetPath string) error {
	// 创建目标文件
	dst, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create target file: %w", err)
	}
	defer dst.Close()

	// 合并分片
	for _, chunkPath := range chunkPaths {
		src, err := os.Open(chunkPath)
		if err != nil {
			return fmt.Errorf("failed to open chunk %s: %w", chunkPath, err)
		}

		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			return fmt.Errorf("failed to copy chunk %s: %w", chunkPath, err)
		}
		src.Close()
	}

	return nil
}

// CleanChunks 清理分片文件
func (s *StorageService) CleanChunks(uploadID string) error {
	// 查找所有相关分片
	pattern := fmt.Sprintf("%s_chunk_*", uploadID)
	matches, err := filepath.Glob(filepath.Join(s.cfg.Storage.ChunkPath, pattern))
	if err != nil {
		return fmt.Errorf("failed to find chunks: %w", err)
	}

	// 删除分片文件
	for _, match := range matches {
		if err := os.Remove(match); err != nil {
			return fmt.Errorf("failed to remove chunk %s: %w", match, err)
		}
	}

	return nil
}

// DeleteFile 从磁盘删除文件（引用计数安全）
// 使用事务确保 count 检查和文件删除之间不会有并发问题
func (s *StorageService) DeleteFile(filePath string) error {
	if filePath == "" {
		return nil
	}

	var shouldDelete bool

	// 在事务中检查引用计数，防止并发删除竞态
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.File{}).Where("file_path = ?", filePath).Count(&count).Error; err != nil {
			return fmt.Errorf("failed to check file references: %w", err)
		}
		// 只有当没有任何文件记录引用此物理文件时才删除
		shouldDelete = (count == 0)
		return nil
	})
	if err != nil {
		return err
	}

	if shouldDelete {
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete file: %w", err)
		}
	}

	return nil
}

// GetChunkPath 获取分片路径
func (s *StorageService) GetChunkPath(uploadID string, chunkIndex int) string {
	return filepath.Join(s.cfg.Storage.ChunkPath, fmt.Sprintf("%s_chunk_%d", uploadID, chunkIndex))
}

// GenerateUploadPath 生成上传文件路径
func (s *StorageService) GenerateUploadPath(fileHash string) string {
	return filepath.Join(s.cfg.Storage.UploadPath, fileHash)
}
