# go-twitchchat-ws

Go library for connecting to Twitch chat via web sockets

## Import
```
go get github.com/dobecad/go-twitchchat-ws
```

## Usage
```go
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
```

## Notes

The intention of this library is simply for maintaining a WS connection to multiple Twitch IRC channels
for viewing the incoming messages. This library does NOT handle IRC message parsing.
