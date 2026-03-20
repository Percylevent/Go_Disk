package service

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"GoDisk/internal/config"
	"GoDisk/internal/model"
	"GoDisk/internal/pkg/hash"

	"gorm.io/gorm"
)

type FileService struct {
	db         *gorm.DB
	cfg        *config.Config
	storageSvc *StorageService
}

func NewFileService(db *gorm.DB, cfg *config.Config, storageSvc *StorageService) *FileService {
	return &FileService{
		db:         db,
		cfg:        cfg,
		storageSvc: storageSvc,
	}
}

// UploadRequest 上传请求
type UploadRequest struct {
	FileName string `json:"file_name" binding:"required"`
	ParentID uint   `json:"parent_id"`
}

// FileListResponse 文件列表响应
type FileListResponse struct {
	ID          uint   `json:"id"`
	FileName    string `json:"file_name"`
	FileSize    int64  `json:"file_size"`
	MimeType    string `json:"mime_type"`
	IsDirectory bool   `json:"is_directory"`
	EmbeddingID string `json:"embedding_id"` // 语义索引ID
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// UploadFile 上传文件
func (s *FileService) UploadFile(userID uint, fileName string, parentID uint, file io.Reader, fileSize int64, mimeType string) (*model.File, error) {
	// 检查用户存储空间
	var user model.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if !user.CanUpload(fileSize) {
		return nil, errors.New("storage limit exceeded")
	}

	// 计算文件哈希
	fileHash, err := hash.CalculateReaderSHA256(file)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate file hash: %w", err)
	}

	// 重置reader（如果支持）
	type seeker interface {
		Seek(offset int64, whence int) (int64, error)
	}
	if s, ok := file.(seeker); ok {
		s.Seek(0, io.SeekStart)
	}

	// 检查父文件夹是否存在
	if parentID != 0 {
		var parent model.File
		if err := s.db.Where("id = ? AND user_id = ? AND is_directory = ?", parentID, userID, true).First(&parent).Error; err != nil {
			return nil, errors.New("parent folder not found")
		}
	}

	// 保存文件
	filePath, savedSize, err := s.storageSvc.SaveFile(file, fileHash)
	if err != nil {
		return nil, fmt.Errorf("failed to save file: %w", err)
	}

	// 在事务中执行数据库操作：去重检查 + 创建记录 + 更新配额
	var fileRecord *model.File
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// 检查是否已经上传过相同文件
		var existingFile model.File
		if err := tx.Where("user_id = ? AND file_hash = ? AND parent_id = ?", userID, fileHash, parentID).First(&existingFile).Error; err == nil {
			fileRecord = &existingFile
			return nil
		}

		// 创建文件记录
		fileRecord = &model.File{
			UserID:      userID,
			ParentID:    parentID,
			FileName:    fileName,
			FilePath:    filePath,
			FileHash:    fileHash,
			FileSize:    savedSize,
			MimeType:    mimeType,
			IsDirectory: false,
		}

		if err := tx.Create(fileRecord).Error; err != nil {
			return fmt.Errorf("failed to create file record: %w", err)
		}

		// 原子更新用户存储使用量
		if err := tx.Model(&model.User{}).Where("id = ?", userID).
			Update("storage_used", gorm.Expr("storage_used + ?", savedSize)).Error; err != nil {
			return fmt.Errorf("failed to update storage: %w", err)
		}

		return nil
	})

	if err != nil {
		s.storageSvc.DeleteFile(filePath)
		return nil, err
	}

	return fileRecord, nil
}

// DownloadFile 下载文件
func (s *FileService) DownloadFile(fileID, userID uint) (*model.File, *os.File, error) {
	// 获取文件信息
	var file model.File
	if err := s.db.Where("id = ? AND user_id = ?", fileID, userID).First(&file).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errors.New("file not found")
		}
		return nil, nil, fmt.Errorf("failed to get file: %w", err)
	}

	// 打开文件
	f, err := os.Open(file.FilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open file: %w", err)
	}

	return &file, f, nil
}

// ListFiles 列出文件
func (s *FileService) ListFiles(userID, parentID uint, page, size int) ([]*FileListResponse, int64, error) {
	var files []*model.File
	var total int64

	offset := (page - 1) * size

	// 查询总数
	if err := s.db.Model(&model.File{}).Where("user_id = ? AND parent_id = ?", userID, parentID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count files: %w", err)
	}

	// 查询文件列表
	if err := s.db.Where("user_id = ? AND parent_id = ?", userID, parentID).
		Order("is_directory DESC, created_at DESC").
		Limit(size).
		Offset(offset).
		Find(&files).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list files: %w", err)
	}

	// 转换为响应格式
	result := make([]*FileListResponse, len(files))
	for i, f := range files {
		result[i] = &FileListResponse{
			ID:          f.ID,
			FileName:    f.FileName,
			FileSize:    f.FileSize,
			MimeType:    f.MimeType,
			IsDirectory: f.IsDirectory,
			EmbeddingID: f.EmbeddingID, // 添加语义索引ID
			CreatedAt:   f.CreatedAt.Format("2006-01-02 15:04:05"),
			UpdatedAt:   f.UpdatedAt.Format("2006-01-02 15:04:05"),
		}
	}

	return result, total, nil
}

// CreateFolder 创建文件夹
func (s *FileService) CreateFolder(userID uint, folderName string, parentID uint) (*model.File, error) {
	// 检查同名文件夹是否存在
	var count int64
	if err := s.db.Model(&model.File{}).Where("user_id = ? AND parent_id = ? AND file_name = ? AND is_directory = ?",
		userID, parentID, folderName, true).Count(&count).Error; err != nil {
		return nil, fmt.Errorf("failed to check folder: %w", err)
	}
	if count > 0 {
		return nil, errors.New("folder already exists")
	}

	// 检查父文件夹是否存在
	if parentID != 0 {
		var parent model.File
		if err := s.db.Where("id = ? AND user_id = ? AND is_directory = ?", parentID, userID, true).First(&parent).Error; err != nil {
			return nil, errors.New("parent folder not found")
		}
	}

	folder := &model.File{
		UserID:      userID,
		ParentID:    parentID,
		FileName:    folderName,
		FilePath:    "",
		FileHash:    "",
		FileSize:    0,
		IsDirectory: true,
	}

	if err := s.db.Create(folder).Error; err != nil {
		return nil, fmt.Errorf("failed to create folder: %w", err)
	}

	return folder, nil
}

// DeleteFile 删除文件
func (s *FileService) DeleteFile(fileID, userID uint) error {
	// 获取文件信息
	var file model.File
	if err := s.db.Where("id = ? AND user_id = ?", fileID, userID).First(&file).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("file not found")
		}
		return fmt.Errorf("failed to get file: %w", err)
	}

	// 在事务中执行删除操作
	return s.db.Transaction(func(tx *gorm.DB) error {
		var totalSize int64

		if file.IsDirectory {
			// 递归收集所有子文件信息并删除
			filePaths, size, err := s.collectAndDeleteFolder(tx, &file)
			if err != nil {
				return fmt.Errorf("failed to delete folder: %w", err)
			}
			totalSize = size

			// 事务提交后再删除物理文件（通过延迟删除列表）
			for _, fp := range filePaths {
				s.storageSvc.DeleteFile(fp)
			}
		} else {
			totalSize = file.FileSize
			// 删除物理文件
			if err := s.storageSvc.DeleteFile(file.FilePath); err != nil {
				return fmt.Errorf("failed to delete physical file: %w", err)
			}
		}

		// 删除数据库记录
		if err := tx.Delete(&file).Error; err != nil {
			return fmt.Errorf("failed to delete file record: %w", err)
		}

		// 一次性更新用户存储使用量
		if totalSize > 0 {
			if err := tx.Model(&model.User{}).Where("id = ?", userID).
				Update("storage_used", gorm.Expr("CASE WHEN storage_used >= ? THEN storage_used - ? ELSE 0 END", totalSize, totalSize)).Error; err != nil {
				return fmt.Errorf("failed to update storage: %w", err)
			}
		}

		return nil
	})
}

// collectAndDeleteFolder 递归收集文件夹下所有文件路径和总大小，并删除数据库记录
func (s *FileService) collectAndDeleteFolder(tx *gorm.DB, folder *model.File) ([]string, int64, error) {
	var children []*model.File
	if err := tx.Where("parent_id = ?", folder.ID).Find(&children).Error; err != nil {
		return nil, 0, err
	}

	var filePaths []string
	var totalSize int64

	for _, child := range children {
		if child.IsDirectory {
			paths, size, err := s.collectAndDeleteFolder(tx, child)
			if err != nil {
				return nil, 0, err
			}
			filePaths = append(filePaths, paths...)
			totalSize += size
		} else {
			filePaths = append(filePaths, child.FilePath)
			totalSize += child.FileSize
		}
		// 删除数据库记录
		if err := tx.Delete(child).Error; err != nil {
			return nil, 0, err
		}
	}

	return filePaths, totalSize, nil
}

// MoveFile 移动文件
func (s *FileService) MoveFile(fileID, userID, targetParentID uint) error {
	// 获取文件信息
	var file model.File
	if err := s.db.Where("id = ? AND user_id = ?", fileID, userID).First(&file).Error; err != nil {
		return errors.New("file not found")
	}

	// 检查目标父文件夹是否存在
	if targetParentID != 0 {
		var parent model.File
		if err := s.db.Where("id = ? AND user_id = ? AND is_directory = ?", targetParentID, userID, true).First(&parent).Error; err != nil {
			return errors.New("target folder not found")
		}

		// 不能移动到自己
		if targetParentID == file.ID {
			return errors.New("cannot move to itself")
		}

		// 如果是文件夹，检查是否移动到自己的子文件夹（防止循环引用）
		if file.IsDirectory {
			currentID := targetParentID
			for currentID != 0 {
				if currentID == file.ID {
					return errors.New("cannot move folder into its own subfolder")
				}
				var ancestor model.File
				if err := s.db.Select("parent_id").First(&ancestor, currentID).Error; err != nil {
					break
				}
				currentID = ancestor.ParentID
			}
		}
	}

	// 更新父文件夹
	return s.db.Model(&file).Update("parent_id", targetParentID).Error
}

// RenameFile 重命名文件
func (s *FileService) RenameFile(fileID, userID uint, newName string) error {
	// 获取文件信息
	var file model.File
	if err := s.db.Where("id = ? AND user_id = ?", fileID, userID).First(&file).Error; err != nil {
		return errors.New("file not found")
	}

	// 检查同名文件是否存在
	var count int64
	if err := s.db.Model(&model.File{}).Where("user_id = ? AND parent_id = ? AND file_name = ? AND id != ?",
		userID, file.ParentID, newName, fileID).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check file: %w", err)
	}
	if count > 0 {
		return errors.New("file with same name already exists")
	}

	// 更新文件名
	return s.db.Model(&file).Update("file_name", newName).Error
}

// SearchFiles 搜索文件
func (s *FileService) SearchFiles(userID uint, keyword string, page, size int) ([]*FileListResponse, int64, error) {
	var files []*model.File
	var total int64

	offset := (page - 1) * size
	searchPattern := "%" + keyword + "%"

	// 查询总数
	if err := s.db.Model(&model.File{}).Where("user_id = ? AND file_name LIKE ?", userID, searchPattern).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count files: %w", err)
	}

	// 查询文件列表
	if err := s.db.Where("user_id = ? AND file_name LIKE ?", userID, searchPattern).
		Order("created_at DESC").
		Limit(size).
		Offset(offset).
		Find(&files).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to search files: %w", err)
	}

	// 转换为响应格式
	result := make([]*FileListResponse, len(files))
	for i, f := range files {
		result[i] = &FileListResponse{
			ID:          f.ID,
			FileName:    f.FileName,
			FileSize:    f.FileSize,
			MimeType:    f.MimeType,
			IsDirectory: f.IsDirectory,
			EmbeddingID: f.EmbeddingID, // 添加语义索引ID
			CreatedAt:   f.CreatedAt.Format("2006-01-02 15:04:05"),
			UpdatedAt:   f.UpdatedAt.Format("2006-01-02 15:04:05"),
		}
	}

	return result, total, nil
}

// DownloadFolder 下载文件夹（打包成ZIP）
func (s *FileService) DownloadFolder(folder *model.File, userID uint, writer io.Writer) error {
	// 创建ZIP writer
	zipWriter := zip.NewWriter(writer)
	defer zipWriter.Close()

	// 递归添加文件夹内容到ZIP
	basePath := folder.FileName
	err := s.addFolderToZip(zipWriter, folder, basePath, userID)
	if err != nil {
		return fmt.Errorf("failed to add folder to zip: %w", err)
	}

	return nil
}

// addFolderToZip 递归添加文件夹内容到ZIP
func (s *FileService) addFolderToZip(zipWriter *zip.Writer, folder *model.File, basePath string, userID uint) error {
	// 查找所有子文件和子文件夹
	var children []*model.File
	if err := s.db.Where("parent_id = ? AND user_id = ?", folder.ID, userID).Find(&children).Error; err != nil {
		return err
	}

	for _, child := range children {
		// 构建ZIP内的路径
		zipPath := path.Join(basePath, child.FileName)

		if child.IsDirectory {
			// 对于文件夹，创建目录项并递归处理
			_, err := zipWriter.Create(zipPath + "/")
			if err != nil {
				return fmt.Errorf("failed to create folder entry in zip: %w", err)
			}
			// 递归处理子文件夹
			if err := s.addFolderToZip(zipWriter, child, zipPath, userID); err != nil {
				return err
			}
		} else {
			// 对于文件，添加到ZIP
			if err := s.addFileToZip(zipWriter, child, zipPath); err != nil {
				return fmt.Errorf("failed to add file %s to zip: %w", child.FileName, err)
			}
		}
	}

	return nil
}

// addFileToZip 添加单个文件到ZIP
func (s *FileService) addFileToZip(zipWriter *zip.Writer, file *model.File, zipPath string) error {
	// 打开源文件
	srcFile, err := os.Open(file.FilePath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// 创建ZIP内的文件
	writer, err := zipWriter.Create(zipPath)
	if err != nil {
		return err
	}

	// 复制文件内容
	_, err = io.Copy(writer, srcFile)
	return err
}

// GetDB 获取数据库实例
func (s *FileService) GetDB() *gorm.DB {
	return s.db
}

// DownloadFileByPath 通过文件路径打开文件（供分享下载使用）
func (s *FileService) DownloadFileByPath(filePath string) (*os.File, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	return f, nil
}
