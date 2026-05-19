package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"contract/internal/config"
	"contract/internal/handler"
	"contract/internal/service"

	"github.com/golang-jwt/jwt"
	"github.com/joho/godotenv"
)

// Структуры для конфигурации редактора
type EditorConfig struct {
	Document     DocumentConfig `json:"document"`
	EditorConfig EditorSettings `json:"editorConfig"`
	Height       string         `json:"height"`
	Width        string         `json:"width"`
}

type DocumentConfig struct {
	FileType string `json:"fileType"`
	Key      string `json:"key"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

type EditorSettings struct {
	CallbackURL   string        `json:"callbackUrl"`
	Mode          string        `json:"mode"`
	Lang          string        `json:"lang"`
	User          User          `json:"user"`
	Customization Customization `json:"customization"`
}

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Customization struct {
	ForceSave bool `json:"forceSave"`
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Main(0): no .env file found")
	}

	cfg := config.Load()

	if _, err := os.Stat(cfg.FilesDir); os.IsNotExist(err) {
		log.Fatalf("Main(1): files directory does not exist: %s", cfg.FilesDir)
	}

	// JWT-секрет для подписи конфигурации редактора
	jwtSecret := os.Getenv("ONLYOFFICE_JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("ONLYOFFICE_JWT_SECRET is required")
	}

	fileSvc := service.NewFileService(cfg.FilesDir)
	fileHandler := handler.NewFileHandler(fileSvc, []byte(jwtSecret))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/files/{name}", fileHandler.Download)
	mux.HandleFunc("GET /api/files", fileHandler.List)
	mux.HandleFunc("POST /api/files", fileHandler.Upload)
	mux.HandleFunc("POST /api/files/save", fileHandler.EditorCallback)

	// Страница редактора – генерирует подписанный конфиг
	mux.HandleFunc("GET /editor-page/{name}", func(w http.ResponseWriter, r *http.Request) {
		fileName := strings.TrimPrefix(r.URL.Path, "/editor-page/")
		if fileName == "" {
			http.Error(w, "file name is required", http.StatusBadRequest)
			return
		}

		internalURL := os.Getenv("FILE_SERVICE_INTERNAL_URL")
		if internalURL == "" {
			internalURL = "http://localhost:8081"
		}

		safeFileName := url.PathEscape(fileName)
		fileURL := internalURL + "/api/files/" + safeFileName
		// callback с status=6, чтобы гарантированно сохранять файл
		callbackURL := internalURL + "/api/files/save?status=6&filename=" + url.QueryEscape(fileName)

		fileType := strings.TrimPrefix(filepath.Ext(fileName), ".")
		key := generateKey(fileName)

		// Конфигурация для JWT — только обязательные поля, без permissions и customization
		jwtConfig := map[string]interface{}{
			"document": map[string]interface{}{
				"fileType": fileType,
				"key":      key,
				"title":    fileName,
				"url":      fileURL,
				"permissions": map[string]interface{}{
					"comment":                 true,
					"copy":                    true,
					"download":                true,
					"edit":                    true,
					"print":                   true,
					"fillForms":               true,
					"modifyContentControl":    true,
					"modifyFilter":            true,
					"review":                  true,
					"deleteCommentAuthorOnly": false,
					"editCommentAuthorOnly":   false,
				},
			},
			"editorConfig": map[string]interface{}{
				"callbackUrl": internalURL + "/api/files/save?filename=" + url.QueryEscape(fileName), // без status=6!
				"mode":        "edit",
				"lang":        "ru",
				"user": map[string]string{
					"id":   "user1",
					"name": "User",
				},
			},
			"height": "100%",
			"width":  "100%",
		}

		log.Printf("Editor page: fileName=%s, fileURL=%s, callbackURL=%s", fileName, fileURL, callbackURL)
		log.Printf("JWT config: %+v", jwtConfig)

		jwtSecret := os.Getenv("ONLYOFFICE_JWT_SECRET")
		if jwtSecret == "" {
			http.Error(w, "server configuration error", http.StatusInternalServerError)
			return
		}

		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"config": jwtConfig,
		}).SignedString([]byte(jwtSecret))
		if err != nil {
			log.Printf("JWT signing error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' http://localhost:8082; style-src 'self' 'unsafe-inline' http://localhost:8082; frame-src http://localhost:8082; connect-src http://localhost:8082 ws://localhost:8082;")
		w.Header().Set("Content-Type", "text/html")

		tmpl, _ := template.ParseFiles("templates/editor.html")
		tmpl.Execute(w, map[string]string{
			"FileName":    fileName,
			"FileType":    fileType,
			"Key":         key,
			"FileURL":     fileURL,
			"CallbackURL": callbackURL,
			"Token":       token,
		})
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	srv := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Main(2): file-service started on: %s, serving %s", cfg.ServerPort, cfg.FilesDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Main(3): listen error: %v", err)
		}
	}()

	<-quit
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Main(4): forced shutdown: %v", err)
	}
	log.Println("server stopped")
}

func generateKey(fileName string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, fileName)
	return safe + "_" + fmt.Sprintf("%d", time.Now().UnixNano())
}
