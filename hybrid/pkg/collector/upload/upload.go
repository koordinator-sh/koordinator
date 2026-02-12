/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-10 @author yangwanjin
 */

package upload

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"hybrid/pkg/constants"

	"k8s.io/klog/v2"
)

const (
	CompressDir = "/tmp/hybrid-output"
)

type Service struct {
	url       string
	mu        sync.Mutex
	authToken string
	dataReady chan string
	stopChan  chan struct{}
}

func NewService(url string, token string) *Service {
	return &Service{
		url:       url,
		authToken: token,
		dataReady: make(chan string, 10),
		stopChan:  make(chan struct{}),
	}
}

func (s *Service) Start() {
	go s.uploadWorker()
	klog.Infof("Upload service started")
}

func (s *Service) Stop() {
	close(s.stopChan)
}

func (s *Service) NotifyDataReady(dataPath string) {
	select {
	case s.dataReady <- dataPath:
		klog.Infof("Data ready notify sent for: %s", dataPath)
	default:
		klog.Warningf("Notify channel has full, skipping notification")
	}
}

func (s *Service) uploadWorker() {
	for {
		select {
		case <-s.stopChan:
			klog.Infof("Upload worker stopped")
			return

		case dataPath := <-s.dataReady:
			if err := s.processAndUpload(dataPath); err != nil {
				klog.Errorf("Failed to process and upload: %v", err)
			}
		}
	}
}

func (s *Service) processAndUpload(dataPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// upload file
	if err := s.uploadFile(dataPath); err != nil {
		return fmt.Errorf("failed to upload : %w", err)
	}

	return nil
}

func (s *Service) compressTo7z(sourceDir, outputFile string) error {
	// 7z a -t7z output.7z sourceDir
	cmd := exec.Command("7z", "a", "-t7z", outputFile, sourceDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("7z command failed: %w, stderr: %s", err, stderr.String())
	}

	return nil
}

func (s *Service) compressDirToTarGz(sourceDir string) (string, error) {
	timestamp := time.Now().Format("20060102_150405")
	compressedFile := filepath.Join(CompressDir, fmt.Sprintf("hybrid-output-%s.tar.gz", timestamp))

	// make sure the compressed directory exists
	if err := os.MkdirAll(CompressDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create compress dir: %s", err)
	}

	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return "", fmt.Errorf("source directory does not exist: %s\n", sourceDir)
	}
	// create output file
	file, err := os.Create(compressedFile)
	if err != nil {
		return "", fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	// create gzip writer
	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()

	// create tar writer
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	// 获取源目录的基础名称,用于在 tar 中正确设置路径
	baseDir := filepath.Base(sourceDir)

	// 遍历源目录
	err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 计算相对于源目录的路径
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		// 跳过根目录本身
		if relPath == "." {
			return nil
		}

		// 在 tar 中使用基础目录名 + 相对路径
		tarPath := filepath.Join(baseDir, relPath)
		// 确保路径分隔符为 /
		tarPath = strings.ReplaceAll(tarPath, string(filepath.Separator), "/")

		// 创建 tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("failed to create tar header: %w", err)
		}

		header.Name = tarPath

		// 写入 header
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header: %w", err)
		}

		// 如果是普通文件,读取并写入内容
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", path, err)
			}
			defer file.Close()

			if _, err := io.Copy(tarWriter, file); err != nil {
				return fmt.Errorf("failed to copy file content: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to walk directory: %w", err)
	}

	// all data is written
	if err := tarWriter.Close(); err != nil {
		return "", fmt.Errorf("failed to close tar writer: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return "", fmt.Errorf("failed to close gzip writer: %w", err)
	}

	return compressedFile, nil
}

func (s *Service) uploadFile(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// create multipart writer
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// add param
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	// close writer
	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close writer: %w", err)
	}

	// create HTTP request
	endpoint := fmt.Sprintf("%s%s", s.url, constants.UploadFileEndpoint)
	req, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// set Header
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("accept", "application/json")
	req.Header.Set("x-token", s.authToken)

	// send request
	client := &http.Client{
		Timeout: 5 * time.Minute, // 5min
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// check http status
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	klog.Infof("Successfully upload metrics data, status: %d\n", resp.StatusCode)

	return nil
}
