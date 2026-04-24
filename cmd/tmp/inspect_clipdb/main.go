package main

import (
	"fmt"
	"strings"

	"velox/go-master/internal/clipdb"
)

func main() {
	db, err := clipdb.Open("data/clip_index.json")
	if err != nil {
		panic(err)
	}
	fmt.Println("FOLDERS:", len(db.GetAllFolders()), "CLIPS:", len(db.GetAllClips()))
	if clips := db.GetAllClips(); len(clips) > 0 {
		fmt.Printf("FIRST CLIP: %+v\n", clips[0])
	}
	for i, folder := range db.GetAllFolders() {
		if i >= 20 {
			break
		}
		fmt.Printf("FOLDER %d: slug=%q path=%q drive=%q\n", i, folder.TopicSlug, folder.FullPath, folder.DriveID)
	}
	for _, q := range []string{"Gervonta Davis", "Mike Tyson", "Elvis Presley", "Wwe"} {
		fmt.Println("QUERY:", q)
		folders := db.SearchFolders(q)
		for i, folder := range folders {
			if i >= 5 {
				break
			}
			fmt.Printf("%d: slug=%q path=%q drive=%q\n", i, folder.TopicSlug, folder.FullPath, folder.DriveID)
		}
		if len(folders) == 0 {
			for _, folder := range db.GetAllFolders() {
				if strings.Contains(strings.ToLower(folder.FullPath), strings.ToLower(q)) || strings.Contains(strings.ToLower(folder.TopicSlug), strings.ToLower(q)) {
					fmt.Printf("MATCH-RAW: slug=%q path=%q drive=%q\n", folder.TopicSlug, folder.FullPath, folder.DriveID)
				}
			}
		}
	}
}
