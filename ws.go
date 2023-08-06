package twitch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

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
	ErrNoConnection  = errors.New("no connection to websocket")
	ErrNoPingContent = errors.New("no ping content")
	ErrChannelNotJoined = errors.New("client is not currently connected to given channel")
)

type Client struct {
	mu           sync.Mutex
	wsEndpoint   string
	connection   *websocket.Conn
	accessToken  string
	capabilities []string
	username     string
	channels     []string
	context      context.Context
	cancel       context.CancelFunc
}

type ClientOpts struct {
	Tls          bool
	Capabilities []string
	Channels     []string
}

// Create a new client for connecting to Twitch's WS servers
func NewClient(username string, accessToken string, opts *ClientOpts) *Client {
	client := &Client{}
	wsEndpoint := wsTwitchTLS

	if !opts.Tls {
		wsEndpoint = wsTwitch
	}

	context, cancel := context.WithCancel(context.Background())
	client.wsEndpoint = wsEndpoint
	client.accessToken = accessToken
	client.username = username
	client.context = context
	client.cancel = cancel
	client.channels = opts.Channels
	client.capabilities = opts.Capabilities
	return client
}

func (ctx *Client) Connect() error {
	dialer := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 30 * time.Second,
		ReadBufferSize:   512,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	header := http.Header{}
	conn, _, err := dialer.Dial(ctx.wsEndpoint, header)
	if err != nil {
		return err
	}

	ctx.connection = conn
	if err := ctx.authenticateWithTwitchWS(); err != nil {
		return err
	}
	return nil
}

func (ctx *Client) authenticateWithTwitchWS() error {
	capabilities := strings.Join(ctx.capabilities, " ")
	if err := ctx.sendTextMessage(fmt.Sprintf("CAP REQ :%s", capabilities)); err != nil {
		return err
	}
	if err := ctx.sendTextMessage(fmt.Sprintf("PASS oauth:%s", ctx.accessToken)); err != nil {
		return err
	}
	if err := ctx.sendTextMessage(fmt.Sprintf("NICK %s", ctx.username)); err != nil {
		return err
	}
	return nil
}

func (ctx *Client) sendTextMessage(message string) error {
	// Need mutex to prevent concurrent writes to websocket connection
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	if err := ctx.connection.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
		return err
	}
	return nil
}

func (ctx *Client) sendPongMessage(message string) error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	if err := ctx.connection.WriteMessage(websocket.PongMessage, []byte(message)); err != nil {
		return err
	}
	return nil
}

func (ctx *Client) Disconnect() error {
	if ctx.connection == nil {
		return ErrNoConnection
	}

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

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
func (c *Client) ReadMessages(output chan<- []byte, error_out chan<- error) {
	for _, channel := range c.channels {
		if err := c.JoinChannel(channel); err != nil {
			error_out <- err
			return
		}
	}

	// When the context is canceled, the goroutine may still be in the
	// process of reading a message from the WebSocket connection
	// which could result in a "use of closed network connection" error.
	// To avoid this issue, ensure that the goroutine stops reading from
	// the connection immediately after the context is canceled.
	// One way to achieve this is by using a separate done channel
	// to signal the goroutine to stop reading when the context is canceled.

	for {
		select {
		case <-c.context.Done():
			for _, channel := range c.channels {
				if err := c.LeaveChannel(channel); err != nil {
					error_out <- err
				}
			}
			return
		default:
			// Use a separate goroutine for reading from the connection
			// This is to prevent cancelling the goroutine in the middle of reading
			// a message from the websocket
			messageCh := make(chan []byte, 1)
			errorCh := make(chan error, 2)
			go func() {
				messageType, message, err := c.connection.ReadMessage()
				if err != nil {
					if closeErr, ok := err.(*websocket.CloseError); ok {
						errorCh <- fmt.Errorf("websocket connection closed with code %d: %s", closeErr.Code, closeErr.Text)
					}
					errorCh <- fmt.Errorf("error reading message from connection for channel: %s", err)
				} else {
					if messageType == websocket.TextMessage {
						if err := c.handleTextMessage(string(message)); err != nil {
							errorCh <- err
						}
					}
					messageCh <- message
				}
			}()

			// Wait for either a message or an error, or until the context is canceled
			select {
			case <-c.context.Done():
				for _, channel := range c.channels {
					if err := c.LeaveChannel(channel); err != nil {
						errorCh <- err
					}
				}
				return
			case err := <-errorCh:
				error_out <- err
				close(messageCh)
				close(errorCh)
				return
			case message := <-messageCh:
				output <- message
			}

			// Close the messageCh and errorCh to release resources
			close(messageCh)
			close(errorCh)
		}
	}
}

func (c *Client) handleTextMessage(message string) error {
	msg := string(message)
	if strings.HasPrefix(message, "PING") {
		pingContent, err := parsePingContent(msg)
		if err != nil {
			return err
		}
		if err := c.sendPongMessage(pingContent); err != nil {
			return err
		}
	}
	return nil
}

func parsePingContent(message string) (string, error) {
	pingMsg := strings.SplitN(message, " ", 2)
	if len(pingMsg) == 2 {
		return pingMsg[1], nil
	}
	return "", ErrNoPingContent
}

func (c *Client) StopReadingMessages() {
	c.cancel()
}

func (c *Client) JoinChannel(channelName string) error {
	if err := c.sendTextMessage(fmt.Sprintf("JOIN #%s", strings.ToLower(channelName))); err != nil {
		return err
	}
	return nil
}

func (c *Client) LeaveChannel(channelName string) error {
	for _, channel := range c.channels {
		if channelName == channel {
			if err := c.sendTextMessage(fmt.Sprintf("PART #%s", strings.ToLower(channelName))); err != nil {
				return err
			}
			return nil
		}
	}
	return ErrChannelNotJoined
}
