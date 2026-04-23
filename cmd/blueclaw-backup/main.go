package main

import (
	"flag"
	"log"

	"blueclaw/internal/backup"
)

func main() {
	bundlePath := flag.String("output", "blueclaw-backup.tar.gz", "bundle output path")
	flag.Parse()

	backupService := backup.BackupService{}
	_, errorValue := backupService.CreateSnapshotBundle(*bundlePath, []string{
		"config",
		"ideas",
	})
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}
