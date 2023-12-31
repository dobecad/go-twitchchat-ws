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
	ErrNoConnection     = errors.New("no connection to websocket")
	ErrNoPingContent    = errors.New("no ping content")
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
	// Connect to Twitch TLS WS endpoint or non-TLS WS endpoint
	Tls bool

	// Specify which capabilities you want
	Capabilities []string

	// Specify channel names you want to join initially
	Channels []string
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

// Establish an authenticated connection with Twitch WS server
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
	ctx.mu.Lock()
	conn, _, err := dialer.Dial(ctx.wsEndpoint, header)
	if err != nil {
		ctx.mu.Unlock()
		return err
	}

	ctx.connection = conn
	ctx.mu.Unlock()
	if err := ctx.authenticateWithTwitchWS(); err != nil {
		return err
	}
	return nil
}

// Send the required messages in order to authenticate with Twitch
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

// Send a text message to the Twitch WS endpoint
func (ctx *Client) sendTextMessage(message string) error {
	// Need mutex to prevent concurrent writes to websocket connection
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	if err := ctx.connection.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
		return err
	}
	return nil
}

// Disconnect from the Twitch WS
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
// Pass in an output and errors channel to ingest the incoming chat messages and errors
func (c *Client) ReadMessages(output chan<- []byte, error_out chan<- error) {
	if err := c.joinChannels(); err != nil {
		error_out <- err
		return
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
			errorCh := make(chan error, 10)
			go func() {
				messageType, message, err := c.connection.ReadMessage()
				if err != nil {
					if closeErr, ok := err.(*websocket.CloseError); ok {
						if err := c.reconnect(5); err != nil {
							errorCh <- fmt.Errorf("websocket connection closed with code %d: %s", closeErr.Code, closeErr.Text)
						}
						if err := c.joinChannels(); err != nil {
							errorCh <- err
						}
						messageCh <- []byte("reconnected to websocket")
					}
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

// Join all of the channels saved in the client
func (c *Client) joinChannels() error {
	for _, channel := range c.channels {
		if err := c.JoinChannel(channel); err != nil {
			return err
		}
	}
	return nil
}

// Reconnect the WS
func (c *Client) reconnect(retryAttempts uint8) error {
	c.connection = nil
	for i := 0; i < 5; i++ {
		fmt.Printf("attempting to reconnect to websocket")
		if err := c.Connect(); err == nil {
			fmt.Printf("reconnect successful")
			return nil
		}
		time.Sleep((1 << uint(i)) * time.Second)
	}

	return fmt.Errorf("websocket connection could not be re-established")
}

func (c *Client) handleTextMessage(message string) error {
	msg := string(message)
	if strings.HasPrefix(message, "PING") {
		pingContent, err := parsePingContent(msg)
		if err != nil {
			return err
		}
		if err := c.sendTextMessage(fmt.Sprintf("PONG %s", pingContent)); err != nil {
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

// Join a new channel to recieve messages for
func (c *Client) JoinChannel(channelName string) error {
	formattedChannelName := strings.ToLower(channelName)
	if err := c.sendTextMessage(fmt.Sprintf("JOIN #%s", formattedChannelName)); err != nil {
		return err
	}
	c.channels = append(c.channels, formattedChannelName)
	return nil
}

// Leave a channel so that you no longer recieve that channels messages
func (c *Client) LeaveChannel(channelName string) error {
	formattedChannelName := strings.ToLower(channelName)
	for index, channel := range c.channels {
		if formattedChannelName == channel {
			if err := c.sendTextMessage(fmt.Sprintf("PART #%s", formattedChannelName)); err != nil {
				return err
			}
			c.channels = append(c.channels[:index], c.channels[index+1:]...)
			return nil
		}
	}
	return ErrChannelNotJoined
}
