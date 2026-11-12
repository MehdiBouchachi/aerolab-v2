package esp32

import (
	"context"
	"encoding/json"
	"math/rand"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
)

// netLink is the mqtt_client component: it owns the broker session and is the
// only code that talks to the network. It reconnects with exponential
// backoff + jitter and never blocks the control loop.
type netLink struct {
	d          *Device
	enabled    atomic.Bool
	reconnects atomic.Int64

	mu        sync.Mutex
	client    paho.Client
	conn      net.Conn
	connected bool
	nonce     string
	lost      chan struct{}
	rng       *rand.Rand
}

func newNetLink(d *Device) *netLink {
	n := &netLink{d: d, rng: rand.New(rand.NewSource(d.cfg.Seed + 7919))}
	n.enabled.Store(true)
	return n
}

func (n *netLink) isConnected() bool { n.mu.Lock(); defer n.mu.Unlock(); return n.connected }

// dropConnection kills the TCP connection abruptly (no MQTT DISCONNECT), the
// way a Wi-Fi loss looks to the broker, so the Last Will is published.
func (n *netLink) dropConnection() {
	n.mu.Lock()
	c := n.conn
	n.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (n *netLink) jitter(d time.Duration) time.Duration {
	n.mu.Lock()
	defer n.mu.Unlock()
	return time.Duration(float64(d) * (0.5 + n.rng.Float64()/2)) // 50-100% of backoff
}

func (n *netLink) run(ctx context.Context) {
	backoff := n.d.cfg.ReconnectMin
	// Stagger first connect so a whole fleet does not stampede the broker.
	if !sleepCtx(ctx, n.jitter(n.d.cfg.ReconnectMin)/2) {
		return
	}
	for ctx.Err() == nil {
		if !n.enabled.Load() {
			backoff = n.d.cfg.ReconnectMin
			if !sleepCtx(ctx, 100*time.Millisecond) {
				return
			}
			continue
		}
		if err := n.connectOnce(); err != nil {
			n.d.cfg.Logger.Debug("connect failed", "device", n.d.cfg.DeviceID, "err", err, "retry_in", backoff)
			if !sleepCtx(ctx, n.jitter(backoff)) {
				return
			}
			if backoff *= 2; backoff > n.d.cfg.ReconnectMax {
				backoff = n.d.cfg.ReconnectMax
			}
			continue
		}
		backoff = n.d.cfg.ReconnectMin
		n.serve(ctx)
		n.teardown(ctx.Err() != nil)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (n *netLink) presence(status mc.PresenceStatus, nonce string) []byte {
	n.d.mu.Lock()
	h := n.d.header()
	n.d.mu.Unlock()
	b, _ := json.Marshal(mc.Presence{Header: h, Status: status, FirmwareVersion: n.d.cfg.Version, SessionNonce: nonce})
	return b
}

func (n *netLink) connectOnce() error {
	d := n.d
	nonce := randToken("nonce-", 8)
	opts := paho.NewClientOptions().AddBroker(d.cfg.BrokerURL).SetClientID(d.cfg.DeviceID).
		SetUsername(d.cfg.Username).SetPassword(d.cfg.Password).
		SetCleanSession(true).SetAutoReconnect(false).SetConnectRetry(false).SetOrderMatters(false).
		SetKeepAlive(10 * time.Second).SetPingTimeout(5 * time.Second).SetConnectTimeout(5 * time.Second).
		SetWriteTimeout(5 * time.Second)
	opts.SetWill(mc.DeviceTopic(d.cfg.DeviceID, mc.TopicPresence), string(n.presence(mc.PresenceOffline, nonce)), 1, true)
	lost := make(chan struct{})
	var once sync.Once
	opts.SetConnectionLostHandler(func(paho.Client, error) { once.Do(func() { close(lost) }) })
	opts.SetCustomOpenConnectionFn(func(uri *url.URL, o paho.ClientOptions) (net.Conn, error) {
		c, err := net.DialTimeout("tcp", uri.Host, o.ConnectTimeout)
		if err == nil {
			n.mu.Lock()
			n.conn = c
			n.mu.Unlock()
		}
		return c, err
	})
	cl := paho.NewClient(opts)
	tok := cl.Connect()
	if !tok.WaitTimeout(6*time.Second) || tok.Error() != nil {
		if tok.Error() != nil {
			cl.Disconnect(0)
			return tok.Error()
		}
		cl.Disconnect(0)
		return context.DeadlineExceeded
	}
	subs := map[string]byte{mc.DeviceTopic(d.cfg.DeviceID, mc.TopicCommands): 1, mc.DeviceTopic(d.cfg.DeviceID, mc.TopicReceipts): 1}
	st := cl.SubscribeMultiple(subs, func(_ paho.Client, m paho.Message) {
		switch m.Topic() {
		case mc.DeviceTopic(d.cfg.DeviceID, mc.TopicCommands):
			d.handleCommand(m.Payload(), nonce)
		case mc.DeviceTopic(d.cfg.DeviceID, mc.TopicReceipts):
			d.handleReceipt(m.Payload())
		}
	})
	if !st.WaitTimeout(5*time.Second) || st.Error() != nil {
		cl.Disconnect(0)
		return context.DeadlineExceeded
	}
	n.mu.Lock()
	n.client, n.connected, n.nonce, n.lost = cl, true, nonce, lost
	n.mu.Unlock()
	n.reconnects.Add(1)
	// Reconnect order from the spec: presence + state FIRST, then backlog.
	cl.Publish(mc.DeviceTopic(d.cfg.DeviceID, mc.TopicPresence), 1, true, n.presence(mc.PresenceOnline, nonce))
	n.publishStateNow()
	return nil
}

func (n *netLink) teardown(graceful bool) {
	n.mu.Lock()
	cl, nonce := n.client, n.nonce
	n.connected = false
	n.client = nil
	n.conn = nil
	n.mu.Unlock()
	if cl == nil {
		return
	}
	if graceful && cl.IsConnected() {
		cl.Publish(mc.DeviceTopic(n.d.cfg.DeviceID, mc.TopicPresence), 1, true, n.presence(mc.PresenceOffline, nonce)).WaitTimeout(time.Second)
	}
	cl.Disconnect(100)
}

func (n *netLink) serve(ctx context.Context) {
	hb := time.NewTicker(n.d.cfg.HeartbeatEvery)
	defer hb.Stop()
	flush := time.NewTicker(250 * time.Millisecond)
	defer flush.Stop()
	n.mu.Lock()
	lost := n.lost
	n.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case <-lost:
			return
		case <-hb.C:
			n.publishStateNow()
		case <-n.d.notify:
			n.flushJournal()
		case <-flush.C:
			n.flushJournal()
		}
		if !n.enabled.Load() {
			return
		}
	}
}

func (n *netLink) current() (paho.Client, string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.client, n.nonce, n.connected && n.client != nil
}

func (n *netLink) publishStateNow() {
	cl, nonce, ok := n.current()
	if !ok {
		return
	}
	b, err := json.Marshal(n.d.buildState(nonce))
	if err != nil {
		return
	}
	cl.Publish(mc.DeviceTopic(n.d.cfg.DeviceID, mc.TopicState), 0, false, b)
}

func (n *netLink) publishResult(r mc.CommandResult) {
	if n.d.shouldDropResult() {
		return
	}
	cl, _, ok := n.current()
	if !ok {
		return
	}
	b, _ := json.Marshal(r)
	cl.Publish(mc.DeviceTopic(n.d.cfg.DeviceID, mc.TopicCommandResult), 1, false, b)
}

// flushJournal sends/re-sends unreceipted records in bounded batches. A
// record leaves the journal only when a Pi receipt names it.
func (n *netLink) flushJournal() {
	cl, _, ok := n.current()
	if !ok {
		return
	}
	d := n.d
	d.mu.Lock()
	batch := d.jr.sendable(time.Now(), d.cfg.ReceiptTimeout, d.cfg.ReplayBatch, d.cfg.MaxInFlight)
	type out struct {
		topic string
		b     []byte
	}
	var outs []out
	now := time.Now()
	for _, r := range batch {
		r.lastSent = now
		outs = append(outs, out{mc.DeviceTopic(d.cfg.DeviceID, r.topic), r.payload})
	}
	more := len(batch) == d.cfg.ReplayBatch
	d.mu.Unlock()
	for _, o := range outs {
		cl.Publish(o.topic, 1, false, o.b)
	}
	if more { // keep draining a backlog in bounded steps
		select {
		case d.notify <- struct{}{}:
		default:
		}
	}
}
