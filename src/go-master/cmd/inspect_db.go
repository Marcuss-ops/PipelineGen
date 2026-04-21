package main

import (
	"database/sql"
	"fmt"
	"log"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	db, err := sql.Open("sqlite3", "data/pipeline.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, video_id, state, locked_until FROM pipeline_queue")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("ID | Video ID | State | Locked Until")
	fmt.Println("------------------------------------")
	for rows.Next() {
		var id int
		var vid, state string
		var locked interface{}
		rows.Scan(&id, &vid, &state, &locked)
		fmt.Printf("%d | %s | %s | %v\n", id, vid, state, locked)
	}
}
