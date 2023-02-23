package service

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
)

type backend struct {
	ctx         context.Context
	logger      *zerolog.Logger
	addr        string
	dialler     net.Dialer
	active      atomic.Bool
	rmu         sync.RWMutex
	connections map[int]*Conn

	healthcheckInterval time.Duration
}

var _ connManager = (*backend)(nil)

func newBackend(ctx context.Context, logger *zerolog.Logger, address string) (*backend, error) {
	_, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.Wrap(err, "SplitHostPort()")
	}
	dialer := net.Dialer{
		Timeout: 2 * time.Second,
	}
	return &backend{
		ctx:                 ctx,
		logger:              logger,
		addr:                address,
		dialler:             dialer,
		connections:         make(map[int]*Conn),
		healthcheckInterval: 5 * time.Second,
	}, nil
}

// addConn adds connection to the connections map or closes this connection.
func (b *backend) addConn(conn *Conn) {
	select {
	case <-b.ctx.Done():
		conn.Close()
	default:
	}
	b.rmu.Lock()
	defer b.rmu.Unlock()
	b.connections[conn.fd] = conn
}

// delConn deletes connection from the connections map or does nothing.
func (b *backend) delConn(conn *Conn) {
	select {
	case <-b.ctx.Done():
		return
	default:
	}
	b.rmu.Lock()
	defer b.rmu.Unlock()
	delete(b.connections, conn.fd)
}

// getConnCount returns connections count.
func (b *backend) getConnCount() int {
	b.rmu.RLock()
	defer b.rmu.RUnlock()
	return len(b.connections)
}

// run is a blocking function. It starts runHealthcheck goroutine.
// It exits on ctx is done and closes all connections.
func (b *backend) run(wg *sync.WaitGroup) {
	defer wg.Done()

	go b.runHealthcheck()

	// waiting for the graceful shutdown. after this it closes connections
	<-b.ctx.Done()
	b.logger.Info().Str("backend", b.addr).Msg("closing connections")

	b.rmu.RLock()
	defer b.rmu.RUnlock()
	for _, conn := range b.connections {
		conn.Close()
	}
}

// runHealthcheck is blocking method. It is responsible for active health checks of the target backend.
// It exits if backend ctx is done.
func (b *backend) runHealthcheck() {
	ticker := time.NewTicker(b.healthcheckInterval)
	defer ticker.Stop()

	// First check right after run
	netConn, err := b.dialler.DialContext(b.ctx, "tcp", b.addr)
	if err != nil {
		b.setActive(false)
	} else {
		netConn.Close()
		b.setActive(true)
	}

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			netConn, err = b.dialler.DialContext(b.ctx, "tcp", b.addr)
			if err != nil {
				b.setActive(false)
				continue
			}
			netConn.Close()
			b.setActive(true)
		}
	}
}

func (b *backend) setActive(t bool) {
	if b.active.CompareAndSwap(!t, t) {
		b.logger.Info().Str("backend", b.addr).Bool("active", t).Msg("changed active status")
	}
}

// createConn creates new net.Conn to the backend.
func (b *backend) createConn() (net.Conn, error) {
	conn, err := b.dialler.DialContext(b.ctx, "tcp", b.addr)
	if err != nil {
		// passive healthcheck
		b.setActive(false)
		return nil, errors.Wrap(err, "Dial()")
	}
	b.logger.Debug().Str("backend", b.addr).Str("connection", conn.LocalAddr().String()).Msg("new remote connection")
	return conn, nil
}
