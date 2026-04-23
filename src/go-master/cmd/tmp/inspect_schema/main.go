package main

import (
	"database/sql"
	"fmt"
	"log"
	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "data/clips_catalog.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("PRAGMA table_info(clips)")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("Columns in 'clips' table:")
	for rows.Next() {
		var cid int
		var name, dtype string
		var notnull, pk int
		var dflt_value interface{}
		err = rows.Scan(&cid, &name, &dtype, &notnull, &dflt_value, &pk)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("- %s (%s)\n", name, dtype)
	}
}
