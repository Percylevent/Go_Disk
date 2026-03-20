package main

import (
	"fmt"
	"log"
	"time"

	"GoDisk/internal/config"
	"GoDisk/internal/handler"
	"GoDisk/internal/middleware"
	"GoDisk/internal/model"
	"GoDisk/internal/service"

	"github.com/gin-gonic/gin"
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

	// 启动后台清理任务
	go startCleanupTasks(cfg, model.DB)

	if err := r.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// startCleanupTasks 启动后台清理任务
func startCleanupTasks(cfg *config.Config, db interface{}) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		cleanupExpiredShares(db)
		cleanupIncompleteUploads(cfg, db)
	}
}

// cleanupExpiredShares 清理过期的分享
func cleanupExpiredShares(db interface{}) {
	// TODO: 实现清理过期分享的逻辑
	log.Println("Running cleanup task: expired shares")
}

// cleanupIncompleteUploads 清理未完成的上传
func cleanupIncompleteUploads(cfg *config.Config, db interface{}) {
	// TODO: 实现清理未完成上传的逻辑
	log.Println("Running cleanup task: incomplete uploads")
}
