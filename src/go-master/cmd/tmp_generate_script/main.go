package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/bootstrap"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	var (
		topic    = flag.String("topic", "Gervonta Davis", "Script topic")
		title    = flag.String("title", "", "Document title")
		language = flag.String("language", "en", "Target language")
		duration = flag.Int("duration", 120, "Target duration in seconds")
		textFile = flag.String("text-file", "", "Source text file")
		text     = flag.String("text", "", "Source text")
	)
	flag.Parse()

	sourceText := *text
	if sourceText == "" && *textFile != "" {
		data, err := os.ReadFile(*textFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read text file: %v\n", err)
			os.Exit(1)
		}
		sourceText = string(data)
	}
	if sourceText == "" {
		fmt.Fprintln(os.Stderr, "error: provide --text or --text-file")
		os.Exit(2)
	}

	cfg := config.Get()
	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	deps, err := bootstrap.WireServices(cfg, log)
	if err != nil {
		log.Error("wire services failed", zap.Error(err))
		os.Exit(1)
	}
	defer deps.Cleanup()

	handler := deps.RouterDeps.Handlers.ScriptPipeline
	if handler == nil {
		fmt.Fprintln(os.Stderr, "script pipeline handler not available")
		os.Exit(1)
	}

	payload := fmt.Sprintf(
		`{"topic":%q,"title":%q,"text":%q,"duration":%d,"language":%q}`,
		*topic,
		func() string {
			if *title != "" {
				return *title
			}
			return *topic
		}(),
		sourceText,
		*duration,
		*language,
	)

	req := httptest.NewRequest(http.MethodPost, "/api/script-pipeline/full", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = req.WithContext(context.Background())

	handler.GenerateFullPipeline(ctx)

	fmt.Print(w.Body.String())
}
