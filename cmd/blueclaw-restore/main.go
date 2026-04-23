package main

import (
	"flag"
	"log"

	"blueclaw/internal/restore"
)

func main() {
	bundlePath := flag.String("input", "blueclaw-backup.tar.gz", "bundle input path")
	targetDirectoryPath := flag.String("target", "restore-output", "restore target directory")
	flag.Parse()

	restoreService := restore.RestoreService{}
	errorValue := restoreService.RestoreSnapshotBundle(*bundlePath, *targetDirectoryPath)
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}
