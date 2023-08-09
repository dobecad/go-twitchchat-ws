package main

import (
	"fmt"

	twitch "github.com/dobecad/go-twitchchat-ws"
)

func main() {
	opts := &twitch.ClientOpts{
		Tls:          true,
		Capabilities: []string{twitch.TagsCapability},
		Channels:     []string{"stanz", "wardell"},
	}
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

	for i := 0; i < 50; i++ {
		msg := <-output
		fmt.Printf("Incoming message: %s", msg)
	}
	if err := client.LeaveChannel("wardell"); err != nil {
		fmt.Println("Failed to leave channel: ", err)
	}

	for i := 0; i < 10; i++ {
		msg := <-output
		fmt.Printf("Incoming message: %s", msg)
	}
	
	if err := client.LeaveChannel("stanz"); err != nil {
		fmt.Println("Failed to leave channel: ", err)
	}
	close(output)
	close(errors)
}
