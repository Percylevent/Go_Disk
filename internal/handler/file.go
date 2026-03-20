package handler

import (
	"GoDisk/internal/middleware"
	"GoDisk/internal/model"
	"GoDisk/internal/pkg/response"
	"GoDisk/internal/service"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type FileHandler struct {
	fileSvc  *service.FileService
	chunkSvc *service.ChunkService
	embSvc   service.EmbeddingService
}

func NewFileHandler(fileSvc *service.FileService, chunkSvc *service.ChunkService, embSvc service.EmbeddingService) *FileHandler {
	return &FileHandler{
		fileSvc:  fileSvc,
		chunkSvc: chunkSvc,
		embSvc:   embSvc,
	}
}

// UploadFile 单文件上传
// @Summary 单文件上传
// @Description 上传单个文件
// @Tags files
// @Accept multipart/form-data
// @Produce json
// @Security Bearer
// @Param file formData file true "文件"
// @Param parent_id formData int false "父文件夹ID"
// @Success 200 {object} response.Response{data=model.File}
// @Router /api/files/upload [post]
func (h *FileHandler) UploadFile(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "file is required")
		return
	}

	parentIDStr := c.PostForm("parent_id")
	parentID := uint(0)
	if parentIDStr != "" {
		pid64, _ := strconv.ParseUint(parentIDStr, 10, 32)
		if pid64 > 0 {
			parentID = uint(pid64)
		}
	}

	file, err := fileHeader.Open()
	if err != nil {
		response.UploadFailed(c, err.Error())
		return
	}
	defer file.Close()

	fileRecord, err := h.fileSvc.UploadFile(userID, fileHeader.Filename, uint(parentID), file, fileHeader.Size, fileHeader.Header.Get("Content-Type"))
	if err != nil {
		response.UploadFailed(c, err.Error())
		return
	}

	// 异步生成embedding
	go h.embSvc.CreateFileEmbedding(fileRecord.ID)

	response.Success(c, fileRecord)
}

// InitChunkUpload 初始化分片上传
// @Summary 初始化分片上传
// @Description 初始化分片上传，获取upload_id
// @Tags files
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body service.InitChunkUploadRequest true "上传信息"
// @Success 200 {object} response.Response{data=service.InitChunkUploadResponse}
// @Router /api/files/upload/chunk/init [post]
func (h *FileHandler) InitChunkUpload(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req service.InitChunkUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	resp, err := h.chunkSvc.InitUpload(userID, &req)
	if err != nil {
		response.UploadFailed(c, err.Error())
		return
	}

	response.Success(c, resp)
}

// UploadChunk 上传分片
// @Summary 上传分片
// @Description 上传单个分片
// @Tags files
// @Accept multipart/form-data
// @Produce json
// @Security Bearer
// @Param upload_id formData string true "上传ID"
// @Param chunk_index formData int true "分片索引"
// @Param chunk formData file true "分片文件"
// @Success 200 {object} response.Response
// @Router /api/files/upload/chunk [post]
func (h *FileHandler) UploadChunk(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	uploadID := c.PostForm("upload_id")
	if uploadID == "" {
		response.BadRequest(c, "upload_id is required")
		return
	}

	chunkIndexStr := c.PostForm("chunk_index")
	chunkIndex, err := strconv.Atoi(chunkIndexStr)
	if err != nil {
		response.BadRequest(c, "invalid chunk_index")
		return
	}

	chunkFile, err := c.FormFile("chunk")
	if err != nil {
		response.BadRequest(c, "chunk file is required")
		return
	}

	file, err := chunkFile.Open()
	if err != nil {
		response.UploadFailed(c, err.Error())
		return
	}
	defer file.Close()

	chunkData, err := io.ReadAll(file)
	if err != nil {
		response.UploadFailed(c, err.Error())
		return
	}

	if err := h.chunkSvc.UploadChunk(userID, uploadID, chunkIndex, chunkData); err != nil {
		response.UploadFailed(c, err.Error())
		return
	}

	response.Success(c, nil)
}

// CompleteUpload 完成分片上传
// @Summary 完成分片上传
// @Description 完成分片上传，合并所有分片
// @Tags files
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body map[string]string true "完成信息"
// @Success 200 {object} response.Response{data=model.File}
// @Router /api/files/upload/chunk/complete [post]
func (h *FileHandler) CompleteUpload(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req struct {
		UploadID string `json:"upload_id" binding:"required"`
		ParentID uint   `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	fileRecord, err := h.chunkSvc.CompleteUpload(userID, req.UploadID, req.ParentID)
	if err != nil {
		response.UploadFailed(c, err.Error())
		return
	}

	// 异步生成embedding
	go h.embSvc.CreateFileEmbedding(fileRecord.ID)

	response.Success(c, fileRecord)
}

// ListFiles 列出文件
// @Summary 列出文件
// @Description 获取文件列表，支持分页
// @Tags files
// @Produce json
// @Security Bearer
// @Param parent_id query int false "父文件夹ID"
// @Param page query int false "页码" default(1)
// @Param size query int false "每页数量" default(20)
// @Success 200 {object} response.Response{data=response.PageData}
// @Router /api/files/list [get]
func (h *FileHandler) ListFiles(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	parentIDStr := c.Query("parent_id")
	parentID := uint(0)
	if parentIDStr != "" {
		if pid, err := strconv.ParseUint(parentIDStr, 10, 32); err == nil {
			parentID = uint(pid)
		}
	}

	page := 1
	if pageStr := c.Query("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	size := 20
	if sizeStr := c.Query("size"); sizeStr != "" {
		if s, err := strconv.Atoi(sizeStr); err == nil && s > 0 {
			size = s
		}
	}

	files, total, err := h.fileSvc.ListFiles(userID, parentID, page, size)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Page(c, total, files, page, size)
}

// DownloadFile 下载文件
// @Summary 下载文件
// @Description 下载指定文件
// @Tags files
// @Produce application/octet-stream
// @Security Bearer
// @Param id path int true "文件ID"
// @Router /api/files/download/:id [get]
func (h *FileHandler) DownloadFile(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	fileIDStr := c.Param("id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 32)
	if err != nil {
		response.BadRequest(c, "invalid file id")
		return
	}

	// 检查是否是文件夹
	var folderRecord model.File
	if err := h.fileSvc.GetDB().Where("id = ? AND user_id = ?", uint(fileID), userID).First(&folderRecord).Error; err != nil {
		response.FileNotFound(c)
		return
	}

	if folderRecord.IsDirectory {
		// 文件夹下载，打包成ZIP
		zipName := folderRecord.FileName + ".zip"
		c.Header("Content-Disposition", "attachment; filename=\""+zipName+"\"")
		c.Header("Content-Type", "application/zip")
		err := h.fileSvc.DownloadFolder(&folderRecord, userID, c.Writer)
		if err != nil {
			response.InternalError(c, err.Error())
			return
		}
		return
	}

	fileRecord, file, err := h.fileSvc.DownloadFile(uint(fileID), userID)
	if err != nil {
		response.FileNotFound(c)
		return
	}
	defer file.Close()

	// 设置响应头
	c.Header("Content-Disposition", "attachment; filename=\""+fileRecord.FileName+"\"")
	c.Header("Content-Type", fileRecord.MimeType)
	c.Header("Content-Length", strconv.FormatInt(fileRecord.FileSize, 10))

	// 支持Range请求
	httpRange := c.GetHeader("Range")
	if httpRange != "" {
		// 解析Range header
		ranges := strings.TrimPrefix(httpRange, "bytes=")
		rangeParts := strings.Split(ranges, "-")
		if len(rangeParts) == 2 {
			start, _ := strconv.ParseInt(rangeParts[0], 10, 64)
			end := fileRecord.FileSize - 1
			if rangeParts[1] != "" {
				end, _ = strconv.ParseInt(rangeParts[1], 10, 64)
			}

			file.Seek(start, io.SeekStart)
			c.Header("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(fileRecord.FileSize, 10))
			c.Header("Content-Length", strconv.FormatInt(end-start+1, 10))
			c.Status(206) // Partial Content

			io.CopyN(c.Writer, file, end-start+1)
			return
		}
	}

	// 完整文件下载
	io.Copy(c.Writer, file)
}

// DeleteFile 删除文件
// @Summary 删除文件
// @Description 删除指定文件或文件夹
// @Tags files
// @Produce json
// @Security Bearer
// @Param id path int true "文件ID"
// @Success 200 {object} response.Response
// @Router /api/files/:id [delete]
func (h *FileHandler) DeleteFile(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	fileIDStr := c.Param("id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 32)
	if err != nil {
		response.BadRequest(c, "invalid file id")
		return
	}

	if err := h.fileSvc.DeleteFile(uint(fileID), userID); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, nil)
}

// CreateFolder 创建文件夹
// @Summary 创建文件夹
// @Description 创建新文件夹
// @Tags files
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body map[string]string true "文件夹信息"
// @Success 200 {object} response.Response{data=model.File}
// @Router /api/files/folder [post]
func (h *FileHandler) CreateFolder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req struct {
		FolderName string `json:"folder_name" binding:"required"`
		ParentID   uint   `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	folder, err := h.fileSvc.CreateFolder(userID, req.FolderName, req.ParentID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	// 异步生成文件夹embedding
	h.embSvc.AsyncEmbeddingTask(folder.ID, true, userID, "文件夹："+req.FolderName)

	response.Success(c, folder)
}

// MoveFile 移动文件
// @Summary 移动文件
// @Description 移动文件到指定文件夹
// @Tags files
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body map[string]interface{} true "移动信息"
// @Success 200 {object} response.Response
// @Router /api/files/move [put]
func (h *FileHandler) MoveFile(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req struct {
		FileID         uint `json:"file_id" binding:"required"`
		TargetParentID uint `json:"target_parent_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := h.fileSvc.MoveFile(req.FileID, userID, req.TargetParentID); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, nil)
}

// RenameFile 重命名文件
// @Summary 重命名文件
// @Description 重命名文件或文件夹
// @Tags files
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body map[string]string true "重命名信息"
// @Success 200 {object} response.Response
// @Router /api/files/rename [put]
func (h *FileHandler) RenameFile(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req struct {
		FileID  uint   `json:"file_id" binding:"required"`
		NewName string `json:"new_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := h.fileSvc.RenameFile(req.FileID, userID, req.NewName); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, nil)
}

// SearchFiles 搜索文件
// @Summary 搜索文件
// @Description 根据关键词搜索文件
// @Tags files
// @Produce json
// @Security Bearer
// @Param keyword query string true "搜索关键词"
// @Param page query int false "页码" default(1)
// @Param size query int false "每页数量" default(20)
// @Success 200 {object} response.Response{data=response.PageData}
// @Router /api/files/search [get]
func (h *FileHandler) SearchFiles(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	keyword := c.Query("keyword")
	if keyword == "" {
		response.BadRequest(c, "keyword is required")
		return
	}

	page := 1
	if pageStr := c.Query("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	size := 20
	if sizeStr := c.Query("size"); sizeStr != "" {
		if s, err := strconv.Atoi(sizeStr); err == nil && s > 0 {
			size = s
		}
	}

	// 优先使用embedding搜索
	modelFiles, err := h.embSvc.SearchByEmbedding(userID, keyword, size)

	if err != nil || (len(modelFiles.Files) == 0 && len(modelFiles.Folders) == 0) {
		// 降级到普通搜索
		listResponses, total, err := h.fileSvc.SearchFiles(userID, keyword, page, size)
		if err != nil {
			response.InternalError(c, err.Error())
			return
		}
		response.Page(c, total, listResponses, page, size)
		return
	}

	// 转换为响应格式 (返回混合结果，前端可自行渲染)
	total := int64(len(modelFiles.Files) + len(modelFiles.Folders))
	result := make([]*service.FileListResponse, 0, total)

	// 先添加文件夹
	for _, f := range modelFiles.Folders {
		result = append(result, &service.FileListResponse{
			ID:          f.ID,
			FileName:    f.FileName,
			FileSize:    f.FileSize,
			MimeType:    f.MimeType,
			IsDirectory: true,
			CreatedAt:   f.CreatedAt.Format("2006-01-02 15:04:05"),
			UpdatedAt:   f.UpdatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	// 后添加文件
	for _, f := range modelFiles.Files {
		result = append(result, &service.FileListResponse{
			ID:          f.ID,
			FileName:    f.FileName,
			FileSize:    f.FileSize,
			MimeType:    f.MimeType,
			IsDirectory: false,
			EmbeddingID: f.EmbeddingID, // 添加语义索引ID
			CreatedAt:   f.CreatedAt.Format("2006-01-02 15:04:05"),
			UpdatedAt:   f.UpdatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	response.Page(c, total, result, page, size)
}

// BuildIndex 手动触发语义索引构建
// @Summary 手动触发生法语义索引构建
// @Tags file
// @Produce json
// @Security Bearer
// @Param id path int true "文件/文件夹ID"
// @Success 200 {object} response.Response
// @Router /api/files/{id}/build_index [post]
func (h *FileHandler) BuildIndex(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	fileIDStr := c.Param("id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 32)
	if err != nil {
		response.BadRequest(c, "无效的文件ID")
		return
	}

	// 检查所有权
	var file model.File
	if err := h.embSvc.GetDB().Where("id = ? AND user_id = ?", uint(fileID), userID).First(&file).Error; err != nil {
		response.BadRequest(c, "文件不存在或无权限")
		return
	}

	// 后台异步构建索引
	if file.IsDirectory {
		// 收集文件夹下所有子文件名作为 summary，给 Qwen 有意义的语义信息
		var children []model.File
		h.embSvc.GetDB().Where("parent_id = ? AND is_directory = ?", file.ID, false).Find(&children)
		summary := "文件夹：" + file.FileName
		if len(children) > 0 {
			summary += "\n包含文件：\n"
			for _, ch := range children {
				summary += "  - " + ch.FileName + "\n"
			}
		}
		h.embSvc.AsyncEmbeddingTask(file.ID, true, userID, summary)
	} else {
		h.embSvc.AsyncEmbeddingTask(file.ID, false, userID, "")
	}

	response.Success(c, gin.H{
		"message": "已在后台启动语义索引构建任务",
	})
}
