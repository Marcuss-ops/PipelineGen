package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/bootstrap"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	var (
		topic    = flag.String("topic", "Gervonta Davis", "Script topic")
		language = flag.String("language", "en", "Target language")
		duration = flag.Int("duration", 120, "Target duration in seconds")
		textFile = flag.String("text-file", "", "Source text file")
	)
	flag.Parse()

	sourceText := ""
	if *textFile != "" {
		data, err := os.ReadFile(*textFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read text file: %v\n", err)
			os.Exit(1)
		}
		sourceText = string(data)
	}

	cfg := config.Get()
	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	deps, err := bootstrap.WireMinimal(cfg, log)
	if err != nil {
		log.Error("wire minimal failed", zap.Error(err))
		os.Exit(1)
	}
	defer deps.Cleanup()

	handler := deps.RouterDeps.Handlers.ScriptPipeline
	
	payload := map[string]interface{}{
		"topic":       *topic,
		"source_text": sourceText,
		"duration":    *duration,
		"language":    *language,
		"model":       "gemma3:4b",
		"minimal":     true,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/script-pipeline/generate", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = req.WithContext(context.Background())

	handler.GenerateText(ctx)

	var resp struct {
		FullContent string `json:"full_content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		fmt.Printf("Error unmarshaling: %v\n", err)
		fmt.Println(w.Body.String())
		return
	}

	fmt.Println(resp.FullContent)
}
