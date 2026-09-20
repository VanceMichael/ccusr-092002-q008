package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/domain"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/httpapi"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	databasePath := os.Getenv("DATABASE_PATH")
	if databasePath == "" {
		databasePath = "data/app.sqlite3"
	}

	db, err := store.Open(databasePath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	if err := store.Migrate(context.Background(), db); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	svc := domain.NewService(db)
	log.Fatal(http.ListenAndServe(":"+port, httpapi.New(svc)))
}
