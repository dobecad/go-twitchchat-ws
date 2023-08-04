package twitch

import (
	"context"
	"errors"
	"github.com/gorilla/websocket"
)

const (
	wsTwitchTLS = "wss://irc-ws.chat.twitch.tv:443"
	wsTwitch    = "ws://irc-ws.chat.twitch.tv:80"

	TagsCapability       = "twitch.tv/tags"
	CommandsCapability   = "twitch.tv/commands"
	MembershipCapability = "twitch.tv/membership"
)

var (
	ErrNoConnection         = errors.New("no connection to websocket")
	ErrFailedReadingMessage = errors.New("failed to read message from websocket")
	ErrChannelDoesNotExist  = errors.New("channel is not in the channel list")
)

type Client struct {
	channels    []*channel
	wsEndpoint  string
	connection  *websocket.Conn
	accessToken string
}

type channel struct {
	channelName string
	messages    chan []byte
	errors      chan error
	context     context.Context
	cancel      context.CancelFunc
}

type ClientOpts struct {
	Tls      bool
	Channels []string
}

// Create a new client for connecting to Twitch's WS servers
func NewClient(accessToken string, opts *ClientOpts) *Client {
	client := &Client{}
	wsEndpoint := wsTwitchTLS

	if !opts.Tls {
		wsEndpoint = wsTwitch
	}

	client.wsEndpoint = wsEndpoint
	client.accessToken = accessToken
	for _, channelName := range opts.Channels {
		context, cancel := context.WithCancel(context.Background())
		channel := &channel{
			channelName: channelName,
			messages:    make(chan []byte),
			errors:      make(chan error),
			context:     context,
			cancel:      cancel,
		}
		client.channels = append(client.channels, channel)
	}
	return client
}

func (ctx *Client) Connect() error {
	conn, _, err := websocket.DefaultDialer.Dial(ctx.wsEndpoint, nil)
	if err != nil {
		return err
	}

	ctx.connection = conn
	return nil
}

func (ctx *Client) Disconnect() error {
	if ctx.connection == nil {
		return ErrNoConnection
	}

	err := ctx.connection.Close()
	if err != nil {
		return err
	}

	return nil
}

// Return a channel which will have all the messages of the channels
// output into.
func (ctx *Client) Read() <-chan []byte {
	output := make(chan []byte)

	for _, channel := range ctx.channels {
		go ctx.readMessages(output, channel.errors, channel.context)
	}

	return output
}

// Stop reading the messages for a particular channel
func (ctx *Client) StopReading(channelName string) error {
	for _, channel := range ctx.channels {
		if channel.channelName == channelName {
			channel.cancel()
			return nil
		}
	}

	return ErrChannelDoesNotExist
}

func (ctx *Client) RemoveChannel(channelName string) error {
	for index, channel := range ctx.channels {
		if channel.channelName == channelName {
			channel.cancel()
			close(channel.messages)
			close(channel.errors)
			ctx.channels = removeChannel(ctx.channels, index)
			return nil
		}
	}

	return ErrChannelDoesNotExist
}

func removeChannel(slice []*channel, index int) []*channel {
	return append(slice[:index], slice[index+1:]...)
}

func (ctx *Client) readMessages(messages chan<- []byte, errors chan<- error, ctxCancel context.Context) {
	for {
		select {
		case <-ctxCancel.Done():
			return
		default:
			_, message, err := ctx.connection.ReadMessage()
			if err != nil {
				errors <- err
				break
			}
			messages <- message
		}
	}
}
