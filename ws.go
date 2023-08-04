package twitch

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	ErrNoConnection = errors.New("no connection to websocket")
)

type Client struct {
	wsEndpoint   string
	connection   *websocket.Conn
	accessToken  string
	capabilities []string
	username     string
}

type Channel struct {
	channelName string
	context     context.Context
	cancel      context.CancelFunc
}

type ClientOpts struct {
	Tls          bool
	Capabilities []string
	Username     string
}

// Create a new client for connecting to Twitch's WS servers
func NewClient(username string, accessToken string, opts *ClientOpts) *Client {
	client := &Client{}
	wsEndpoint := wsTwitchTLS

	if !opts.Tls {
		wsEndpoint = wsTwitch
	}

	client.wsEndpoint = wsEndpoint
	client.accessToken = accessToken
	client.username = username
	return client
}

func NewChannel(channelName string) *Channel {
	context, cancel := context.WithCancel(context.Background())
	channel := &Channel{
		channelName: channelName,
		context:     context,
		cancel:      cancel,
	}
	return channel
}

func (ctx *Client) Connect() error {
	conn, _, err := websocket.DefaultDialer.Dial(ctx.wsEndpoint, nil)
	if err != nil {
		return err
	}

	ctx.connection = conn
	return ctx.authenticateWithTwitchWS()
}

func (ctx *Client) authenticateWithTwitchWS() error {
	capabilities := strings.Join(ctx.capabilities, " ")
	if err := ctx.sendTextMessage(fmt.Sprintf("CAP REQ :%s\r\n", capabilities)); err != nil {
		return err
	}
	if err := ctx.sendTextMessage(fmt.Sprintf("PASS oauth:%s\r\n", ctx.accessToken)); err != nil {
		return err
	}
	if err := ctx.sendTextMessage(fmt.Sprintf("NICK %s\r\n", ctx.username)); err != nil {
		return err
	}
	return nil
}

func (ctx *Client) sendTextMessage(message string) error {
	if err := ctx.connection.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
		return err
	}
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

// Read chat messages from the channel.
//
// This should be run as a goroutine.
//
// Pass in an output and errors channel to ingest the incoming chat messages or errors
func (c *Channel) ReadMessages(client *Client, output chan<- []byte, errors chan<- error) {
	if err := c.joinChannel(client); err != nil {
		errors <- err
	}

	for {
		select {
		case <-c.context.Done():
			if err := c.leaveChannel(client); err != nil {
				errors <- err
			}
			return
		default:
			_, message, err := client.connection.ReadMessage()
			if err != nil {
				errors <- err
				break
			}
			output <- message
		}
	}
}

func (c *Channel) StopReadingMessages() {
	c.cancel()
}

func (c *Channel) joinChannel(client *Client) error {
	if err := client.connection.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("JOIN #%s\r\n", strings.ToLower(c.channelName)))); err != nil {
		return err
	}
	return nil
}

func (c *Channel) leaveChannel(client *Client) error {
	if err := client.connection.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("PART #%s\r\n", strings.ToLower(c.channelName)))); err != nil {
		return err
	}
	return nil
}
