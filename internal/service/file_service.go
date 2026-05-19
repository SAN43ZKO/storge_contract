package service

import (
	"os"
	"path/filepath"
	"strings"
)

type FileInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

type FileService struct {
	dir string
}

func NewFileService(dir string) *FileService {
	return &FileService{dir: dir}
}

func (s *FileService) Dir() string {
	return s.dir
}

func (s *FileService) ListDocFiles(search string) ([]FileInfo, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}

	var files []FileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".doc" && ext != ".docx" {
			continue
		}

		// Фильтрация по подстроке, без учёта регистра
		if search != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(search)) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, FileInfo{
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format("2006-01-02 15:04:05"),
		})
	}
	return files, nil
}
