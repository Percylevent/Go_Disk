package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Storage  StorageConfig  `mapstructure:"storage"`
	JWT      JWTConfig      `mapstructure:"jwt"`
	Qwen     QwenConfig     `mapstructure:"qwen"`
	Upload   UploadConfig   `mapstructure:"upload"`
}

type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
}

type DatabaseConfig struct {
	Path string `mapstructure:"path"`
}

type StorageConfig struct {
	UploadPath          string `mapstructure:"upload_path"`
	ChunkPath           string `mapstructure:"chunk_path"`
	VectorPath          string `mapstructure:"vector_path"` // chromem-go persistent storage
	MaxFileSize         int64  `mapstructure:"max_file_size"`
	DefaultStorageLimit int64  `mapstructure:"default_storage_limit"`
}

type JWTConfig struct {
	Secret      string `mapstructure:"secret"`
	ExpireHours int    `mapstructure:"expire_hours"`
}

type QwenConfig struct {
	APIKey               string `mapstructure:"api_key"`
	APIURL               string `mapstructure:"api_url"`
	Model                string `mapstructure:"model"`
	Enabled              bool   `mapstructure:"enabled"`
	Timeout              int    `mapstructure:"timeout"`
	MaxRetries           int    `mapstructure:"max_retries"`
	FallbackToTextSearch bool   `mapstructure:"fallback_to_text_search"`
}

type UploadConfig struct {
	ChunkSize            int `mapstructure:"chunk_size"`
	MaxConcurrentUploads int `mapstructure:"max_concurrent_uploads"`
}

var cfg *Config

// Load 加载配置文件
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// 设置配置文件路径
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
	}

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg = &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// 确保必要的目录存在
	if err := ensureDirectories(); err != nil {
		return nil, fmt.Errorf("failed to create directories: %w", err)
	}

	return cfg, nil
}

// Get 获取配置实例
func Get() *Config {
	return cfg
}

// ensureDirectories 确保必要的目录存在
func ensureDirectories() error {
	dirs := []string{
		cfg.Storage.UploadPath,
		cfg.Storage.ChunkPath,
		cfg.Storage.VectorPath, // Ensure vector path exists
		filepath.Dir(cfg.Database.Path),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}
