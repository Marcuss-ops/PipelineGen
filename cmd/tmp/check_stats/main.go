package main

import (
	"database/sql"
	"fmt"
	"log"
	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "data/unified_catalog.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM clips").Scan(&count)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Total clips in unified catalog: %d\n", count)

	rows, err := db.Query("SELECT source, COUNT(*) FROM clips GROUP BY source")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("\nClips per source:")
	for rows.Next() {
		var source string
		var count int
		if err := rows.Scan(&source, &count); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("- %s: %d\n", source, count)
	}
}
