package main

import (
	"context"

	blinkogram "github.com/wolfsilver/blinko-telegram"
)

func main() {
	ctx := context.Background()
	service, err := blinkogram.NewService()
	if err != nil {
		panic(err)
	}
	service.Start(ctx)
}
