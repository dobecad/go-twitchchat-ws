package main

import (
	"fmt"

	twitch "github.com/dobecad/go-twitchchat-ws"
)

func main() {
	opts := &twitch.ClientOpts{
		Tls:          true,
		Capabilities: []string{twitch.TagsCapability},
		Channels:     []string{"theprimeagen"},
	}

	// Anonymous client
	client := twitch.NewClient("justinfan321123", "59301", opts)
	if err := client.Connect(); err != nil {
		fmt.Println("Failed to connect: ", err)
		return
	}
	defer client.Disconnect()

	output := make(chan []byte)
	errors := make(chan error)
	go client.ReadMessages(output, errors)

	go func() {
		for err := range errors {
			fmt.Println("Error: ", err)
		}
	}()

	for msg := range output {
		fmt.Printf("Incoming message: %s", msg)
	}
}
