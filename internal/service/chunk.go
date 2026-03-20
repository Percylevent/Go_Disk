package service

import (
	"errors"
	"fmt"
	"io"
	"os"

	"GoDisk/internal/config"
	"GoDisk/internal/model"
	"GoDisk/internal/pkg/hash"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ChunkService struct {
	db         *gorm.DB
	cfg        *config.Config
	storageSvc *StorageService
	fileSvc    *FileService
}

func NewChunkService(db *gorm.DB, cfg *config.Config, storageSvc *StorageService, fileSvc *FileService) *ChunkService {
	return &ChunkService{
		db:         db,
		cfg:        cfg,
		storageSvc: storageSvc,
		fileSvc:    fileSvc,
	}
}

// InitChunkUpload 初始化分片上传
type InitChunkUploadRequest struct {
	FileName  string `json:"file_name" binding:"required"`
	FileSize  int64  `json:"file_size" binding:"required"`
	FileHash  string `json:"file_hash"`
	ChunkSize int    `json:"chunk_size"`
	ParentID  uint   `json:"parent_id"`
}

// InitChunkUploadResponse 初始化分片上传响应
type InitChunkUploadResponse struct {
	UploadID       string `json:"upload_id"`
	ChunkSize      int    `json:"chunk_size"`
	TotalChunks    int    `json:"total_chunks"`
	UploadedChunks []int  `json:"uploaded_chunks"`
}

// InitUpload 初始化分片上传
func (s *ChunkService) InitUpload(userID uint, req *InitChunkUploadRequest) (*InitChunkUploadResponse, error) {
	// 检查用户存储空间
	var user model.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if !user.CanUpload(req.FileSize) {
		return nil, errors.New("storage limit exceeded")
	}

	// 计算分片数量
	chunkSize := int64(s.cfg.Upload.ChunkSize)
	if req.ChunkSize > 0 {
		chunkSize = int64(req.ChunkSize)
	}
	totalChunks := int((req.FileSize + chunkSize - 1) / chunkSize)

	// 检查是否已有相同的上传记录
	var existingChunk model.FileChunk
	if err := s.db.Where("user_id = ? AND file_hash = ? AND status IN (?)",
		userID, req.FileHash, []string{"pending", "uploading"}).First(&existingChunk).Error; err == nil {
		// 已存在未完成的上传
		uploadedChunks, _ := existingChunk.GetUploadedChunks()
		return &InitChunkUploadResponse{
			UploadID:       existingChunk.UploadID,
			ChunkSize:      existingChunk.ChunkSize,
			TotalChunks:    totalChunks,
			UploadedChunks: uploadedChunks,
		}, nil
	}

	// 生成上传ID
	uploadID := generateUploadID(userID, req.FileName)

	// 创建分片上传记录
	chunkRecord := &model.FileChunk{
		UserID:         userID,
		UploadID:       uploadID,
		FileName:       req.FileName,
		FileHash:       req.FileHash,
		TotalSize:      req.FileSize,
		ChunkSize:      int(chunkSize),
		UploadedChunks: "[]",
		Status:         "pending",
	}

	if err := s.db.Create(chunkRecord).Error; err != nil {
		return nil, fmt.Errorf("failed to create chunk record: %w", err)
	}

	return &InitChunkUploadResponse{
		UploadID:       uploadID,
		ChunkSize:      int(chunkSize),
		TotalChunks:    totalChunks,
		UploadedChunks: []int{},
	}, nil
}

// UploadChunk 上传分片
func (s *ChunkService) UploadChunk(userID uint, uploadID string, chunkIndex int, chunkData io.Reader) error {
	// 获取上传记录
	var chunkRecord model.FileChunk
	if err := s.db.Where("upload_id = ? AND user_id = ?", uploadID, userID).First(&chunkRecord).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("upload record not found")
		}
		return fmt.Errorf("failed to get chunk record: %w", err)
	}

	// 检查分片是否已上传
	uploadedChunks, err := chunkRecord.GetUploadedChunks()
	if err != nil {
		return fmt.Errorf("failed to get uploaded chunks: %w", err)
	}

	for _, c := range uploadedChunks {
		if c == chunkIndex {
			return nil // 已上传，跳过
		}
	}

	// 流式保存分片到磁盘
	if _, err := s.storageSvc.SaveChunk(uploadID, chunkIndex, chunkData); err != nil {
		return fmt.Errorf("failed to save chunk: %w", err)
	}

	// 更新已上传分片列表
	if err := chunkRecord.AddUploadedChunk(chunkIndex); err != nil {
		return fmt.Errorf("failed to add uploaded chunk: %w", err)
	}

	// 更新状态
	chunkRecord.Status = "uploading"
	if err := s.db.Save(&chunkRecord).Error; err != nil {
		return fmt.Errorf("failed to update chunk record: %w", err)
	}

	return nil
}

// CompleteUpload 完成分片上传
func (s *ChunkService) CompleteUpload(userID uint, uploadID string, parentID uint) (*model.File, error) {
	// 获取上传记录
	var chunkRecord model.FileChunk
	if err := s.db.Where("upload_id = ? AND user_id = ?", uploadID, userID).First(&chunkRecord).Error; err != nil {
		return nil, errors.New("upload record not found")
	}

	// 计算总分片数
	totalChunks := (chunkRecord.TotalSize + int64(chunkRecord.ChunkSize) - 1) / int64(chunkRecord.ChunkSize)

	// 检查所有分片是否已上传
	uploadedChunks, err := chunkRecord.GetUploadedChunks()
	if err != nil {
		return nil, fmt.Errorf("failed to get uploaded chunks: %w", err)
	}

	if len(uploadedChunks) != int(totalChunks) {
		return nil, errors.New("not all chunks uploaded")
	}

	// 收集所有分片路径
	var chunkPaths []string
	for i := 0; i < int(totalChunks); i++ {
		chunkPaths = append(chunkPaths, s.storageSvc.GetChunkPath(uploadID, i))
	}

	// 合并分片
	filePath := s.storageSvc.GenerateUploadPath(chunkRecord.FileHash)
	if err := s.storageSvc.MergeChunks(uploadID, chunkPaths, filePath); err != nil {
		return nil, fmt.Errorf("failed to merge chunks: %w", err)
	}

	// 验证文件哈希
	if chunkRecord.FileHash != "" {
		calculatedHash, err := hash.CalculateFileSHA256(filePath)
		if err != nil {
			os.Remove(filePath)
			s.storageSvc.CleanChunks(uploadID)
			return nil, fmt.Errorf("failed to calculate file hash: %w", err)
		}

		if calculatedHash != chunkRecord.FileHash {
			os.Remove(filePath)
			s.storageSvc.CleanChunks(uploadID)
			return nil, errors.New("file hash mismatch")
		}
	}

	// 获取文件大小
	_, err = os.Stat(filePath)
	if err != nil {
		os.Remove(filePath)
		s.storageSvc.CleanChunks(uploadID)
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// 检查父文件夹
	if parentID != 0 {
		var parent model.File
		if err := s.db.Where("id = ? AND user_id = ? AND is_directory = ?", parentID, userID, true).First(&parent).Error; err != nil {
			os.Remove(filePath)
			s.storageSvc.CleanChunks(uploadID)
			return nil, errors.New("parent folder not found")
		}
	}

	// 创建文件记录
	fileRecord := &model.File{
		UserID:      userID,
		ParentID:    parentID,
		FileName:    chunkRecord.FileName,
		FilePath:    filePath,
		FileHash:    chunkRecord.FileHash,
		FileSize:    chunkRecord.TotalSize,
		IsDirectory: false,
	}

	if err := s.db.Create(fileRecord).Error; err != nil {
		os.Remove(filePath)
		s.storageSvc.CleanChunks(uploadID)
		return nil, fmt.Errorf("failed to create file record: %w", err)
	}

	// 更新上传记录状态
	chunkRecord.Status = "completed"
	s.db.Save(&chunkRecord)

	// 清理分片文件
	s.storageSvc.CleanChunks(uploadID)

	// 更新用户存储使用量
	var user model.User
	if err := s.db.First(&user, userID).Error; err == nil {
		user.AddStorage(chunkRecord.TotalSize)
		s.db.Save(&user)
	}

	return fileRecord, nil
}

// GetUploadProgress 获取上传进度
func (s *ChunkService) GetUploadProgress(userID uint, uploadID string) (*model.FileChunk, error) {
	var chunkRecord model.FileChunk
	if err := s.db.Where("upload_id = ? AND user_id = ?", uploadID, userID).First(&chunkRecord).Error; err != nil {
		return nil, errors.New("upload record not found")
	}
	return &chunkRecord, nil
}

// CancelUpload 取消上传
func (s *ChunkService) CancelUpload(userID uint, uploadID string) error {
	// 获取上传记录
	var chunkRecord model.FileChunk
	if err := s.db.Where("upload_id = ? AND user_id = ?", uploadID, userID).First(&chunkRecord).Error; err != nil {
		return errors.New("upload record not found")
	}

	// 清理分片文件
	s.storageSvc.CleanChunks(uploadID)

	// 更新状态
	chunkRecord.Status = "failed"
	if err := s.db.Save(&chunkRecord).Error; err != nil {
		return fmt.Errorf("failed to update chunk record: %w", err)
	}

	return nil
}

// generateUploadID 生成上传ID（使用UUID确保唯一性和安全性）
func generateUploadID(userID uint, fileName string) string {
	return uuid.New().String()
}
