package main

import (
	"context"
	"fmt"
	"os"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"time"
)

func main() {
	if err := config.InitConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if err := model.InitDB(); err != nil {
		fmt.Fprintf(os.Stderr, "migrate database: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := model.CloseWriter(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "close database writer: %v\n", err)
		os.Exit(1)
	}
	if sqlDB, err := model.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
	fmt.Println("database schema is current")
}
