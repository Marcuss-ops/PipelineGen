package script

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// TestGenerate_Concurrency verifies that many concurrent POST requests
// to /api/script/generate are accepted without data races or panics.
func TestGenerate_Concurrency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	idemKey := "idem-concurrent-"

	const n = 50
	var wg sync.WaitGroup
	statuses := make([]int, n)
	var mu sync.Mutex

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Vary the topic per goroutine so each request derives a
			// distinct request hash and submits a distinct job.
			body := `{"version":2,"preset":"custom","items":[{"id":"item-` + strconv.Itoa(idx) + `","source":{"type":"text","topic":"concurrency-` + strconv.Itoa(idx) + `"},"script_params":{"target_words":100}}]}`
			req := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", idemKey+strconv.Itoa(idx))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			mu.Lock()
			statuses[idx] = w.Code
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	accepted := 0
	for _, code := range statuses {
		if code == http.StatusAccepted {
			accepted++
		}
	}
	assert.Equal(t, n, accepted, "all concurrent requests must be accepted")
	assert.Equal(t, n, submit.submitCount, "each concurrent request must submit a distinct job")
}
