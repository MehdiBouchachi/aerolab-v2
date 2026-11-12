// Package mqtt wraps the Eclipse Paho client for the Pi edge service. The
// edge authenticates with its own service account (broker ACL: read device
// uploads, write commands + receipts). Message handlers run on a single
// dispatch goroutine and must only validate + enqueue.
package mqtt

import (
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
)

var ErrNotConnected = errors.New("mqtt: not connected")

type Client struct {
	c         paho.Client
	connected atomic.Bool
	log       *slog.Logger
	onChange  func(connected bool)
}

type Options struct {
	URL, User, Password, SiteID string
	Handler                     func(topic string, payload []byte)
	OnConnectionChange          func(connected bool)
	Log                         *slog.Logger
}

// uploads the Pi consumes
var subscribeLeaves = []string{mc.TopicPresence, mc.TopicState, mc.TopicTelemetry, mc.TopicEvents, mc.TopicCommandResult}

func New(o Options) *Client {
	cl := &Client{log: o.Log, onChange: o.OnConnectionChange}
	opts := paho.NewClientOptions().AddBroker(o.URL).SetClientID("aerolab-edge-" + o.SiteID).
		SetUsername(o.User).SetPassword(o.Password).
		SetCleanSession(true).SetAutoReconnect(true).SetConnectRetry(true).
		SetConnectRetryInterval(3 * time.Second).SetMaxReconnectInterval(30 * time.Second).
		SetKeepAlive(10 * time.Second).SetPingTimeout(5 * time.Second).SetConnectTimeout(5 * time.Second).
		SetWriteTimeout(5 * time.Second).SetOrderMatters(true)
	opts.SetOnConnectHandler(func(c paho.Client) {
		for _, leaf := range subscribeLeaves {
			t := mc.DeviceWildcard(leaf)
			if tok := c.Subscribe(t, 1, func(_ paho.Client, m paho.Message) { o.Handler(m.Topic(), m.Payload()) }); tok.Wait() && tok.Error() != nil {
				cl.log.Error("subscribe failed", "topic", t, "err", tok.Error())
			}
		}
		cl.set(true)
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		cl.log.Warn("mqtt connection lost", "err", err)
		cl.set(false)
	})
	cl.c = paho.NewClient(opts)
	return cl
}

func (c *Client) set(v bool) {
	if c.connected.Swap(v) != v && c.onChange != nil {
		c.onChange(v)
	}
}

// Start begins connecting in the background (never blocks startup: the edge
// must serve its local UI even if the broker is down).
func (c *Client) Start() { c.c.Connect() }

func (c *Client) Stop() {
	if c.c.IsConnected() {
		c.c.Disconnect(250)
	}
	c.set(false)
}

func (c *Client) Connected() bool { return c.connected.Load() }

// Publish queues a message without waiting for the broker round trip so the
// writer / control paths never stall on the network.
func (c *Client) Publish(topic string, qos byte, retained bool, payload []byte) error {
	if !c.connected.Load() {
		return ErrNotConnected
	}
	tok := c.c.Publish(topic, qos, retained, payload)
	select {
	case <-tok.Done():
		if err := tok.Error(); err != nil {
			return fmt.Errorf("publish %s: %w", topic, err)
		}
	default:
	}
	return nil
}
