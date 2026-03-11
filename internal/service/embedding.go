package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"GoDisk/internal/config"
	"GoDisk/internal/model"

	"github.com/gabriel-vasile/mimetype"
	"github.com/ledongthuc/pdf"
	"github.com/philippgille/chromem-go"
	"gorm.io/gorm"
)

type embeddingTask struct {
	fileID        uint
	isDir         bool
	userID        uint
	folderSummary string
}

type embeddingServiceImpl struct {
	db         *gorm.DB
	cfg        *config.Config
	client     *http.Client
	chromemDB  *chromem.DB
	collection *chromem.Collection
	taskQueue  chan embeddingTask
}

// NewEmbeddingService 创建 EmbeddingService
func NewEmbeddingService(db *gorm.DB, cfg *config.Config) EmbeddingService {
	// 初始化 chromem-go 内存/持久化数据库
	// 判断是放在内存还是持久化
	var chromemDB *chromem.DB
	var err error

	// 这里假设我们在 data 目录下持久化 embedding DB
	dbPath := filepath.Join("data", "chromem")
	err = os.MkdirAll(dbPath, 0755)
	if err != nil {
		log.Fatalf("Failed to create chromem db path: %v", err)
	}

	chromemDB, err = chromem.NewPersistentDB(dbPath, false)
	if err != nil {
		log.Fatalf("Failed to init chromem db: %v", err)
	}

	// 初始化或获取集合
	collectionName := "godisk_embeddings"
	collection := chromemDB.GetCollection(collectionName, nil)
	if collection == nil {
		collection, err = chromemDB.CreateCollection(collectionName, nil, nil)
		if err != nil {
			log.Fatalf("Failed to create chromem collection: %v", err)
		}
	}

	svc := &embeddingServiceImpl{
		db:         db,
		cfg:        cfg,
		client:     &http.Client{Timeout: 60 * time.Second}, // 考虑到长文本或多模态时间较长
		chromemDB:  chromemDB,
		collection: collection,
		taskQueue:  make(chan embeddingTask, 500), // 设置队列大小
	}

	svc.startWorkerPool()

	var interfaceSvc EmbeddingService = svc
	return interfaceSvc
}

func (s *embeddingServiceImpl) startWorkerPool() {
	// 启动固定数量的 worker，限制并发度为 2 (可以根据需要调整)
	concurrency := 2
	for i := 0; i < concurrency; i++ {
		go func(workerID int) {
			for task := range s.taskQueue {
				if task.isDir {
					s.CreateFolderEmbedding(task.fileID, task.userID, task.folderSummary)
				} else {
					s.CreateFileEmbedding(task.fileID)
				}
				// 任务之间稍微暂停防止 API 限流过快
				time.Sleep(500 * time.Millisecond)
			}
		}(i)
	}
}

func (s *embeddingServiceImpl) AsyncEmbeddingTask(fileID uint, isDir bool, userID uint, folderSummary string) {
	select {
	case s.taskQueue <- embeddingTask{fileID: fileID, isDir: isDir, userID: userID, folderSummary: folderSummary}:
		// 成功加入队列
	default:
		// 队列满了的情况，这里可以选择记录日志或者使用 goroutine 强行执行（有风险）
		log.Printf("[Warning] Embedding task queue is full, generating synchronously for fileID: %d\n", fileID)
		go func() {
			if isDir {
				s.CreateFolderEmbedding(fileID, userID, folderSummary)
			} else {
				s.CreateFileEmbedding(fileID)
			}
		}()
	}
}

// QwenContent Qwen API content item (Multimodal: text, image, video)
type QwenContent map[string]interface{}

// QwenEmbeddingRequest Qwen API请求
type QwenEmbeddingRequest struct {
	Model string `json:"model"`
	Input struct {
		Contents []QwenContent `json:"contents"`
	} `json:"input"`
	Parameters struct {
		Dimension  int     `json:"dimension"`
		OutputType string  `json:"output_type"`
		FPS        float64 `json:"fps,omitempty"`
	} `json:"parameters"`
}

// QwenEmbeddingItem 单个embedding结果
type QwenEmbeddingItem struct {
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
	Type      string    `json:"type"`
}

// QwenEmbeddingResponse Qwen API响应
type QwenEmbeddingResponse struct {
	Output struct {
		Embeddings []QwenEmbeddingItem `json:"embeddings"`
	} `json:"output"`
	Usage struct {
		InputTokens int `json:"input_tokens"`
		ImageTokens int `json:"image_tokens"`
		ImageCount  int `json:"image_count,omitempty"`
		Duration    int `json:"duration,omitempty"`
	} `json:"usage"`
	RequestID string `json:"request_id"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// SearchResult 搜索结果分类
type SearchResult struct {
	Folders []*model.File `json:"folders"`
	Files   []*model.File `json:"files"`
}

// EmbeddingService 定义 Embedding 服务的接口
type EmbeddingService interface {
	// GenerateMultimodalEmbedding 生成多模态融合 Embedding
	GenerateMultimodalEmbedding(contents []QwenContent) ([]float32, error)
	// ParseFileContent 根据文件类型智能解析并构建 QwenContent payload
	ParseFileContent(filePath, fileName string) ([]QwenContent, string, error)
	// CreateFileEmbedding 为具体文件创建或更新Embedding
	CreateFileEmbedding(fileID uint) error
	// CreateFolderEmbedding 为文件夹生成总体 Summary 的 Embedding
	CreateFolderEmbedding(folderID uint, userID uint, summaryText string) error
	// DeleteEmbedding 删除指定文件的语义向量 (处理 chromem-go 缺少独立删除 API 的情况)
	// chromem-go (v0.7.0) 目前不支持按 ID 删除单个 Document。
	// 为了确保数据一致性，我们的策略是：
	// 1. 删除文件时，数据库中对应记录会被物理/软删除。
	// 2. 搜索（SearchByEmbedding）的后续逻辑会带着 chromem 返回的 id 查 DB。
	// 3. 因为 DB 记录已被删除，`s.db.First(&file, fid)` 会查询失败，因此搜索结果中不会出现已删除的文件。
	// 总结：向量库里存在"孤儿"向量，但不影响业务逻辑或数据泄露。
	// 定期可以通过全量构建（清空 collection 重建）来垃圾回收节省内存。
	DeleteEmbedding(fileID uint, isDirectory bool) error
	// SearchByEmbedding 两阶段搜索架构：文件夹和具体文件
	SearchByEmbedding(userID uint, query string, limit int) (*SearchResult, error)
	GetDB() *gorm.DB

	// AsyncEmbeddingTask 异步提交Embedding任务
	AsyncEmbeddingTask(fileID uint, isDir bool, userID uint, folderSummary string)
}

// GenerateMultimodalEmbedding 生成多模态融合 Embedding
func (s *embeddingServiceImpl) GenerateMultimodalEmbedding(contents []QwenContent) ([]float32, error) {
	if !s.cfg.Qwen.Enabled {
		return nil, errors.New("embedding service is disabled")
	}

	reqBody := QwenEmbeddingRequest{
		Model: s.cfg.Qwen.Model,
	}
	reqBody.Input.Contents = contents
	reqBody.Parameters.Dimension = 1024
	reqBody.Parameters.OutputType = "dense"

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", s.cfg.Qwen.APIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.Qwen.APIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var qwenResp QwenEmbeddingResponse
	if err := json.Unmarshal(body, &qwenResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if qwenResp.Code != "" {
		return nil, fmt.Errorf("API error (code=%s): %s", qwenResp.Code, qwenResp.Message)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	if len(qwenResp.Output.Embeddings) == 0 {
		return nil, errors.New("no embedding returned")
	}

	// Chromem requires float32
	float64Vec := qwenResp.Output.Embeddings[0].Embedding
	float32Vec := make([]float32, len(float64Vec))
	for i, v := range float64Vec {
		float32Vec[i] = float32(v)
	}

	return float32Vec, nil
}

// ParseFileContent 根据文件类型智能解析并构建 QwenContent payload
func (s *embeddingServiceImpl) ParseFileContent(filePath, fileName string) ([]QwenContent, string, error) {
	mtype, err := mimetype.DetectFile(filePath)
	if err != nil {
		return nil, "", err
	}

	ext := strings.ToLower(filepath.Ext(fileName))
	mimeStr := mtype.String()

	// 1. Image Files
	if strings.HasPrefix(mimeStr, "image/") {
		imgBytes, err := os.ReadFile(filePath)
		if err == nil {
			imgBase64 := base64.StdEncoding.EncodeToString(imgBytes)
			dataURI := fmt.Sprintf("data:%s;base64,%s", mimeStr, imgBase64)
			// Merge text + image into ONE content object for fusion vector
			return []QwenContent{
				{
					"image": dataURI,
					"text":  "图片文件名: " + fileName,
				},
			}, "图片", nil
		}
	}

	// 2. Video Files
	// Note: According to API, videos should ideally be URLs.
	// Sending giant base64 for video might be too heavy. We will just use filename for now unless small.
	if strings.HasPrefix(mimeStr, "video/") {
		return []QwenContent{
			{"text": "视频文件: " + fileName},
		}, "视频", nil
	}

	// 3. PDF Files
	if ext == ".pdf" || mimeStr == "application/pdf" {
		text, err := extractPDFText(filePath)
		if err == nil && len(text) > 0 {
			// Limit text length to prevent token overflow (approx heuristic)
			if len(text) > 10000 {
				text = text[:10000]
			}
			return []QwenContent{{"text": text}}, "PDF文档", nil
		}
	}

	// 4. Code / Text Files
	if isTextFile(ext) || strings.HasPrefix(mimeStr, "text/") {
		data, err := os.ReadFile(filePath)
		if err == nil {
			contentStr := string(data)
			if isCodeFile(ext) {
				contentStr = fmt.Sprintf("以下是%s代码文件内容：\n%s", ext, contentStr)
			}
			if len(contentStr) > 10000 {
				contentStr = contentStr[:10000] // Truncate
			}
			return []QwenContent{{"text": contentStr}}, "纯文本/代码", nil
		}
	}

	// 5. Fallback for binaries/unknown
	return []QwenContent{{"text": "文件名称: " + fileName}}, "其他", nil
}

// buildFilePath 递归查询 parent_id 链，得到完整路径如 /项目A/backend/auth.go
func (s *embeddingServiceImpl) buildFilePath(file *model.File) string {
	parts := []string{file.FileName}
	parentID := file.ParentID
	for parentID != 0 {
		var parent model.File
		if err := s.db.First(&parent, parentID).Error; err != nil {
			break
		}
		parts = append([]string{parent.FileName}, parts...)
		parentID = parent.ParentID
	}
	return "/" + strings.Join(parts, "/")
}

// CreateFileEmbedding 为具体文件创建或更新Embedding
func (s *embeddingServiceImpl) CreateFileEmbedding(fileID uint) error {
	var file model.File
	if err := s.db.First(&file, fileID).Error; err != nil {
		return fmt.Errorf("failed to get file: %w", err)
	}

	if file.IsDirectory {
		// Directory embedding logic is handled separately (summarization)
		return nil
	}

	// 直接解析文件内容（图片/文本/代码等）
	contents, desc, err := s.ParseFileContent(file.FilePath, file.FileName)
	if err != nil {
		log.Printf("Warning: ParseFileContent failed for %d: %v", fileID, err)
		contents = []QwenContent{{"text": file.FileName}}
		desc = "其他"
	}

	// 构建路径前缀，降低 pure text 内容的 token 占比，让路径上下文提升相关性
	filePath := s.buildFilePath(&file)
	pathPrefix := fmt.Sprintf("路径: %s\n类型: %s\n", filePath, desc)

	// 将路径信息挺入 text 内容首部
	for i, c := range contents {
		if txt, ok := c["text"].(string); ok {
			contents[i]["text"] = pathPrefix + txt
		} else if _, hasImg := c["image"]; hasImg {
			// 图片类型：把路径嵌入 text 字段内
			c["text"] = pathPrefix + file.FileName
			contents[i] = c
		}
	}

	// 生成向量
	vector, err := s.GenerateMultimodalEmbedding(contents)
	if err != nil {
		log.Printf("Warning: Failed to generate vector for file %d (%s): %v", fileID, desc, err)
		return err
	}

	// 存入 Chromem
	docID := fmt.Sprintf("file_%d", fileID)
	doc := chromem.Document{
		ID: docID,
		Metadata: map[string]string{
			"type":      "file",
			"parent_id": strconv.FormatUint(uint64(file.ParentID), 10),
			"user_id":   strconv.FormatUint(uint64(file.UserID), 10),
			"file_path": filePath,
		},
		Embedding: vector,
		Content:   filePath, // Store path for display
	}

	if s.collection == nil {
		return errors.New("chromem collection not initialized")
	}

	ctx := context.Background()
	if err := s.collection.AddDocument(ctx, doc); err != nil {
		return fmt.Errorf("failed to save to chromem: %w", err)
	}

	// 更新 DB 标记
	file.EmbeddingID = docID
	s.db.Save(&file)

	return nil
}

// CreateFolderEmbedding 为文件夹生成总体 Summary 的 Embedding
func (s *embeddingServiceImpl) CreateFolderEmbedding(folderID uint, userID uint, summaryText string) error {
	vector, err := s.GenerateMultimodalEmbedding([]QwenContent{{"text": summaryText}})
	if err != nil {
		return err
	}

	docID := fmt.Sprintf("folder_%d", folderID)
	doc := chromem.Document{
		ID: docID,
		Metadata: map[string]string{
			"type":      "folder",
			"parent_id": "0", // Treat root folders as top level
			"user_id":   strconv.FormatUint(uint64(userID), 10),
		},
		Embedding: vector,
		Content:   summaryText,
	}

	if err := s.collection.AddDocument(context.Background(), doc); err != nil {
		return err
	}
	return nil
}

// DeleteEmbedding 删除指定文件的语义向量 (处理 chromem-go 缺少独立删除 API 的情况)
func (s *embeddingServiceImpl) DeleteEmbedding(fileID uint, isDirectory bool) error {
	log.Printf("Embedding for file %d (isDir: %v) unlinked due to file deletion", fileID, isDirectory)
	return nil
}

// SearchByEmbedding 两阶段搜索架构：文件夹和具体文件
func (s *embeddingServiceImpl) SearchByEmbedding(userID uint, query string, limit int) (*SearchResult, error) {
	if !s.cfg.Qwen.Enabled {
		return nil, errors.New("embedding service is disabled")
	}

	if s.collection == nil {
		return nil, errors.New("chromem collection unavailable")
	}

	queryVector, err := s.GenerateMultimodalEmbedding([]QwenContent{{"text": query}})
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	ctx := context.Background()
	result := &SearchResult{
		Folders: make([]*model.File, 0),
		Files:   make([]*model.File, 0),
	}

	uidStr := strconv.FormatUint(uint64(userID), 10)

	// ==========================================
	// Phase 1: Search for relevant folders using real queryVector
	// Folder search: topk=5, threshold=0.6
	folderDocs, folderErr := s.collection.QueryEmbedding(ctx, queryVector, 5, map[string]string{
		"type":    "folder",
		"user_id": uidStr,
	}, nil)

	if folderErr == nil && len(folderDocs) > 0 {
		for _, fd := range folderDocs {
			// Threshold check for folders: 0.6
			if fd.Similarity < 0.6 {
				continue
			}

			// Extract folder ID from "folder_123"
			fidStr := strings.TrimPrefix(fd.ID, "folder_")

			// Load from DB
			if fid, pErr := strconv.ParseUint(fidStr, 10, 32); pErr == nil {
				var folder model.File
				if s.db.First(&folder, fid).Error == nil {
					result.Folders = append(result.Folders, &folder)
				}
			}
		}
	}

	// ==========================================
	// Phase 2: Search for specific files (with user_id filter to prevent data leakage)
	// File search: topk=10, threshold=0.5
	fileDocs, err := s.collection.QueryEmbedding(ctx, queryVector, 10, map[string]string{
		"type":    "file",
		"user_id": uidStr,
	}, nil)

	if err == nil {
		for _, fd := range fileDocs {
			// Threshold check for files to filter out noise: 0.5
			if fd.Similarity < 0.5 {
				continue
			}

			fidStr := strings.TrimPrefix(fd.ID, "file_")
			if fid, pErr := strconv.ParseUint(fidStr, 10, 32); pErr == nil {
				var file model.File
				if s.db.First(&file, fid).Error == nil {
					result.Files = append(result.Files, &file)
				}
			}
		}
	}

	// Fallback to text if everything failed and empty
	if len(result.Files) == 0 && len(result.Folders) == 0 && s.cfg.Qwen.FallbackToTextSearch {
		// Just pull standard list
		var files []*model.File
		s.db.Where("user_id = ? AND file_name LIKE ?", userID, "%"+query+"%").Limit(limit).Find(&files)
		for _, f := range files {
			if f.IsDirectory {
				result.Folders = append(result.Folders, f)
			} else {
				result.Files = append(result.Files, f)
			}
		}
	}

	return result, nil
}

// Helpers
func extractPDFText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var buf bytes.Buffer
	b, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	buf.ReadFrom(b)
	return buf.String(), nil
}

func isTextFile(ext string) bool {
	exts := []string{".txt", ".md", ".json", ".xml", ".csv", ".ini"}
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return isCodeFile(ext)
}

func isCodeFile(ext string) bool {
	exts := []string{".go", ".py", ".js", ".ts", ".html", ".css", ".cpp", ".c", ".java", ".php", ".rb", ".sh"}
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}

func (s *embeddingServiceImpl) GetDB() *gorm.DB {
	return s.db
}
