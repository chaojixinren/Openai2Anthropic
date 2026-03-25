package main

import (
	"log"

	"github.com/LangQi99/Openai2Anthropic/internal/config"
	"github.com/LangQi99/Openai2Anthropic/internal/gateway"
)

func main() {
	store, err := config.NewStore("data/config.json")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	app := gateway.NewServer(store)
	if err := app.ListenAndServe(); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
