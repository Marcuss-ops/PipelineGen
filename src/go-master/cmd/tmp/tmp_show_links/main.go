package main

import (
	"fmt"
	"log"
	"velox/go-master/internal/clipdb"
)

func main() {
	db, err := clipdb.OpenSQLite("data/clips_catalog.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	keywords := []string{
		"artificial intelligence",
		"cybersecurity",
		"mountain landscape",
		"ocean waves",
		"luxury yacht",
		"chef cooking",
		"money counting",
		"meditation",
	}

	fmt.Printf("%-25s | %-40s | %s\n", "KEYWORD", "TITOLO CLIP", "LINK MP4 DIRETTO")
	fmt.Println(string(make([]byte, 110, 110)))

	for _, kw := range keywords {
		params := clipdb.QueryParams{
			Tags:  []string{kw},
			Limit: 3,
		}
		clips, _ := db.Resolve(params)
		for _, c := range clips {
			// Pulizia link per visualizzazione
			link := c.LocalPath
			if len(link) > 60 {
				link = link[:57] + "..."
			}
			fmt.Printf("%-25s | %-40s | %s\n", kw, truncate(c.Filename, 40), link)
		}
		fmt.Println("-" + string(make([]byte, 109, 109)))
	}
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
