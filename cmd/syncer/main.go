package main

import (
	"github.com/cecobask/imdb-trakt-sync/pkg/syncer"
	_ "github.com/joho/godotenv/autoload"
)

func main() {
	syncer.NewSyncer().Run()
}
