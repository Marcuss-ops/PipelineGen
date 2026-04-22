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

	params := clipdb.QueryParams{
		Limit: 50,
	}

	clips, err := db.Resolve(params)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%-5s | %-50s | %-8s | %s\n", "N.", "TITOLO", "DURATA", "LINK MP4")
	fmt.Println(string(make([]byte, 120, 120)))
	for i, c := range clips {
		fmt.Printf("%-5d | %-50s | %-8.1fs | %s\n", i+1, c.Filename, c.Duration, c.LocalPath)
	}
}
