package handler

import (
	"contract/internal/service"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/golang-jwt/jwt"
)

type FileHandler struct {
	svc       *service.FileService
	jwtSecret []byte
}

func NewFileHandler(svc *service.FileService, jwtSecret []byte) *FileHandler {
	return &FileHandler{svc: svc, jwtSecret: jwtSecret}
}

func (h *FileHandler) List(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("search")
	files, err := h.svc.ListDocFiles(search)
	if err != nil {
		log.Printf("HandlerService(0): error listing fiiles: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to read files")
		return
	}
	writeJson(w, http.StatusOK, files)
}

func (h *FileHandler) Download(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	log.Printf("Download request: name=%s", name)

	safeName := filepath.Base(name)
	ext := strings.ToLower(filepath.Ext(safeName))
	if ext != ".doc" && ext != ".docx" {
		// Возвращаем 415 Unsupported Media Type без JSON
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}

	filePath := filepath.Join(h.svc.Dir(), safeName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("Download: file not found: %s", filePath)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (h *FileHandler) Upload(w http.ResponseWriter, r *http.Request) {
	//Ограничение
	// r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	// if err := r.ParseMultipartForm(32 << 20); err != nil {
	// 	writeError(w, http.StatusBadRequest, "file too large")
	// 	return
	// }

	file, hader, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()

	safeName := filepath.Base(hader.Filename)
	ext := strings.ToLower(filepath.Ext(safeName))
	if ext != ".doc" && ext != ".docx" {
		writeError(w, http.StatusBadRequest, "only .doc and .docx allowed")
		return
	}

	destPath := filepath.Join(h.svc.Dir(), safeName)
	if _, err := os.Stat(destPath); err == nil {
		writeError(w, http.StatusConflict, "file already exists")
		return
	}

	dst, err := os.Create(destPath)
	if err != nil {
		log.Printf("create file error: %v", err)
		writeError(w, http.StatusInternalServerError, "cannot create fiile")
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		os.Remove(destPath)
		writeError(w, http.StatusInternalServerError, "save error")
		return
	}

	info, _ := os.Stat(destPath)
	resp := map[string]interface{}{
		"name":     safeName,
		"size":     info.Size(),
		"mod_time": info.ModTime().UTC().Format("2006-01-02 15:04:05"),
	}
	writeJson(w, http.StatusCreated, resp)
}

func (h *FileHandler) EditorCallback(w http.ResponseWriter, r *http.Request) {
	// 1. Проверяем JWT-токен (если задан секрет)
	if len(h.jwtSecret) > 0 {
		tokenString := r.Header.Get("Authorization")
		tokenString = strings.TrimPrefix(tokenString, "Bearer ")
		if tokenString == "" {
			// Пробуем также из query (на случай JWT_IN_BODY=false, но иногда передают иначе)
			tokenString = r.URL.Query().Get("token")
		}
		if tokenString != "" {
			token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				return h.jwtSecret, nil
			})
			if err != nil || !token.Valid {
				log.Printf("EditorCallback: invalid token: %v", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":1}`))
				return
			}
		}
	}

	// 2. Читаем тело запроса
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("EditorCallback: read body error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":1}`))
		return
	}
	defer r.Body.Close()

	// 3. Если тело начинается с '{' — это JSON-уведомление, не сохраняем файл
	if len(body) > 0 && body[0] == '{' {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error":0}`))
		return
	}

	// 4. Получаем имя файла из query
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":1}`))
		return
	}

	// 5. Безопасность: проверяем расширение и сохраняем
	safeName := filepath.Base(filename)
	ext := strings.ToLower(filepath.Ext(safeName))
	if ext != ".doc" && ext != ".docx" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":1}`))
		return
	}

	filePath := filepath.Join(h.svc.Dir(), safeName)
	if err := os.WriteFile(filePath, body, 0644); err != nil {
		log.Printf("EditorCallback: write file error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":1}`))
		return
	}

	log.Printf("EditorCallback: file %s saved, size=%d", safeName, len(body))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"error":0}`))
}

func (h *FileHandler) Preview(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "file name is required")
		return
	}

	safeName := filepath.Base(name)
	ext := strings.ToLower(filepath.Ext(safeName))
	if ext != ".doc" && ext != ".docx" {
		writeError(w, http.StatusBadRequest, "only .doc/.docx files can be previewed")
		return
	}

	srcPath := filepath.Join(h.svc.Dir(), safeName)
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}

	// Создаём временную директорию для PDF
	tmpDir, err := os.MkdirTemp("", "preview-")
	if err != nil {
		log.Printf("Preview: create temp dir error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer os.RemoveAll(tmpDir) // очистка после ответа

	// Копируем исходный файл во временную папку
	tmpSrc := filepath.Join(tmpDir, safeName)
	if err := copyFile(srcPath, tmpSrc); err != nil {
		log.Printf("Preview: copy file error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Запускаем конвертацию
	cmd := exec.Command("libreoffice", "--headless", "--convert-to", "pdf", "--outdir", tmpDir, tmpSrc)
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("Preview: libreoffice error: %v, output: %s", err, string(out))
		writeError(w, http.StatusInternalServerError, "conversion failed")
		return
	}

	// Ищем получившийся PDF
	pdfPath := filepath.Join(tmpDir, strings.TrimSuffix(safeName, ext)+".pdf")
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		log.Println("Preview: PDF not created")
		writeError(w, http.StatusInternalServerError, "conversion failed")
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline; filename=\""+strings.TrimSuffix(safeName, ext)+".pdf\"")
	http.ServeFile(w, r, pdfPath)
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

func writeJson(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJson(w, status, map[string]string{"error": msg})
}
