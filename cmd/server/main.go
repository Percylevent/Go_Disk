package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"GoDisk/internal/config"
	"GoDisk/internal/handler"
	"GoDisk/internal/middleware"
	"GoDisk/internal/model"
	"GoDisk/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func main() {
	// 加载配置
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 设置Gin模式
	gin.SetMode(cfg.Server.Mode)

	// 初始化数据库
	if err := model.InitDB(cfg); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// 初始化服务
	authSvc := service.NewAuthService(model.DB)
	storageSvc := service.NewStorageService(model.DB, cfg)
	fileSvc := service.NewFileService(model.DB, cfg, storageSvc)
	chunkSvc := service.NewChunkService(model.DB, cfg, storageSvc, fileSvc)
	embSvc := service.NewEmbeddingService(model.DB, cfg)
	shareHandler := handler.NewShareHandler(model.DB, fileSvc, embSvc)

	// 初始化Handler
	authHandler := handler.NewAuthHandler(authSvc)
	fileHandler := handler.NewFileHandler(fileSvc, chunkSvc, embSvc)
	pageHandler := handler.NewPageHandler()
	adminHandler := handler.NewAdminHandler(embSvc)

	// 创建Gin引擎
	r := gin.Default()

	// 加载HTML模板
	r.LoadHTMLGlob("webpage/*.html")

	// 全局中间件
	r.Use(middleware.Logger())
	r.Use(middleware.CORS())

	// 健康检查（无需认证）
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 公开API（无需认证）
	public := r.Group("/api")
	{
		public.POST("/auth/register", authHandler.Register)
		public.POST("/auth/login", authHandler.Login)
	}

	// 认证后的API
	api := r.Group("/api")
	api.Use(middleware.AuthMiddleware())
	{
		// 认证相关
		auth := api.Group("/auth")
		{
			auth.GET("/me", authHandler.GetMe)
			auth.PUT("/profile", authHandler.UpdateProfile)
			auth.POST("/change-password", authHandler.ChangePassword)
		}

		// 文件操作
		files := api.Group("/files")
		{
			files.POST("/upload", fileHandler.UploadFile)
			files.POST("/upload/chunk/init", fileHandler.InitChunkUpload)
			files.POST("/upload/chunk", fileHandler.UploadChunk)
			files.POST("/upload/chunk/complete", fileHandler.CompleteUpload)
			files.GET("/list", fileHandler.ListFiles)
			files.GET("/download/:id", fileHandler.DownloadFile)
			files.DELETE("/:id", fileHandler.DeleteFile)
			files.POST("/folder", fileHandler.CreateFolder)
			files.PUT("/move", fileHandler.MoveFile)
			files.PUT("/rename", fileHandler.RenameFile)
			files.GET("/search", fileHandler.SearchFiles)
			files.POST("/:id/build_index", fileHandler.BuildIndex)
		}

		// 分享链接
		shares := api.Group("/shares")
		{
			shares.POST("/create", shareHandler.CreateShare)
			shares.GET("", shareHandler.ListShares)
			shares.DELETE("/:id", shareHandler.DeleteShare)
		}

		// 管理员功能
		admin := api.Group("/admin")
		{
			admin.POST("/regenerate-embeddings", adminHandler.RegenerateAllEmbeddings)
			admin.GET("/embedding-status", adminHandler.GetEmbeddingStatus)
		}
	}

	// 页面访问
	r.GET("/", pageHandler.IndexPage)

	// 公开分享链接访问（无需认证）
	r.GET("/s/:code", pageHandler.SharePage)
	r.GET("/api/s/:code/info", shareHandler.AccessShare)
	r.POST("/s/:code/verify", shareHandler.VerifyShare)
	r.GET("/s/:code/download", shareHandler.DownloadShare)

	// 启动服务器
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	fmt.Printf(`
╔═════════════════════════════════════════════════════════╗
║                                                         ║
║   GoDisk - Cloud Storage Backend                        ║
║                                                         ║
║   Server running on: http://localhost%s              ║
║   Environment: %s                                   ║
║   Database: %s                                      ║
║                                                         ║
╚═════════════════════════════════════════════════════════╝
`, addr, cfg.Server.Mode, cfg.Database.Path)

	// 创建 HTTP Server（替代 r.Run，以支持 Graceful Shutdown）
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// 启动后台清理任务
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	go startCleanupTasks(cleanupCtx, cfg, model.DB, storageSvc)

	// 在 goroutine 中启动 HTTP 服务
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Received shutdown signal, shutting down gracefully...")

	// 停止后台清理任务
	cleanupCancel()

	// 优雅关闭 embedding 服务（等待当前任务完成）
	embSvc.Shutdown()

	// 优雅关闭 HTTP 服务器（等待进行中的请求完成，最多30秒）
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}

// startCleanupTasks 启动后台清理任务
func startCleanupTasks(ctx context.Context, cfg *config.Config, db *gorm.DB, storageSvc *service.StorageService) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Cleanup tasks stopped")
			return
		case <-ticker.C:
			cleanupExpiredShares(db)
			cleanupIncompleteUploads(cfg, db, storageSvc)
		}
	}
}

// cleanupExpiredShares 清理过期的分享
func cleanupExpiredShares(db *gorm.DB) {
	result := db.Where("expire_at IS NOT NULL AND expire_at < ?", time.Now()).Delete(&model.Share{})
	if result.Error != nil {
		log.Printf("[Cleanup] Failed to cleanup expired shares: %v", result.Error)
	} else if result.RowsAffected > 0 {
		log.Printf("[Cleanup] Cleaned up %d expired shares", result.RowsAffected)
	}
}

// cleanupIncompleteUploads 清理超过24小时未完成的上传
func cleanupIncompleteUploads(cfg *config.Config, db *gorm.DB, storageSvc *service.StorageService) {
	cutoff := time.Now().Add(-24 * time.Hour)

	var chunks []model.FileChunk
	if err := db.Where("status IN ? AND updated_at < ?", []string{"pending", "uploading"}, cutoff).Find(&chunks).Error; err != nil {
		log.Printf("[Cleanup] Failed to query incomplete uploads: %v", err)
		return
	}

	for _, chunk := range chunks {
		// 清理分片文件
		if err := storageSvc.CleanChunks(chunk.UploadID); err != nil {
			log.Printf("[Cleanup] Failed to clean chunks for upload %s: %v", chunk.UploadID, err)
		}
		// 更新状态为失败
		db.Model(&chunk).Update("status", "failed")
	}

	if len(chunks) > 0 {
		log.Printf("[Cleanup] Cleaned up %d incomplete uploads", len(chunks))
	}
}
