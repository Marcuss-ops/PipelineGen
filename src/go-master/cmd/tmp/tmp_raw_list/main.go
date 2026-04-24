package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
	"velox/go-master/pkg/config"
)

func main() {
	db, err := sql.Open("sqlite", config.ResolveDataPath("clips_catalog.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT title, tags, url FROM clips ORDER BY RANDOM() LIMIT 20")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Printf("%-35s | %-30s | %s\n", "TITOLO", "TAGS (KEYWORDS)", "LINK MP4")
	fmt.Println(string(make([]byte, 120, 120)))

	for rows.Next() {
		var title, tags, url string
		rows.Scan(&title, &tags, &url)

		// Troncamento per visualizzazione
		if len(title) > 33 {
			title = title[:30] + "..."
		}
		if len(tags) > 28 {
			tags = tags[:25] + "..."
		}
		if len(url) > 60 {
			url = url[:57] + "..."
		}

		fmt.Printf("%-35s | %-30s | %s\n", title, tags, url)
	}
}
