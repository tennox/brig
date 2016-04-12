package ipfsutil

import (
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/net/context"

	"github.com/disorganizer/brig/util"
	"github.com/ipfs/go-ipfs/core/corenet"

	manet "gx/ipfs/QmYVqhVfbK4BKvbW88Lhm26b3ud14sTBvcm1H7uWUx1Fkp/go-multiaddr-net"
	p2pnet "gx/ipfs/QmZMehXD2w81qeVJP6r1mmocxwsD7kqAvuzGm2QWDw1H88/go-libp2p/p2p/net"
	peer "gx/ipfs/QmZMehXD2w81qeVJP6r1mmocxwsD7kqAvuzGm2QWDw1H88/go-libp2p/p2p/peer"
	protocol "gx/ipfs/QmZMehXD2w81qeVJP6r1mmocxwsD7kqAvuzGm2QWDw1H88/go-libp2p/p2p/protocol"
)

type streamConn struct {
	stream p2pnet.Stream
	torw   *util.TimeoutReadWriter
}

func wrapStream(stream p2pnet.Stream) net.Conn {
	return &streamConn{
		stream: stream,
		torw:   util.NewTimeoutReadWriter(stream, 20*time.Minute),
	}
}

func (sc *streamConn) Read(buf []byte) (int, error) {
	return sc.torw.Read(buf)
}

func (sc *streamConn) Write(buf []byte) (n int, err error) {
	return sc.torw.Write(buf)
}

func (sc *streamConn) Close() error {
	return sc.stream.Close()
}

func (sc *streamConn) LocalAddr() net.Addr {
	if c := sc.stream.Conn(); c != nil {
		addr, err := manet.ToNetAddr(c.LocalMultiaddr())
		if err != nil {
			panic("TODO: manet sucks")
		}

		return addr
	}

	return nil
}

func (sc *streamConn) RemoteAddr() net.Addr {
	if c := sc.stream.Conn(); c != nil {
		addr, err := manet.ToNetAddr(c.RemoteMultiaddr())
		if err != nil {
			panic("TODO: manet sucks")
		}

		return addr
	}

	return nil
}

func (sc *streamConn) SetDeadline(t time.Time) error {
	return sc.torw.SetDeadline(t)
}
func (sc *streamConn) SetReadDeadline(t time.Time) error {
	return sc.torw.SetReadDeadline(t)
}
func (sc *streamConn) SetWriteDeadline(t time.Time) error {
	return sc.torw.SetWriteDeadline(t)
}

// TODO: Taken and slightly modified from corenet.go
type ipfsListener struct {
	conCh  chan p2pnet.Stream
	ctx    context.Context
	cancel func()
}

func (il *ipfsListener) Accept() (net.Conn, error) {
	select {
	case c := <-il.conCh:
		return wrapStream(c), nil
	case <-il.ctx.Done():
		return nil, il.ctx.Err()
	}
}

func (il *ipfsListener) Close() error {
	il.cancel()

	// TODO: unregister handler from peerhost
	return nil
}

func (il *ipfsListener) Addr() net.Addr {
	return &net.TCPAddr{
		IP:   net.IPv4(0, 0, 0, 0),
		Port: 0,
	}
}

func (nd *Node) Listen(proto string) (net.Listener, error) {
	if !nd.IsOnline() {
		return nil, fmt.Errorf("Not online") // TODO: common error?
	}

	node, err := nd.proc()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(node.Context())

	list := &ipfsListener{
		conCh:  make(chan p2pnet.Stream),
		ctx:    ctx,
		cancel: cancel,
	}

	protoID := protocol.ID(proto)
	node.PeerHost.SetStreamHandler(protoID, func(s p2pnet.Stream) {
		fmt.Println("Received a stream", s)
		select {
		case list.conCh <- s:
		case <-ctx.Done():
			s.Close()
		}
	})

	return list, nil
}

func (nd *Node) Dial(peerHash, protocol string) (net.Conn, error) {
	peerID, err := peer.IDB58Decode(peerHash)
	if err != nil {
		return nil, err
	}

	stream, err := corenet.Dial(nd.ipfsNode, peerID, protocol)
	if err != nil {
		return nil, err
	}

	fmt.Println("wrap stream", peerID)
	return wrapStream(stream), nil
}

type Pinger struct {
	lastSeen  time.Time
	roundtrip time.Duration
	cancel    func()
	mu        sync.Mutex
}

func (p *Pinger) LastSeen() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.lastSeen
}

func (p *Pinger) Roundtrip() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.roundtrip
}

func (p *Pinger) Close() error {
	p.cancel()
	return nil
}

func (nd *Node) Ping(peerHash string) (*Pinger, error) {
	if !nd.IsOnline() {
		return nil, fmt.Errorf("Not online") // TODO: common error?
	}

	node, err := nd.proc()
	if err != nil {
		return nil, err
	}

	peerID, err := peer.IDB58Decode(peerHash)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(nd.Context)
	pingCh, err := node.Ping.Ping(ctx, peerID)
	if err != nil {
		return nil, err
	}

	pinger := &Pinger{
		lastSeen: time.Now(),
		cancel:   cancel,
	}

	go func() {
		for roundtrip := range pingCh {
			pinger.mu.Lock()
			pinger.roundtrip = roundtrip
			pinger.lastSeen = time.Now()
			pinger.mu.Unlock()
		}
	}()

	return pinger, nil
}