package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// CalculateFileSHA256 计算文件的SHA256哈希值
func CalculateFileSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CalculateReaderSHA256 计算io.Reader的SHA256哈希值
func CalculateReaderSHA256(reader io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CalculateBytesSHA256 计算字节数组的SHA256哈希值
func CalculateBytesSHA256(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
