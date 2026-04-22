package clipdb

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// CheckLinks verifica lo stato HTTP reale delle clip usando GET Range per bypassare blocchi CDN.
func (s *SQLiteDB) CheckLinks(ctx context.Context, workers int) error {
	rows, err := s.db.Query(`
		SELECT id, url FROM clips 
		WHERE last_checked IS NULL OR last_checked < datetime('now','-12 hours')
		ORDER BY last_checked ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type task struct {
		id  string
		url string
	}
	tasks := make(chan task)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{Timeout: 10 * time.Second}
			for t := range tasks {
				status := s.performRequestWithRetry(ctx, client, t.url)
				_ = s.UpdateHealth(t.id, status)
				
				// Rate Limit: Evitiamo il ban da Artlist
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}

	for rows.Next() {
		var t task
		if err := rows.Scan(&t.id, &t.url); err == nil {
			select {
			case tasks <- t:
			case <-ctx.Done():
				break
			}
		}
	}
	close(tasks)
	wg.Wait()
	return nil
}

func (s *SQLiteDB) performRequestWithRetry(ctx context.Context, client *http.Client, url string) int {
	maxRetries := 2
	for i := 0; i <= maxRetries; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		req.Header.Set("Range", "bytes=0-0")
		req.Header.Set("User-Agent", "Velox-Health-Bot/2.0")

		resp, err := client.Do(req)
		if err != nil {
			return 500 // Errore di connessione
		}
		resp.Body.Close()

		status := resp.StatusCode
		if status == 429 { // Rate Limited: Aspetta di più e riprova
			time.Sleep(time.Duration(i+1) * 2 * time.Second)
			continue
		}
		return status
	}
	return 429
}
